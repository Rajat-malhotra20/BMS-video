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

// maxActiveChannels caps how many channels this adapter will keep live at
// once, below the account's real 16-channel ceiling (4 ports x 4 channels,
// see portFor). The 4-channel gap is deliberate slack, not waste: it covers
// the brief overlap between an old connection closing and its replacement
// opening on refresh/reconnect, plus any manual dashboard/curl use against
// the same account — headroom that doesn't exist at 16/16, where a single
// overlapping connection 408s an unrelated channel (see portFor's doc).
const maxActiveChannels = 12

// activeSlotTTL is a crash-safety backstop, not the normal release path.
// The FLV hub (flvhub.go) holds one real device connection open per
// channel for as long as it has viewers — often far longer than any short
// timeout — and calls Release the moment it actually tears that connection
// down (flvHub.onChannelClosed), which is the deterministic way a slot is
// meant to be freed. This TTL only matters if that callback is ever missed
// (a panic mid-teardown, a process restart that drops the hub's state but
// not this adapter's), so it's set long enough to never fire during a
// legitimately long-lived, healthy stream.
const activeSlotTTL = 30 * time.Minute

type Adapter struct {
	cfg Config

	// keyMu/key hold the one shared login session. See session().
	keyMu sync.Mutex
	key   string

	// portMu/portByChannel pin each {terid}_{chl} to one of the account's
	// live-video ports. See portFor.
	portMu        sync.Mutex
	portByChannel map[string]int

	// activeMu/active track which channel keys currently hold one of the
	// maxActiveChannels slots, and when that slot was last renewed. See
	// acquireSlot and Release.
	activeMu sync.Mutex
	active   map[string]time.Time
}

func New(cfg Config) *Adapter {
	return &Adapter{cfg: cfg, portByChannel: make(map[string]int), active: make(map[string]time.Time)}
}

// acquireSlot reserves capacity for channelKey, evicting any slots whose
// TTL has lapsed first. A channel already holding a slot just gets its
// timestamp renewed — a reconnect of an already-counted channel must never
// be turned away for being "over capacity" against its own past self.
func (a *Adapter) acquireSlot(channelKey string) error {
	a.activeMu.Lock()
	defer a.activeMu.Unlock()

	now := time.Now()
	for k, last := range a.active {
		if now.Sub(last) > activeSlotTTL {
			delete(a.active, k)
		}
	}

	if _, ok := a.active[channelKey]; ok {
		a.active[channelKey] = now
		return nil
	}

	if len(a.active) >= maxActiveChannels {
		return &domain.VendorError{
			Vendor: "chemitoapi", Op: "resolve live video url", Code: "capacity_limit",
			Retryable: true,
		}
	}

	a.active[channelKey] = now
	return nil
}

// Release frees channelKey's capacity slot immediately, for callers that
// know a channel has actually stopped — in practice, only the FLV hub's
// onChannelClosed callback, once every viewer of that camera is gone and
// its idle grace has lapsed — rather than waiting for activeSlotTTL to
// reclaim it. Safe to call for a key that never held a slot.
func (a *Adapter) Release(channelKey string) {
	a.activeMu.Lock()
	delete(a.active, channelKey)
	a.activeMu.Unlock()
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

// session returns a client carrying the account's shared verify key,
// logging in only when there isn't a cached one yet.
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
// No proactive TTL-based refresh either, for the same reason: the doc
// gives no expiry, and a Login() call is never harmless here — it kills
// every camera streaming on the previous key. A time-based refresh was
// tried and rejected: it re-logged-in every 10 minutes regardless of
// active viewers, which reads to the vendor as someone else logging in
// and silently drops every open stream on that schedule. An actually-
// expired or externally-invalidated key is instead caught by the
// isAuthError retry in resolveURL, which only re-logs-in once a real call
// has actually failed.
//
// The lock is held across the login on purpose: concurrent first-connects
// must queue behind one login rather than each start their own.
func (a *Adapter) session() (*rawclient.Client, error) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()

	c := rawclient.NewClient(a.cfg.BaseURL, nil)
	if a.key != "" {
		c.UseKey(a.key)
		return c, nil
	}
	key, err := c.Login(a.cfg.Username, a.cfg.Password)
	if err != nil {
		return nil, domain.WrapVendorErr("chemitoapi", "login", err)
	}
	a.key = key
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
// played in the browser by mpegts.js against our own /api/flv/{key} proxy
// (see flvhub.go) — no ffmpeg, no MediaMTX. Upstream logs in, resolves an
// available relay port (§4 of the doc's operation steps — "Get video port
// information" then "Get device list"), and requests the FLV URL fresh
// each time the hub opens a real connection, since the vendor's token is
// single-use. The browser never sees that URL or has to know about its
// lifetime: it holds one stable proxy URL, and the hub re-resolves and
// reconnects underneath it.
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
// Upstream must re-resolve per connection, not freeze one URL: confirmed
// live 2026-08-19 that Chemito's login token / live-video URL is single-use
// or short-lived, so a cached URL is already dead for the next connection
// attempt. The hub is the only thing that calls this — once when it first
// opens a channel, again on each reconnect — never once per viewer.
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

	// Always request audio=1 from the vendor, regardless of req.Audio:
	// Chemito's FLV header claims audio even when asked for audio=0 and
	// then sends no audio tags at all, which stalls a player that trusts
	// the header. That mismatch only exists in the audio=0 case, so
	// asking for audio unconditionally sidesteps it — no header to lie
	// about — at the cost of decoding an audio track callers may not want.
	channelKey := terid + "_" + strconv.Itoa(channel)
	upstream := func(ctx context.Context) (string, error) {
		// Gate before ever calling the vendor: acquireSlot renews an
		// already-held slot on every reconnect, and only rejects a channel
		// that would push the account past maxActiveChannels. See its doc
		// for why this stops short of the account's real 16-channel limit.
		if err := a.acquireSlot(channelKey); err != nil {
			return "", err
		}
		return a.resolveURL(terid, channel, true, st)
	}

	return domain.LiveSource{Kind: domain.KindFLV, Upstream: upstream, HasAudio: true}, nil
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
