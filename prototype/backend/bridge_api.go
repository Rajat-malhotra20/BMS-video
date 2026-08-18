package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"mediamtx-console/vendorclients/bridge"
	"mediamtx-console/vendorclients/castmaster"
	"mediamtx-console/vendorclients/n9m"
	"mediamtx-console/vendorclients/n9mserver"
)

// bridgeServer wires the vendor clients (Castmaster HTTP, N9M/"Chemito"
// device server) into a Supervisor that republishes each requested live
// feed into MediaMTX under this project's "{bus}_{cam}" path convention, so
// the existing fleet/api handlers pick it up with no changes. Requires the
// `ffmpeg` binary on PATH at runtime.
type bridgeServer struct {
	supervisor     *bridge.Supervisor
	rtspPublish    string // e.g. "rtsp://localhost:8554"
	restartBackoff time.Duration
	n9m            *n9mserver.Server // nil if the N9M/Chemito device listeners aren't running
}

func newBridgeServer(rtspPublish string, n9mSrv *n9mserver.Server) *bridgeServer {
	return &bridgeServer{
		supervisor:     bridge.NewSupervisor(),
		rtspPublish:    rtspPublish,
		restartBackoff: 5 * time.Second,
		n9m:            n9mSrv,
	}
}

func jobKey(bus string, cam int) string { return fmt.Sprintf("%s_%d", bus, cam) }

// checkNotRunning rejects the request with 409 if key is already active,
// so callers can bail out before doing any vendor-side work (login, etc).
func (b *bridgeServer) checkNotRunning(w http.ResponseWriter, key string) bool {
	if b.supervisor.Running(key) {
		http.Error(w, fmt.Sprintf("bridge job %q already running", key), http.StatusConflict)
		return false
	}
	return true
}

// startAndRespond starts the supervised job and writes the standard
// {key, path, rtspOut} response shared by every bridge-start handler.
func (b *bridgeServer) startAndRespond(w http.ResponseWriter, key, rtspOut string, run func(context.Context) error) {
	if err := b.supervisor.Start(key, run, b.restartBackoff); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, bridgeStartResponse{Key: key, Path: key, RTSPOut: rtspOut})
}

type castmasterStartRequest struct {
	BaseURL  string `json:"baseUrl"` // e.g. "https://cmmipl.org:22056"
	Username string `json:"username"`
	Password string `json:"password"`
	Terid    string `json:"terid"`   // vendor device serial number
	Channel  int    `json:"channel"` // 1-based camera channel
	Audio    bool   `json:"audio"`
	Main     bool   `json:"main"` // true: main stream, false: sub stream
	Bus      string `json:"bus"`  // this project's bus id (matches fleet path convention)
	Cam      int    `json:"cam"`  // this project's camera number for the same path convention
}

type bridgeStartResponse struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	RTSPOut string `json:"rtspOut"`
}

