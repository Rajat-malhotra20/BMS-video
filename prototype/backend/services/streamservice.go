// Package services holds the vendor-agnostic orchestration that sits
// between the HTTP layer and the vendor adapters.
package services

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"mediamtx-console/domain"
	"mediamtx-console/vendors"
)

type StreamService struct {
	Registry *vendors.Registry

	// Ingest is the media-MTX service. Vendors whose only delivery
	// mechanism is a remux into a media server (n9m, castmaster) are not
	// registered here at all - they live entirely in that service, and a
	// request for one is forwarded to it. This process therefore owns no
	// ffmpeg process, no supervisor, and no RTSP publish address.
	Ingest *IngestClient

	// directActive tracks {bus}_{cam} keys started via a non-RTSP vendor
	// result (KindEmbed or KindHLS), along with the resolved URL so a later
	// GET /api/stream/{id} — from a client that never called StartStream
	// itself — can still hand back a playable link. These never touch
	// Supervisor — there's no ffmpeg process, so nothing for GET
	// /api/bridge to list — but the fleet view still needs to know they're
	// "online" until explicitly stopped, same as an RTSP job would be.
	directMu     sync.Mutex
	directActive map[string]DirectEntry

	// rosterCache holds the last VendorRoster() sweep. Vendor logins are
	// slow (and some vendors rate-limit), so this is cached independently
	// of the 2s MediaMTX fleet cache — refreshed at most every
	// rosterCacheTTL regardless of how often GET /api/fleet is polled.
	rosterMu       sync.Mutex
	rosterCache    []RosterEntry
	rosterCachedAt time.Time

	// channelCountsCache holds the last ChannelCounts() sweep — a much
	// slower-refreshing view than rosterCache, since a device's channel
	// count rarely changes and there's no reason to recompute it as often
	// as the roster's other (session-adjacent) signals.
	channelCountsMu       sync.Mutex
	channelCountsCache    map[string]int
	channelCountsCachedAt time.Time

	// ingestVendors remembers which vendor names the media-MTX service
	// serves, learned from its /cameras roster, so StartAny can route a
	// request without this process hardcoding the split.
	ingestVendorMu sync.Mutex
	ingestVendors  map[string]bool
}

const rosterCacheTTL = 30 * time.Second
const channelCountsCacheTTL = 30 * time.Minute

// RosterEntry is one camera a vendor account reports knowing about,
// whether or not anyone has ever called StartStream for it.
type RosterEntry struct {
	Key    string // {bus}_{cam}, bus = Camera.VendorID, cam always 1 (this listing has no per-camera channel breakdown)
	Vendor string
	Label  string // human-readable name (plate, car licence, ...) — empty if the vendor gave none
	Online bool
	// Channels is Camera.Channels passed through — how many cam numbers
	// this bus has, per the vendor's own listing. 0 when the vendor
	// doesn't report one.
	Channels int
	// VendorParams is ready to hand straight to StartStream for this bus —
	// lets a caller auto-start it without a config/buses.json entry.
	VendorParams map[string]string
}

// VendorRoster sweeps every registered vendor's ListCameras and returns
// what each account reports knowing about — e.g. Sumith's full vehicle
// list, not just ones someone has explicitly bridged. Best-effort: a
// vendor that errors (or hasn't implemented listing) just contributes no
// entries, so one vendor's hiccup doesn't blank the whole fleet.
func (s *StreamService) VendorRoster(ctx context.Context) []RosterEntry {
	s.rosterMu.Lock()
	if s.rosterCache != nil && time.Since(s.rosterCachedAt) < rosterCacheTTL {
		cached := s.rosterCache
		s.rosterMu.Unlock()
		return cached
	}
	s.rosterMu.Unlock()

	var entries []RosterEntry
	for _, adapter := range s.Registry.All() {
		cams, err := adapter.ListCameras(ctx, nil)
		if err != nil {
			log.Printf("vendor roster: %s: ListCameras: %v", adapter.Name(), err)
			continue
		}
		for _, c := range cams {
			label := c.Label
			if label == c.VendorID {
				label = "" // no extra info beyond the id itself — nothing worth surfacing separately
			}
			entries = append(entries, RosterEntry{
				Key:          c.VendorID + "_1",
				Vendor:       adapter.Name(),
				Label:        label,
				Online:       c.Online,
				Channels:     c.Channels,
				VendorParams: c.VendorParams,
			})
		}
	}

	// The media-MTX service vendors (n9m, castmaster) are not registered
	// here, so ask it what its own accounts know about. Best-effort for the
	// same reason as above: if that service is down, the vendors this
	// process serves directly must still show up.
	if s.Ingest != nil {
		cams, err := s.Ingest.Cameras(ctx)
		if err != nil {
			log.Printf("vendor roster: ingest service: %v", err)
		}
		for _, c := range cams {
			s.noteIngestVendor(c.Vendor)
			label := c.Label
			if label == c.VendorID {
				label = ""
			}
			entries = append(entries, RosterEntry{
				Key:          c.VendorID + "_1",
				Vendor:       c.Vendor,
				Label:        label,
				Online:       c.Online,
				Channels:     c.Channels,
				VendorParams: c.VendorParams,
			})
		}
	}

	s.rosterMu.Lock()
	s.rosterCache = entries
	s.rosterCachedAt = time.Now()
	s.rosterMu.Unlock()
	return entries
}

