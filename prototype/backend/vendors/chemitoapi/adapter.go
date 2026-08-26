// Package chemitoapi adapts vendorclients/chemitoapi (Chemito's HTTP API,
// docs/vendors/chemito/PMIDTC_CIPLAPIS.xlsx) to the vendors.Adapter
// contract. Distinct from Chemito's other integration path
// (vendorclients/n9m + n9mserver, a device-initiated TCP protocol) — this
// one is a plain HTTP vendor, same shape as vendors/sumithlive.
package chemitoapi

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"mediamtx-console/domain"
	rawclient "mediamtx-console/vendorclients/chemitoapi"
)

// Config is the per-vendor block loaded from config/vendors.go.
type Config struct {
	BaseURL, Username, Password string
}

type Adapter struct {
	cfg Config

	// keyMu/key/keyAt hold the one shared login session. See session().
	keyMu sync.Mutex
	key   string
	keyAt time.Time

	// portMu/portByChannel pin each {terid}_{chl} to one of the account's
	// live-video ports. See portFor.
	portMu        sync.Mutex
	portByChannel map[string]int
}

func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, portByChannel: make(map[string]int)}
}

// portFor picks which of the account's live-video ports a channel streams
// on, and remembers it so a reconnect lands on the same one.
//
// Not ports[0] for everything, which is what this used to do: the API doc
// (PMIDTC_CIPLAPIS.xlsx §4) states "A port can configure up to 4 channels"
// and recommends "4-way video on one port and 16-way video at most at the
// same time". The account exposes 4 ports (12060-12063), so the real
// ceiling is 16 concurrent channels — but only if they are spread. Three
// buses x 4 cams all on 12060 is 12 channels on a port that holds 4, and
// the ones past the limit come back as HTTP 408 or an immediate EOF,
// indistinguishable from a dead device.
//
// Least-loaded rather than round-robin so channels released by StopStream
// (or never started) don't leave permanent holes in the distribution.
func (a *Adapter) portFor(ports []rawclient.VideoPort, channelKey string) int {
	a.portMu.Lock()
	defer a.portMu.Unlock()

	if p, ok := a.portByChannel[channelKey]; ok {
		for _, vp := range ports {
			if vp.Port == p {
				return p // still offered by the account — keep it stable
			}
		}
	}

	load := make(map[int]int, len(ports))
	for _, p := range a.portByChannel {
		load[p]++
	}
	best := ports[0].Port
	for _, vp := range ports {
		if load[vp.Port] < load[best] {
			best = vp.Port
		}
	}
	a.portByChannel[channelKey] = best
	return best
}

func (a *Adapter) Name() string { return "chemitoapi" }

// keyTTL is how long a verify key is reused before logging in again. The
// doc gives no expiry, so this is a conservative refresh; an actually-
// expired key is also caught by the retry in resolveURL.
const keyTTL = 10 * time.Minute

// session returns a client carrying the account's shared verify key,
// logging in only when there isn't a usable one.
//
// One key for the whole process, not one per call, because the server
// keeps a single active session per account: a second Login invalidates
// the first key and every stream token minted from it (see Client.UseKey).
// The old code logged in inside every Upstream call, so opening N cameras
// at once made N logins race and killed N-1 of the tokens — the cameras
// were live and streamed fine one at a time, but a grid showed one tile.
// Confirmed live 2026-08-26: 6 concurrent channels went 1/6 with per-call
// logins, 6/6 sharing a key.
//
// The lock is held across the login on purpose: concurrent first-connects
// must queue behind one login rather than each start their own.
func (a *Adapter) session() (*rawclient.Client, error) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()

	c := rawclient.NewClient(a.cfg.BaseURL, nil)
	if a.key != "" && time.Since(a.keyAt) < keyTTL {
		c.UseKey(a.key)
		return c, nil
	}
	key, err := c.Login(a.cfg.Username, a.cfg.Password)
	if err != nil {
		return nil, domain.WrapVendorErr("chemitoapi", "login", err)
	}
	a.key, a.keyAt = key, time.Now()
	return c, nil
}

// forgetKey drops the cached key so the next session() logs in again.
func (a *Adapter) forgetKey() {
	a.keyMu.Lock()
	a.key = ""
	a.keyMu.Unlock()
}

// isAuthError reports whether err means the key is no longer accepted —
// the codes from Appendix 1 that a re-login can fix. Anything else (device
// offline, no data) must not trigger one, since a needless login would
// invalidate the key every other viewer is holding.
func isAuthError(err error) bool {
	var apiErr *rawclient.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case 203, 204, 209, 210: // no authority, expired, unauthorized key, key error
		return true
	}
	return false
}

