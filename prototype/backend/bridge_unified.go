package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/domain"
	"mediamtx-console/services"
)

// unifiedBridgeServer exposes the vendor-less bridge API: the frontend
// says "start bus X cam Y" and never names a vendor, credential, or
// device id — those come from config.Bus, looked up by bus id. This sits
// alongside the existing per-vendor /api/bridge/{vendor}/... endpoints
// (bridge_api.go, bridge_sumithlive.go), which still work unchanged for
// direct vendor testing/debugging.
type unifiedBridgeServer struct {
	stream *services.StreamService
	buses  map[string]vendorconfig.Bus

	// hub holds one upstream vendor connection per {bus}_{cam}, shared by
	// every viewer of that camera. See flvhub.go.
	hub *flvHub
}

func newUnifiedBridgeServer(stream *services.StreamService, buses map[string]vendorconfig.Bus) *unifiedBridgeServer {
	return &unifiedBridgeServer{stream: stream, buses: buses, hub: newFLVHub()}
}

type bridgeStartRequest struct {
	Bus   string `json:"bus"`
	Cam   int    `json:"cam"`
	Main  bool   `json:"main,omitempty"`
	Audio bool   `json:"audio,omitempty"`
}

func (u *unifiedBridgeServer) handleStart(w http.ResponseWriter, r *http.Request) {
	var req bridgeStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Bus == "" || req.Cam == 0 {
		http.Error(w, "bus and cam are required", http.StatusBadRequest)
		return
	}

	result, configured, err := u.startBus(r.Context(), req.Bus, req.Cam, req.Main, req.Audio)
	if !configured {
		http.Error(w, fmt.Sprintf("bus %q is not configured for bridging", req.Bus), http.StatusNotFound)
		return
	}
	if err != nil {
		writeBridgeError(w, err)
		return
	}
	writeJSON(w, result)
}

// startBus is the shared core behind handleStart and ensureStream: look up
// the bus's vendor+params — first config/buses.json (a manual pin, or the
// only option for a vendor with no listing API, like N9M/Castmaster), then
// falling back to the vendor roster (Chemito's device list, Sumith's
// vehicle list) so a bus that's only auto-discovered can still be started
// on-demand, not just displayed. ok=false (nil error) means the bus isn't
// findable either way — not a failure, just nothing to do.
func (u *unifiedBridgeServer) startBus(ctx context.Context, bus string, cam int, main, audio bool) (result domain.StreamResult, ok bool, err error) {
	vendor, vendorParams := "", map[string]string(nil)
	if busCfg, configured := u.buses[bus]; configured {
		vendor, vendorParams = busCfg.Vendor, busCfg.VendorParams
	} else if v, params, found := u.stream.RosterVendor(ctx, bus); found {
		vendor, vendorParams = v, params
	} else {
		return domain.StreamResult{}, false, nil
	}
	result, err = u.stream.StartStream(ctx, domain.StreamRequest{
		Bus:          bus,
		Cam:          cam,
		Vendor:       vendor,
		Main:         main,
		Audio:        audio,
		VendorParams: vendorParams,
	})
	return result, true, err
}

// ensureStream is what GET /api/stream/{id}?cam=N calls when that bus+cam
// isn't already showing up in the fleet snapshot: it starts the bridge on
// demand, transparently, so the frontend never has to call
// POST /api/bridge/start itself — that endpoint is internal-only now, kept
// for admin/debug and as the thing this function calls under the hood.
// Returns (nil, nil) when the bus isn't configured for bridging at all,
// so the caller falls through to its normal "not found" handling.
func (u *unifiedBridgeServer) ensureStream(ctx context.Context, bus string, cam int) (*domain.StreamResult, error) {
	key := bus + "_" + strconv.Itoa(cam)
	if u.stream.IsActive(key) {
		// Already live: let the normal cache path report it rather than
		// re-triggering the vendor's start call. The old "nudge a job stuck
		// in backoff" step is gone with the local supervisor — retry
		// backoff now lives in the media-MTX service, which nudges itself
		// when a start arrives for a job it is already sleeping on.
		return nil, nil
	}
	// audio=true: cameras with a microphone carry AAC (confirmed live
	// 2026-08-27, ~10% more bandwidth), and the hub drops the header's
	// audio bit for cameras that turn out not to have one. Players mute
	// their own tiles; the stream carries the track either way.
	result, configured, err := u.startBus(ctx, bus, cam, true, true)
	if !configured {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (u *unifiedBridgeServer) handleStop(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key query parameter is required", http.StatusBadRequest)
		return
	}
	stopped := u.stream.StopStream(r.Context(), key)
	writeJSON(w, map[string]any{"key": key, "stopped": stopped})
}

// handleHub reports what every FLV channel is doing right now.
func (u *unifiedBridgeServer) handleHub(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, u.hub.stats())
}