// ChannelCounts returns, per bus id, how many camera slots a vendor reports
// that bus having — independent of whether any are currently streaming.
// Purely informational (e.g. so a dashboard can show "this bus has N
// cameras" before anyone's opened one): it only reuses VendorRoster's
// device-listing data, never opens a live session, so it can't interfere
// with an active stream or a device's concurrent-session capacity. Cached
// far longer than the roster itself (channelCountsCacheTTL) since a
// device's channel count rarely changes.
func (s *StreamService) ChannelCounts(ctx context.Context) map[string]int {
	s.channelCountsMu.Lock()
	if s.channelCountsCache != nil && time.Since(s.channelCountsCachedAt) < channelCountsCacheTTL {
		cached := s.channelCountsCache
		s.channelCountsMu.Unlock()
		return cached
	}
	s.channelCountsMu.Unlock()

	counts := make(map[string]int)
	for _, entry := range s.VendorRoster(ctx) {
		if entry.Channels > 0 {
			counts[strings.TrimSuffix(entry.Key, "_1")] = entry.Channels
		}
	}

	s.channelCountsMu.Lock()
	s.channelCountsCache = counts
	s.channelCountsCachedAt = time.Now()
	s.channelCountsMu.Unlock()
	return counts
}

// RosterVendor looks up which vendor + VendorParams a bus id resolves to,
// straight from the vendor roster (Chemito's device list, Sumith's vehicle
// list) — the same auto-discovery GET /api/fleet already shows bus listings
// from. Lets a bus with no config/buses.json entry still be started
// on-demand via GET /api/stream/{id}?cam=N, not just displayed.
func (s *StreamService) RosterVendor(ctx context.Context, bus string) (vendor string, vendorParams map[string]string, ok bool) {
	for _, entry := range s.VendorRoster(ctx) {
		if strings.TrimSuffix(entry.Key, "_1") == bus {
			return entry.Vendor, entry.VendorParams, true
		}
	}
	return "", nil, false
}

// StartStream resolves req.Vendor's adapter, builds the remux/embed
// result, and — for RTSP results — starts the supervised job. Never
// branches on vendor name; only on the domain.SourceKind an adapter hands
// back.
func (s *StreamService) StartStream(ctx context.Context, req domain.StreamRequest) (domain.StreamResult, error) {
	adapter, err := s.Registry.Get(req.Vendor)
	if err != nil {
		return domain.StreamResult{}, err
	}

	key := req.Bus + "_" + strconv.Itoa(req.Cam)

	src, err := adapter.ResolveLiveSource(ctx, req)
	if err != nil {
		return domain.StreamResult{}, err
	}

	switch src.Kind {
	case domain.KindFLV:
		// No ffmpeg, no MediaMTX: the browser plays the vendor's FLV
		// through our proxy, which calls src.Upstream fresh per connect.
		// Cached as active for the same reason KindHLS is — later
		// GET /api/stream/{id}?cam=N calls resolve from here instead of
		// re-running the vendor's start-streaming side effect, and it's
		// what /api/flv/{key} looks the Upstream func up in.
		flvURL := "/api/flv/" + key
		s.directMu.Lock()
		if s.directActive == nil {
			s.directActive = make(map[string]DirectEntry)
		}
		s.directActive[key] = DirectEntry{Key: key, Kind: src.Kind, HLSURL: flvURL, Upstream: src.Upstream, HasAudio: src.HasAudio}
		s.directMu.Unlock()
		return domain.StreamResult{Key: key, Kind: src.Kind, HLSURL: flvURL}, nil

	case domain.KindHLS:
		// Only a confirmed real stream is worth caching as "active" — an
		// HLS probe that just succeeded is a strong signal. Caching this
		// means later GET /api/stream/{id}?cam=N calls skip StartStream
		// entirely (see IsActive) instead of re-triggering the vendor.
		s.directMu.Lock()
		if s.directActive == nil {
			s.directActive = make(map[string]DirectEntry)
		}
		s.directActive[key] = DirectEntry{Key: key, Kind: src.Kind, HLSURL: src.HLSURL}
		s.directMu.Unlock()
		return domain.StreamResult{Key: key, Kind: src.Kind, HLSURL: src.HLSURL}, nil

	case domain.KindEmbed:
		// Deliberately NOT cached as active: for Sumith this is what an
		// adapter falls back to when its HLS probe fails, and camera
		// availability flips within seconds — caching a stale "embed"
		// result would permanently mask a channel that's actually live
		// again by the next request. Leaving it uncached means the next
		// GET /api/stream/{id}?cam=N re-resolves fresh, giving HLS
		// another real chance instead of getting stuck on one bad probe.
		return domain.StreamResult{Key: key, Kind: src.Kind, EmbedURL: src.EmbedURL}, nil

	case domain.KindRTSP:
		// No adapter registered here returns this any more - the vendors
		// that need a remux moved to the media-MTX service, reached via
		// StartIngest rather than resolved here. Kept as an explicit error
		// so a future locally-registered RTSP adapter fails loudly instead
		// of silently publishing nowhere.
		return domain.StreamResult{}, fmt.Errorf(
			"adapter %q returned kind %q: RTSP vendors belong in the media-MTX service, not this API",
			req.Vendor, src.Kind)

	default:
		return domain.StreamResult{}, fmt.Errorf("adapter %q returned unknown source kind %q", req.Vendor, src.Kind)
	}
}

