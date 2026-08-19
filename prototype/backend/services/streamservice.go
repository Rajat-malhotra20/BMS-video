// Package services holds the vendor-agnostic orchestration that sits
// between the HTTP layer and the vendor adapters.
package services

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"mediamtx-console/domain"
	"mediamtx-console/vendorclients/bridge"
	"mediamtx-console/vendors"
)

type StreamService struct {
	Registry       *vendors.Registry
	Supervisor     *bridge.Supervisor
	RTSPPublish    string // e.g. "rtsp://localhost:8554"
	RestartBackoff time.Duration

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
}

const rosterCacheTTL = 30 * time.Second

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

	s.rosterMu.Lock()
	s.rosterCache = entries
	s.rosterCachedAt = time.Now()
	s.rosterMu.Unlock()
	return entries
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
	if s.Supervisor.Running(key) {
		return domain.StreamResult{}, &bridge.ErrAlreadyRunning{Key: key}
	}
	req.RTSPOut = s.RTSPPublish + "/" + key

	src, err := adapter.ResolveLiveSource(ctx, req)
	if err != nil {
		return domain.StreamResult{}, err
	}

	switch src.Kind {
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
		run := src.Remux.Run
		if run == nil {
			run = bridge.RemuxToRTSP(src.Remux.URL, req.RTSPOut)
		}
		if err := s.Supervisor.Start(key, run, s.RestartBackoff); err != nil {
			return domain.StreamResult{}, err
		}
		return domain.StreamResult{Key: key, Kind: domain.KindRTSP, RTSPOut: req.RTSPOut}, nil

	default:
		return domain.StreamResult{}, fmt.Errorf("adapter %q returned unknown source kind %q", req.Vendor, src.Kind)
	}
}

// IsActive reports whether key is already live — either a supervised RTSP
// job or a tracked direct (embed/HLS) session. Callers that might trigger
// StartStream on demand (e.g. a lazy GET /api/stream/{id}) must check this
// first: calling StartStream again for an already-active direct session
// would silently re-run the vendor's real "start streaming" side effect
// (login, getLiveStreamingLink, ...) on every poll — Supervisor.Running
// alone only catches the RTSP case.
func (s *StreamService) IsActive(key string) bool {
	if s.Supervisor.Running(key) {
		return true
	}
	s.directMu.Lock()
	defer s.directMu.Unlock()
	_, ok := s.directActive[key]
	return ok
}

func (s *StreamService) StopStream(key string) bool {
	s.directMu.Lock()
	_, wasDirect := s.directActive[key]
	delete(s.directActive, key)
	s.directMu.Unlock()

	stopped := s.Supervisor.Stop(key)
	return stopped || wasDirect
}

func (s *StreamService) ListActive() []bridge.Status {
	return s.Supervisor.List()
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

func (s *StreamService) ListCameras(ctx context.Context, vendor string, vendorParams map[string]string) ([]domain.Camera, error) {
	adapter, err := s.Registry.Get(vendor)
	if err != nil {
		return nil, err
	}
	return adapter.ListCameras(ctx, vendorParams)
}