// handleCastmasterStart logs into a Castmaster server, resolves the FLV live
// URL for one device channel, and starts a supervised ffmpeg remux into
// MediaMTX at rtsp://.../{bus}_{cam}.
func (b *bridgeServer) handleCastmasterStart(w http.ResponseWriter, r *http.Request) {
	var req castmasterStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.BaseURL == "" || req.Terid == "" || req.Bus == "" || req.Cam == 0 {
		http.Error(w, "baseUrl, terid, bus and cam are required", http.StatusBadRequest)
		return
	}
	if req.Channel == 0 {
		req.Channel = 1
	}

	key := jobKey(req.Bus, req.Cam)
	if !b.checkNotRunning(w, key) {
		return
	}

	client := castmaster.NewClient(req.BaseURL, nil)
	if _, err := client.Login(req.Username, req.Password); err != nil {
		http.Error(w, "castmaster login failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	ports, err := client.LivePorts()
	if err != nil || len(ports) == 0 {
		http.Error(w, fmt.Sprintf("castmaster: no live ports available: %v", err), http.StatusBadGateway)
		return
	}

	st := castmaster.LiveStreamSub
	if req.Main {
		st = castmaster.LiveStreamMain
	}
	liveURL, err := client.LiveVideoURL(req.Terid, req.Channel, req.Audio, st, ports[0].Port, castmaster.DeviceN9M)
	if err != nil {
		http.Error(w, "castmaster: get live video url failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	rtspOut := fmt.Sprintf("%s/%s", b.rtspPublish, key)
	b.startAndRespond(w, key, rtspOut, bridge.RemuxToRTSP(liveURL, rtspOut))
}

type n9mStartRequest struct {
	DSNO      string `json:"dsno"`    // connected device's serial number
	Bus       string `json:"bus"`     // this project's bus id
	Cam       int    `json:"cam"`     // this project's camera number
	Channel   uint32 `json:"channel"` // 1-based device channel, converted to the CHANNEL bitmask
	Main      bool   `json:"main"`    // true: main stream, false: sub stream
	Audio     bool   `json:"audio"`
	Format    string `json:"format"` // elementary-stream framing on the wire: "h264" (default) or "hevc"
	IPAndPort string `json:"ipAndPort,omitempty"`
}

// handleN9mStart requests a live-video stream from a connected N9M device
// (see n9mserver.Server) and starts a supervised ffmpeg process that reads
// the raw media frames off its media channel and republishes them into
// MediaMTX at rtsp://.../{bus}_{cam}.
//
// Note: each Supervisor retry re-issues RequestLiveVideo (a fresh device
// media connection), since a device's media channel cannot be reused once
// its stream ends. RequestLiveVideo's own internal timeout means Stop may
// take up to that long to actually release an in-flight attempt.
func (b *bridgeServer) handleN9mStart(w http.ResponseWriter, r *http.Request) {
	if b.n9m == nil {
		http.Error(w, "n9m device server is not running", http.StatusServiceUnavailable)
		return
	}
	var req n9mStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.DSNO == "" || req.Bus == "" || req.Cam == 0 {
		http.Error(w, "dsno, bus and cam are required", http.StatusBadRequest)
		return
	}
	if req.Channel == 0 {
		req.Channel = 1
	}
	format := bridge.StdinFormat(req.Format)
	if format == "" {
		format = bridge.StdinFormatH264
	}

	if _, ok := b.n9m.Registry.Get(req.DSNO); !ok {
		http.Error(w, fmt.Sprintf("device %q is not connected", req.DSNO), http.StatusNotFound)
		return
	}

	key := jobKey(req.Bus, req.Cam)
	if !b.checkNotRunning(w, key) {
		return
	}

	streamName := key
	streamType := 0 // sub
	if req.Main {
		streamType = 1
	}
	channelBit := uint32(1) << (req.Channel - 1)

	rtspOut := fmt.Sprintf("%s/%s", b.rtspPublish, key)
	run := func(ctx context.Context) error {
		mediaConn, err := b.n9m.RequestLiveVideo(req.DSNO, streamName, n9m.RequestAliveVideoParams{
			StreamType: streamType,
			Channel:    channelBit,
			IPAndPort:  req.IPAndPort,
		}, 15*time.Second)
		if err != nil {
			return err
		}
		defer mediaConn.Close()
		reader := n9mserver.NewMediaFrameReader(mediaConn, n9m.PayloadLiveVideo)
		return bridge.StreamStdinToRTSP(reader, format, rtspOut)(ctx)
	}

	log.Printf("bridge: started n9m live video job %s (device=%s stream=%s)", key, req.DSNO, streamName)
	b.startAndRespond(w, key, rtspOut, run)
}

// handleN9mDevices lists currently-connected N9M devices.
func (b *bridgeServer) handleN9mDevices(w http.ResponseWriter, _ *http.Request) {
	if b.n9m == nil {
		writeJSON(w, []any{})
		return
	}
	sessions := b.n9m.Registry.List()
	out := make([]map[string]any, 0, len(sessions))
	for _, d := range sessions {
		out = append(out, map[string]any{
			"dsno":        d.DSNO,
			"devName":     d.Info.DevName,
			"carNum":      d.Info.CarNum,
			"connectedAt": d.ConnectedAt,
		})
	}
	writeJSON(w, out)
}