// ErrAlreadyRunning is returned when a channel is already live.
type ErrAlreadyRunning struct{ Key string }

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("stream %q already running", e.Key)
}

// StartIngest forwards a bus/cam to the media-MTX service, for vendors that
// only deliver via a remux into a media server. The result is reported as
// KindRTSP with the media-server path that service published to - identical
// to what this API returned when it ran the remux itself, so the frontend
// contract is unchanged.
func (s *StreamService) StartIngest(ctx context.Context, req domain.StreamRequest) (domain.StreamResult, error) {
	if s.Ingest == nil {
		return domain.StreamResult{}, fmt.Errorf("no ingest service configured for vendor %q", req.Vendor)
	}
	key := req.Bus + "_" + strconv.Itoa(req.Cam)
	res, err := s.Ingest.Start(ctx, req.Bus, req.Cam, req.Vendor, req.Main, req.Audio, req.VendorParams)
	if err != nil {
		return domain.StreamResult{}, err
	}
	return domain.StreamResult{Key: key, Kind: domain.KindRTSP, RTSPOut: res.RTSPOut}, nil
}

// isIngestVendor / noteIngestVendor track which vendor names the media-MTX
// service serves, learned from its own roster rather than hardcoded here.
func (s *StreamService) isIngestVendor(vendor string) bool {
	s.ingestVendorMu.Lock()
	defer s.ingestVendorMu.Unlock()
	return s.ingestVendors[vendor]
}

func (s *StreamService) noteIngestVendor(vendor string) {
	s.ingestVendorMu.Lock()
	if s.ingestVendors == nil {
		s.ingestVendors = make(map[string]bool)
	}
	s.ingestVendors[vendor] = true
	s.ingestVendorMu.Unlock()
}

// StartAny routes to whichever side owns req.Vendor: a locally registered
// adapter (Chemito FLV, Sumith HLS/embed) or the media-MTX service. Callers
// need not know which, only that a vendor name resolves somewhere.
func (s *StreamService) StartAny(ctx context.Context, req domain.StreamRequest) (domain.StreamResult, error) {
	key := req.Bus + "_" + strconv.Itoa(req.Cam)
	if s.IsActive(key) {
		return domain.StreamResult{}, &ErrAlreadyRunning{Key: key}
	}
	if _, err := s.Registry.Get(req.Vendor); err != nil && s.isIngestVendor(req.Vendor) {
		return s.StartIngest(ctx, req)
	}
	return s.StartStream(ctx, req)
}

// IsActive reports whether key is already live — either a supervised RTSP
// job or a tracked direct (embed/HLS) session. Callers that might trigger
// StartStream on demand (e.g. a lazy GET /api/stream/{id}) must check this
// first: calling StartStream again for an already-active direct session
// would silently re-run the vendor's real "start streaming" side effect
// (login, getLiveStreamingLink, ...) on every poll — Supervisor.Running
// alone only catches the RTSP case.
func (s *StreamService) IsActive(key string) bool {
	s.directMu.Lock()
	defer s.directMu.Unlock()
	_, ok := s.directActive[key]
	return ok
}

