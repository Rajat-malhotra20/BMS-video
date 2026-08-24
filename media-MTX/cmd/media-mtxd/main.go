// Command media-mtxd is the raw-packet side of the fleet system.
//
// It owns everything that deals in actual media bytes: the MediaMTX server
// it publishes into, the ffmpeg remux supervisor, the N9M device listeners
// (devices dial in to us over TCP and push frames), and the vendors whose
// only delivery mechanism is an RTSP-style remux (n9m, castmaster).
//
// The fleet API (prototype/backend) talks to this service over HTTP and
// holds no MediaMTX knowledge of its own: it asks GET /paths what is
// currently ingesting, and POST /start to bring a bus/cam up. That split is
// the point — vendors that hand back a directly playable URL (Chemito's
// HTTP-FLV, Sumith's HLS) never enter this process at all.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	castmasteradapter "media-mtx/adapters/castmaster"
	n9madapter "media-mtx/adapters/n9m"
	"media-mtx/bridge"
	"media-mtx/config"
	"media-mtx/domain"
	"media-mtx/n9mserver"
)

// restartBackoff is the default initial delay before a failed ingest job is
// retried. An adapter can override it via LiveSource.RestartBackoff when it
// knows its own vendor's session behavior differs.
const restartBackoff = 5 * time.Second

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

type server struct {
	adapters   map[string]domain.Adapter
	supervisor *bridge.Supervisor
	mtxAPIBase string // e.g. http://127.0.0.1:9997/v3
	rtspBase   string // e.g. rtsp://127.0.0.1:8554
	client     *http.Client
}

func main() {
	addr := env("MEDIA_MTX_ADDR", ":8090")
	vendorsPath := env("VENDORS_CONFIG", "config/vendors.json")

	accounts, err := config.LoadVendors(vendorsPath)
	if err != nil {
		log.Printf("media-mtxd: %v (castmaster will be unconfigured)", err)
		accounts = map[string]config.VendorAccount{}
	}

	// n9mSrv is the N9M device server: OBUs dial in to us on two ports and
	// push frames, so these listeners must be up before any request
	// arrives — unlike an HTTP vendor, there is nothing to "start".
	n9mSrv := n9mserver.NewServer(log.Default())
	serve("n9m signaling", env("N9M_SIGNAL_ADDR", ":9500"), n9mSrv.ServeSignaling)
	serve("n9m media", env("N9M_MEDIA_ADDR", ":9501"), n9mSrv.ServeMedia)

	cm := accounts["castmaster"]
	s := &server{
		adapters:   map[string]domain.Adapter{},
		supervisor: bridge.NewSupervisor(),
		mtxAPIBase: env("MEDIAMTX_API", "http://127.0.0.1:9997/v3"),
		rtspBase:   env("MEDIAMTX_RTSP", "rtsp://127.0.0.1:8554"),
		client:     &http.Client{Timeout: 5 * time.Second},
	}
	for _, a := range []domain.Adapter{
		castmasteradapter.New(castmasteradapter.Config{
			BaseURL: cm.BaseURL, Username: cm.Username, Password: cm.Password,
		}),
		n9madapter.New(n9mSrv),
	} {
		s.adapters[a.Name()] = a
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /paths", s.handlePaths)
	mux.HandleFunc("POST /start", s.handleStart)
	mux.HandleFunc("POST /stop", s.handleStop)
	mux.HandleFunc("GET /jobs", s.handleJobs)
	mux.HandleFunc("GET /cameras", s.handleCameras)

	log.Printf("media-mtxd listening on %s (mediamtx api %s, publish %s)",
		addr, s.mtxAPIBase, s.rtspBase)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// serve starts one device listener in the background. A port that will not
// bind is logged and skipped rather than fatal: N9M hardware is optional,
// and the rest of this service is still useful without it.
func serve(what, addr string, accept func(net.Listener) error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("media-mtxd: %s listener disabled: %v", what, err)
		return
	}
	log.Printf("media-mtxd: %s listening on %s", what, addr)
	go func() {
		if err := accept(ln); err != nil {
			log.Printf("media-mtxd: %s listener stopped: %v", what, err)
		}
	}()
}

// Path is one ingesting path, in the shape the fleet API consumes. This is
// deliberately MediaMTX's own field set rather than a reinvented one: this
// service is the only producer, and translating twice would just lose
// information the fleet view already knows how to read.
type Path struct {
	Name          string   `json:"name"`
	Ready         bool     `json:"ready"`
	Tracks        []string `json:"tracks"`
	BytesReceived uint64   `json:"bytesReceived"`
	Readers       []struct {
		Type string `json:"type"`
	} `json:"readers"`
}

type mtxPathList struct {
	PageCount int    `json:"pageCount"`
	Items     []Path `json:"items"`
}

// handlePaths reports every path currently ingesting into MediaMTX — the
// whole reason the fleet API no longer needs MediaMTX's address, its paging
// quirks, or a way to reach it at all.
func (s *server) handlePaths(w http.ResponseWriter, _ *http.Request) {
	paths, err := s.fetchAllPaths()
	if err != nil {
		log.Printf("media-mtxd: paths: %v", err)
		http.Error(w, "mediamtx unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, paths)
}

// fetchAllPaths pages through MediaMTX /paths/list.
func (s *server) fetchAllPaths() ([]Path, error) {
	all := []Path{}
	for page := 0; ; page++ {
		url := fmt.Sprintf("%s/paths/list?itemsPerPage=500&page=%d", s.mtxAPIBase, page)
		resp, err := s.client.Get(url)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("mediamtx returned HTTP %d for %s", resp.StatusCode, url)
		}
		var list mtxPathList
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, list.Items...)
		// Guard against pageCount == 0 to avoid integer underflow.
		if list.PageCount == 0 || page >= list.PageCount-1 {
			break
		}
	}
	return all, nil
}

type startRequest struct {
	Bus          string            `json:"bus"`
	Cam          int               `json:"cam"`
	Vendor       string            `json:"vendor"`
	Main         bool              `json:"main,omitempty"`
	Audio        bool              `json:"audio,omitempty"`
	VendorParams map[string]string `json:"vendorParams,omitempty"`
}

type startResponse struct {
	Key     string            `json:"key"`
	Kind    domain.SourceKind `json:"kind"`
	RTSPOut string            `json:"rtspOut"`
}

// handleStart resolves the vendor's upstream and starts the supervised
// remux job that publishes it into MediaMTX under {bus}_{cam}.
func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Bus == "" || req.Cam == 0 || req.Vendor == "" {
		http.Error(w, "bus, cam and vendor are required", http.StatusBadRequest)
		return
	}
	adapter, ok := s.adapters[req.Vendor]
	if !ok {
		http.Error(w, fmt.Sprintf("unknown vendor %q", req.Vendor), http.StatusNotFound)
		return
	}

	key := req.Bus + "_" + strconv.Itoa(req.Cam)
	if s.supervisor.Running(key) {
		http.Error(w, fmt.Sprintf("job %q already running", key), http.StatusConflict)
		return
	}
	rtspOut := s.rtspBase + "/" + key

	src, err := adapter.ResolveLiveSource(r.Context(), domain.StreamRequest{
		Bus: req.Bus, Cam: req.Cam, Vendor: req.Vendor,
		Main: req.Main, Audio: req.Audio,
		VendorParams: req.VendorParams, RTSPOut: rtspOut,
	})
	if err != nil {
		writeVendorError(w, err)
		return
	}

	run := src.Remux.Run
	if run == nil {
		run = bridge.RemuxToRTSP(src.Remux.URL, rtspOut)
	}
	backoff := restartBackoff
	if src.RestartBackoff > 0 {
		backoff = src.RestartBackoff
	}
	if err := s.supervisor.Start(key, run, backoff); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, startResponse{Key: key, Kind: domain.KindRTSP, RTSPOut: rtspOut})
}

