// Package sumithlive adapts vendorclients/sumithlive to the vendors.Adapter
// contract. Reference implementation for any HTTP+embed-URL vendor —
// vendors/chemitoapi mirrors this shape.
package sumithlive

import (
	"context"
	"strconv"
	"sync"
	"time"

	"mediamtx-console/domain"
	rawclient "mediamtx-console/vendorclients/sumithlive"
)

// Config is the per-vendor block loaded from config/vendors.go.
// DefaultProjectID is account-wide (confirmed same value across every
// vehicle on the one real account tested) and used whenever a request
// doesn't override it via VendorParams["projectId"].
type Config struct {
	BaseURL, Username, Password string
	DefaultProjectID            string
}

type Adapter struct {
	cfg Config

	// tokenMu/token/tokenAt hold the shared access token. See accessToken.
	tokenMu sync.Mutex
	token   string
	tokenAt time.Time
}

func New(cfg Config) *Adapter { return &Adapter{cfg: cfg} }

// tokenTTL is how long an access token is reused before re-authenticating.
// The vendor documents no expiry, so this is a conservative refresh; a
// token rejected earlier than that is handled by the retry in
// ResolveLiveSource.
const tokenTTL = 10 * time.Minute

// accessToken returns the shared access token, authenticating only when
// there isn't a usable one. reused reports whether it came from the cache,
// which tells the caller whether a failure is worth retrying with a fresh
// one.
//
// One token for the whole process rather than one per call, for the same
// reason vendors/chemitoapi shares its key: opening a grid of cameras
// otherwise fires one login per tile at the same instant. Chemito was
// confirmed to keep a single session per account and drop all but the last
// (see that adapter's session()); Sumith has not been shown to do that, but
// the redundant logins are pointless either way, and its stream URLs are
// vendor-hosted so a stale token cannot cut a playing stream.
//
// The lock is held across the login so concurrent first-calls queue behind
// one round trip instead of each starting their own.
func (a *Adapter) accessToken() (tok string, reused bool, err error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	if a.token != "" && time.Since(a.tokenAt) < tokenTTL {
		return a.token, true, nil
	}
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	tok, err = client.GetAccessToken(a.cfg.Username, a.cfg.Password)
	if err != nil {
		return "", false, domain.WrapVendorErr("sumithlive", "login", err)
	}
	a.token, a.tokenAt = tok, time.Now()
	return tok, false, nil
}

// forgetToken drops the cached token so the next call re-authenticates.
func (a *Adapter) forgetToken() {
	a.tokenMu.Lock()
	a.token = ""
	a.tokenMu.Unlock()
}

func (a *Adapter) Name() string { return "sumithlive" }

// ResolveLiveSource logs in and calls getLiveStreamingLink (this is the
// call that actually tells the device to start streaming — not a passive
// lookup). The documented result is jspLink, an embeddable page — but
// that page turns out to just render a fixed 4-camera grid by fetching
// direct per-camera HLS URLs (confirmed live, undocumented — see
// vendorclients/sumithlive doc comment). We decode the device id from
// jspLink and try the real HLS URL for the requested channel first: if
// it's actually live (some channels 404 depending on which camera inputs
// are connected), KindHLS gives the frontend a direct, playable-without-
// an-iframe URL. If the probe fails for any reason, fall back to the
// original KindEmbed jspLink — no regression versus the old behavior.
func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	token, reused, err := a.accessToken()
	if err != nil {
		return domain.LiveSource{}, err
	}

	plateNo := req.VendorParams["plateNo"]
	channel := req.Cam
	if channel == 0 {
		channel = 1
	}
	projectID := atoiOr(req.VendorParams["projectId"], atoiOr(a.cfg.DefaultProjectID, 0))

	link, err := client.GetLiveStreamingLink(token, plateNo, channel, projectID)
	// Only retry when the token came from the cache: a freshly minted one
	// that fails failed for a real reason (device offline, bad plate), and
	// re-authenticating would just double every such call.
	if err != nil && reused {
		a.forgetToken()
		if token, _, err = a.accessToken(); err == nil {
			link, err = client.GetLiveStreamingLink(token, plateNo, channel, projectID)
		}
	}
	if err != nil {
		return domain.LiveSource{}, domain.WrapVendorErr("sumithlive", "resolve live streaming link", err)
	}

	if deviceID, derr := rawclient.DecodeDeviceID(link); derr == nil {
		hlsURL := client.HLSURL(deviceID, channel)
		if client.ProbeLive(hlsURL) {
			return domain.LiveSource{Kind: domain.KindHLS, HLSURL: hlsURL}, nil
		}
	}

	return domain.LiveSource{Kind: domain.KindEmbed, EmbedURL: link}, nil
}

// ListCameras returns every vehicle visible to this account (not filtered
// by params — params is for callers that already know a specific vehicle,
// which this account-wide listing call doesn't need). Online is a
// best-effort signal: true only when the vehicle's last GPS fix is a real
// (nonzero) position, since this API exposes no per-camera status.
func (a *Adapter) ListCameras(ctx context.Context, params map[string]string) ([]domain.Camera, error) {
	client := rawclient.NewClient(a.cfg.BaseURL, nil)
	vehicles, err := client.ListVehicles(a.cfg.Username, a.cfg.Password)
	if err != nil {
		return nil, domain.WrapVendorErr("sumithlive", "list vehicles", err)
	}

	cams := make([]domain.Camera, len(vehicles))
	for i, v := range vehicles {
		lat, _ := strconv.ParseFloat(v.Latitude, 64)
		lng, _ := strconv.ParseFloat(v.Longitude, 64)
		cams[i] = domain.Camera{
			VendorID: v.PlateNo,
			Label:    v.PlateNo,
			Online:   lat != 0 || lng != 0,
			// channel isn't included — ResolveLiveSource takes it from
			// StreamRequest.Cam, not VendorParams, since it varies per
			// request (which camera) rather than per vehicle.
			VendorParams: map[string]string{
				"plateNo":   v.PlateNo,
				"projectId": a.cfg.DefaultProjectID,
			},
		}
	}
	return cams, nil
}

func atoiOr(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if s == "" {
		return def
	}
	return n
}