// StopStream drops a tracked direct session and asks the ingest service to
// stop any remux job under the same key. Reports whether either had one.
func (s *StreamService) StopStream(ctx context.Context, key string) bool {
	s.directMu.Lock()
	_, wasDirect := s.directActive[key]
	delete(s.directActive, key)
	s.directMu.Unlock()

	stopped := false
	if s.Ingest != nil {
		stopped = s.Ingest.Stop(ctx, key)
	}
	return stopped || wasDirect
}

// ListActive reports the ingest service remux jobs. Direct (FLV/HLS)
// sessions are not jobs - nothing supervises them, since no process sits
// between the vendor and the browser - so they surface via
// ActiveDirectKeys and the fleet view instead.
func (s *StreamService) ListActive(ctx context.Context) []IngestJob {
	if s.Ingest == nil {
		return []IngestJob{}
	}
	jobs, err := s.Ingest.Jobs(ctx)
	if err != nil {
		log.Printf("list active: ingest jobs: %v", err)
		return []IngestJob{}
	}
	return jobs
}

// DirectEntry is one {bus}_{cam} key currently live via a non-RTSP vendor
// result: which kind it is and its resolved URL, so a caller (e.g.
// GET /api/stream/{id}, from a client that never called StartStream
// itself) can render it without guessing between "embed" and "hls".
type DirectEntry struct {
	Key      string
	Kind     domain.SourceKind
	EmbedURL string
	HLSURL   string
	// Upstream is set only for KindFLV: what /api/flv/{key} calls to get a
	// fresh vendor stream URL for each viewer that connects. Never
	// serialized — the browser only ever sees HLSURL (our proxy path), so
	// the vendor's single-use token stays server-side.
	Upstream func(ctx context.Context) (string, error)
	// HasAudio mirrors domain.LiveSource.HasAudio — whether audio was
	// actually requested, so the proxy knows if the vendor's FLV header is
	// lying about having an audio track.
	HasAudio bool
}

// ActiveDirectKeys returns the keys currently live via a non-RTSP vendor
// result (embed or direct HLS), for GET /api/fleet to merge alongside
// MediaMTX's own paths using the same {bus}_{cam} convention.
func (s *StreamService) ActiveDirectKeys() []DirectEntry {
	s.directMu.Lock()
	defer s.directMu.Unlock()
	entries := make([]DirectEntry, 0, len(s.directActive))
	for _, e := range s.directActive {
		entries = append(entries, e)
	}
	return entries
}

// UpstreamFor resolves one FLV channel at the exact quality req asks for,
// WITHOUT touching the active-session registry.
//
// The registry holds one entry per camera, and that entry's resolver has a
// quality baked into it (whatever the first caller asked for). That is fine
// as the camera's default, but a grid watching the device's sub-stream while
// someone else watches the main stream needs two resolvers for one camera —
// so this hands back a resolver directly and leaves the registry alone. The
// caller (handleFLVProxy) still ensures the registry entry exists, because
// that is what makes the camera visible in GET /api/fleet.
//
// Returns a VendorError for anything but an FLV source: no other kind has a
// per-connect resolver to hand out.
func (s *StreamService) UpstreamFor(ctx context.Context, req domain.StreamRequest) (domain.LiveSource, error) {
	adapter, err := s.Registry.Get(req.Vendor)
	if err != nil {
		return domain.LiveSource{}, err
	}
	src, err := adapter.ResolveLiveSource(ctx, req)
	if err != nil {
		return domain.LiveSource{}, err
	}
	if src.Kind != domain.KindFLV || src.Upstream == nil {
		return domain.LiveSource{}, &domain.VendorError{
			Vendor: req.Vendor, Op: "resolve live source", Code: "not_flv",
		}
	}
	return src, nil
}

// DirectEntryFor returns the tracked direct session under key, if any —
// how the /api/flv/{key} proxy reaches that session's Upstream resolver
// without re-running StartStream (which would re-trigger the vendor's
// start-streaming call on every viewer connect, not just the first).
func (s *StreamService) DirectEntryFor(key string) (DirectEntry, bool) {
	s.directMu.Lock()
	defer s.directMu.Unlock()
	e, ok := s.directActive[key]
	return e, ok
}

func (s *StreamService) ListCameras(ctx context.Context, vendor string, vendorParams map[string]string) ([]domain.Camera, error) {
	adapter, err := s.Registry.Get(vendor)
	if err != nil {
		return nil, err
	}
	return adapter.ListCameras(ctx, vendorParams)
}