// resolveURL does one full resolve (ports + live-video URL) on the shared
// session, retrying once with a fresh login if the key was rejected.
func (a *Adapter) resolveURL(terid string, channel int, audio bool, st rawclient.LiveStreamType) (string, error) {
	attempt := func() (string, error) {
		client, err := a.session()
		if err != nil {
			return "", err
		}
		ports, err := client.LivePorts()
		if err != nil {
			return "", domain.WrapVendorErr("chemitoapi", "list live ports", err)
		}
		if len(ports) == 0 {
			return "", &domain.VendorError{
				Vendor: "chemitoapi", Op: "resolve live source", Code: "no_live_ports", Retryable: true,
			}
		}
		url, err := client.LiveVideoURL(terid, channel, audio, st, a.portFor(ports, terid+"_"+strconv.Itoa(channel)))
		if err != nil {
			return "", domain.WrapVendorErr("chemitoapi", "resolve live video url", err)
		}
		return url, nil
	}

	url, err := attempt()
	if err != nil && isAuthError(err) {
		a.forgetKey()
		return attempt()
	}
	return url, err
}

// ResolveLiveSource returns KindFLV: the vendor's HTTP-FLV live stream,
// played directly in the browser by mpegts.js behind our /api/flv/{key}
// proxy — no ffmpeg, no MediaMTX. Upstream logs in, resolves an available
// relay port (§4 of the doc's operation steps — "Get video port
// information" then "Get device list"), and requests the FLV URL fresh on
// every viewer connect.
//
// This used to be KindRTSP (ffmpeg remux into MediaMTX). Dropped because
// MediaMTX's RTSP muxer rejects Chemito's non-monotonic DTS (it reports 0
// repeatedly) with "Error submitting a packet to the muxer: Broken pipe",
// and since every channel of one device carries the same defect they all
// got dropped at the same moment — the real cause behind "all cams vanish
// together". mpegts.js does its own timestamp remapping, so the defect
// stops mattering. Bypassing the remux also means channels no longer
// compete for ffmpeg processes or share a publisher's fate.
//
// Upstream must re-resolve per connect, not freeze one URL: confirmed live
// 2026-08-19 that Chemito's login token / live-video URL is single-use or
// short-lived, so a cached URL is already dead for the next viewer.
//
// The device's own "transmitport" field (from ListDevices) is NOT a
// connectable stream port — confirmed live 2026-08-19: connecting to it
// resets immediately, while a port from LivePorts() (e.g. 12060) serves the
// actual FLV stream. transmitport only reappears as the response's fixed
// "svrport" value, unrelated to the host:port you connect to.
func (a *Adapter) ResolveLiveSource(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	terid := req.VendorParams["terid"]
	channel := req.Cam // the vendor's device channel — same number as our own {bus}_{cam}, not a separate config value
	if channel == 0 {
		channel = 1
	}
	st := rawclient.LiveStreamSub
	if req.Main {
		st = rawclient.LiveStreamMain
	}

	upstream := func(ctx context.Context) (string, error) {
		return a.resolveURL(terid, channel, req.Audio, st)
	}

	return domain.LiveSource{Kind: domain.KindFLV, Upstream: upstream, HasAudio: req.Audio}, nil
}

// ListCameras returns every device registered on this account, via the
// undocumented-but-confirmed-real ListDevices call (see
// vendorclients/chemitoapi.ListDevices). Online is left false: device
// registration doesn't mean the device is currently connected — that's
// only knowable via LivePorts, checked at actual start time, not here
// (calling it once per device on every roster sweep would be one more
// vendor round trip than this account-wide listing needs).
func (a *Adapter) ListCameras(ctx context.Context, params map[string]string) ([]domain.Camera, error) {
	// Shared session, same as resolveURL: a roster sweep that logged in on
	// its own would invalidate the key every active viewer is streaming on.
	client, err := a.session()
	if err != nil {
		return nil, err
	}
	devices, err := client.ListDevices()
	if err != nil {
		return nil, domain.WrapVendorErr("chemitoapi", "list devices", err)
	}

	cams := make([]domain.Camera, len(devices))
	for i, d := range devices {
		// VendorID is the bus identity the frontend sees (fleet id, key
		// into config/buses.json) — that must be the plate, same as
		// sumithlive, not the vendor's internal terid. terid only lives in
		// VendorParams, for ResolveLiveSource to actually call the vendor.
		id := d.CarLicence
		if id == "" {
			id = d.Terid // no plate on file for this device — fall back rather than drop it
		}
		cams[i] = domain.Camera{
			VendorID:     id,
			Label:        id,
			VendorParams: map[string]string{"terid": d.Terid},
			Channels:     d.ChannelCount,
		}
	}
	return cams, nil
}
