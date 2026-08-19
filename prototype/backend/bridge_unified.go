package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/domain"
	"mediamtx-console/services"
	"mediamtx-console/vendorclients/bridge"
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
}

func newUnifiedBridgeServer(stream *services.StreamService, buses map[string]vendorconfig.Bus) *unifiedBridgeServer {
	return &unifiedBridgeServer{stream: stream, buses: buses}
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
// the bus's vendor+params from config and start it. ok=false (nil error)
// means the bus simply isn't configured for bridging — not a failure, just
// nothing to do.
func (u *unifiedBridgeServer) startBus(ctx context.Context, bus string, cam int, main, audio bool) (result domain.StreamResult, ok bool, err error) {
	busCfg, configured := u.buses[bus]
	if !configured {
		return domain.StreamResult{}, false, nil
	}
	result, err = u.stream.StartStream(ctx, domain.StreamRequest{
		Bus:          bus,
		Cam:          cam,
		Vendor:       busCfg.Vendor,
		Main:         main,
		Audio:        audio,
		VendorParams: busCfg.VendorParams,
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
		// Someone's actively asking for this bus/cam again — if it's stuck
		// sleeping between failed attempts (or in its give-up cooldown),
		// give it a fresh try now instead of leaving it to wait out
		// whatever's left. No-op if it's already streaming fine.
		u.stream.Nudge(key)
		return nil, nil // let the normal cache path report it, don't re-trigger the vendor's start call
	}
	result, configured, err := u.startBus(ctx, bus, cam, true, false)
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
	stopped := u.stream.StopStream(key)
	writeJSON(w, map[string]any{"key": key, "stopped": stopped})
}

func (u *unifiedBridgeServer) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, u.stream.ListActive())
}

// writeBridgeError maps a StreamService error to an HTTP status without
// the caller having to know which vendor (or none) produced it.
func writeBridgeError(w http.ResponseWriter, err error) {
	var alreadyRunning *bridge.ErrAlreadyRunning
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