func (s *server) handleStop(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key query parameter is required", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"key": key, "stopped": s.supervisor.Stop(key)})
}

func (s *server) handleJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.supervisor.List())
}

// handleCameras exposes this service's own vendors' device listings, so the
// fleet API can fold them into its roster without knowing these vendors
// exist. A vendor with no listing API contributes nothing rather than
// failing the whole sweep — castmaster has no such call.
func (s *server) handleCameras(w http.ResponseWriter, r *http.Request) {
	type camera struct {
		VendorID     string            `json:"vendorId"`
		Label        string            `json:"label,omitempty"`
		Vendor       string            `json:"vendor"`
		Online       bool              `json:"online"`
		Channels     int               `json:"channels,omitempty"`
		VendorParams map[string]string `json:"vendorParams,omitempty"`
	}
	want := r.URL.Query().Get("vendor")

	out := []camera{}
	for name, a := range s.adapters {
		if want != "" && want != name {
			continue
		}
		cams, err := a.ListCameras(r.Context(), nil)
		if err != nil {
			log.Printf("media-mtxd: cameras: %s: %v", name, err)
			continue
		}
		for _, c := range cams {
			out = append(out, camera{
				VendorID: c.VendorID, Label: c.Label, Vendor: name,
				Online: c.Online, Channels: c.Channels, VendorParams: c.VendorParams,
			})
		}
	}
	writeJSON(w, out)
}

// writeVendorError maps an adapter failure to a status code in one place
// rather than string-matching errors at each call site.
func writeVendorError(w http.ResponseWriter, err error) {
	var ve *domain.VendorError
	if errors.As(err, &ve) {
		switch {
		case ve.Code == "not_implemented":
			http.Error(w, err.Error(), http.StatusNotImplemented)
		case ve.Retryable:
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("media-mtxd: writeJSON: %v", err)
	}
}