func (u *unifiedBridgeServer) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, u.stream.ListActive(r.Context()))
}

// writeBridgeError maps a StreamService error to an HTTP status without
// the caller having to know which vendor (or none) produced it.
func writeBridgeError(w http.ResponseWriter, err error) {
	var alreadyRunning *services.ErrAlreadyRunning
	if errors.As(err, &alreadyRunning) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	var vendorErr *domain.VendorError
	if errors.As(err, &vendorErr) {
		switch {
		case vendorErr.Code == "not_implemented":
			http.Error(w, err.Error(), http.StatusNotImplemented)
		case vendorErr.Code == "device_offline":
			http.Error(w, err.Error(), http.StatusNotFound)
		default:
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}

	http.Error(w, err.Error(), http.StatusBadGateway)
}

// flvStallTimeout is how long the proxy waits for any byte from the vendor
// before giving up on a connection. Mirrors what the old ffmpeg path needed
// -rw_timeout for: a Chemito TCP connection that stops delivering frames
// but never closes leaves the reader blocked forever, so the viewer sees a
// frozen picture and no error. Generous against a live feed's real frame
// gaps (~30KB/s continuous, confirmed live 2026-08-20).
const flvStallTimeout = 15 * time.Second

// flvClient has no overall timeout on purpose — an http.Client.Timeout
// covers the whole request including reading the body, which for a live
// stream means cutting off a perfectly healthy feed at the deadline.
// stallReader below is what bounds a dead connection instead.
var flvClient = &http.Client{}

// handleFLVProxy streams one Chemito channel's HTTP-FLV through to the
// browser, where mpegts.js plays it directly. This exists for two reasons
// the browser can't work around itself: the vendor's relay port sends no
// CORS headers, so a direct fetch from the page is blocked; and its stream
// URL carries a single-use/short-lived token that must be re-resolved per
// viewer and must not be handed out to clients.
//
// Deliberately not a byte cache or a fan-out tee: each viewer opens its own
// vendor session. See the ponytail note below.
//
// ponytail: one upstream connection per viewer. Chemito has a small real
// concurrent-session capacity and no stop/close call, so N people watching
// the same bus/cam is N sessions on that one device. If session-exhaustion
// errors show up, tee a single upstream per {bus}_{cam} to all attached
// writers here — that keeps the no-ffmpeg property and touches only this
// function.
func (u *unifiedBridgeServer) handleFLVProxy(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	bus, cam, ok := parseBusPath(key)
	if !ok {
		http.Error(w, "invalid stream key "+key, http.StatusBadRequest)
		return
	}

	entry, ok := u.stream.DirectEntryFor(key)
	if !ok {
		// Nobody has started this channel yet (fresh process, or the
		// browser hit the proxy URL straight from a bookmark) — start it
		// the same way GET /api/stream/{id}?cam=N would, then retry.
		if _, configured, err := u.startBus(r.Context(), bus, cam, true, true); err != nil {
			writeBridgeError(w, err)
			return
		} else if !configured {
			http.Error(w, fmt.Sprintf("bus %q is not configured for bridging", bus), http.StatusNotFound)
			return
		}
		if entry, ok = u.stream.DirectEntryFor(key); !ok {
			http.Error(w, "stream "+key+" is not an FLV source", http.StatusConflict)
			return
		}
	}
	if entry.Upstream == nil {
		http.Error(w, "stream "+key+" is not an FLV source", http.StatusConflict)
		return
	}

	// Everything past here — connecting, reconnecting, holding the viewer
	// open across a vendor outage — belongs to the hub, which shares one
	// device session across every viewer of this camera.
	u.hub.serve(w, r, key, entry.Upstream, entry.HasAudio)
}

// stallReader fails a Read that produces nothing for timeout, so a vendor
// connection that goes quiet without closing doesn't hang the viewer
// forever. It closes the underlying body to unblock the in-flight Read.
type stallReader struct {
	r       io.Reader
	timeout time.Duration
	closer  io.Closer
}

func (s *stallReader) Read(p []byte) (int, error) {
	timer := time.AfterFunc(s.timeout, func() { s.closer.Close() })
	defer timer.Stop()
	return s.r.Read(p)
}

// flushWriter flushes after every chunk so live frames aren't buffered.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}
