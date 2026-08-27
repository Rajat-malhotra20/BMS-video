package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/domain"
	"mediamtx-console/services"
	"mediamtx-console/vendors"
	chemitoapiadapter "mediamtx-console/vendors/chemitoapi"
	sumithliveadapter "mediamtx-console/vendors/sumithlive"
)

type config struct {
	addr string

	// mediaMTX* are reverse-proxy targets only: dumb byte pipes so a
	// browser can play an RTSP-ingested channel through this origin. This
	// process never calls the MediaMTX API - it asks the media-MTX service
	// (ingestURL) what is ingesting, and that service owns MediaMTX, the
	// ffmpeg supervisor, and the N9M device listeners.
	mediaMTXHLS      string
	mediaMTXWHEP     string
	mediaMTXPlayback string

	ingestURL     string
	corsOrigin    string
	apiToken      string
	vendorsConfig string
	busesConfig   string
}

func main() {
	cfg := config{
		addr:             env("ADDR", ":8080"),
		mediaMTXHLS:      env("MEDIAMTX_HLS_URL", "http://localhost:8888"),
		mediaMTXWHEP:     env("MEDIAMTX_WEBRTC_URL", "http://localhost:8889"),
		mediaMTXPlayback: env("MEDIAMTX_PLAYBACK_URL", "http://localhost:9996"),
		// INGEST_URL="" turns the raw-packet side off entirely: no MediaMTX,
		// no media-mtxd, no ffmpeg. Chemito (HTTP-FLV) and Sumith
		// (HLS/embed) need none of it, so that is a valid way to run this.
		// envAllowEmpty, not env: an explicitly empty INGEST_URL must mean
		// "there is no ingest service", not "unset, use the default".
		ingestURL:     envAllowEmpty("INGEST_URL", "http://localhost:8090"),
		corsOrigin:    env("CORS_ALLOWED_ORIGIN", "*"),
		apiToken:      env("API_TOKEN", ""),
		vendorsConfig: env("VENDORS_CONFIG", "config/vendors.json"),
		busesConfig:   env("BUSES_CONFIG", "config/buses.json"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ingestEnabled gates every route that could only ever answer "empty"
	// without the raw-packet side. With INGEST_URL unset there is no
	// MediaMTX, so /live /whep /playback have nothing behind them, no
	// channel is ever RTSP-ingested, and no recording exists. Routing them
	// anyway would answer 200-with-nothing, which reads as "working, no
	// data" when the truth is "not part of this deployment" — so they are
	// not registered and answer 404.
	ingestEnabled := cfg.ingestURL != ""

	api := newAPIServer(cfg.ingestURL)
	mux.HandleFunc("GET /api/fleet", api.handleFleet)
	mux.HandleFunc("GET /api/fleet/stream", api.handleFleetStream)
	mux.HandleFunc("GET /api/bus/{id}", api.handleBusDetail)
	mux.HandleFunc("GET /api/stream/{id}", api.handleStreamLive)
	if ingestEnabled {
		mux.Handle("/live/", reverseProxy(cfg.mediaMTXHLS, "/live", noCache))
		mux.Handle("/whep/", reverseProxy(cfg.mediaMTXWHEP, "/whep", nil))
		mux.Handle("/playback/", reverseProxy(cfg.mediaMTXPlayback, "/playback", noCache))
		mux.HandleFunc("GET /api/stream/{id}/recording", api.handleStreamRecording)
	}

	// Vendor-less bridge API: the frontend just says "start bus X cam Y"
	// (see config.Bus) and never names a vendor. Missing config files mean
	// no buses are bridgeable this way yet — everything else keeps working.
	vendorAccounts, err := vendorconfig.LoadVendors(cfg.vendorsConfig)
	if err != nil {
		log.Printf("bridge: %v (unified /api/bridge/start has no buses configured)", err)
		vendorAccounts = map[string]vendorconfig.VendorAccount{}
	}
	buses, err := vendorconfig.LoadBuses(cfg.busesConfig)
	if err != nil {
		log.Printf("bridge: %v (unified /api/bridge/start has no buses configured)", err)
		buses = map[string]vendorconfig.Bus{}
	}
	sumithliveAcct := vendorAccounts["sumithlive"]
	chemitoapiAcct := vendorAccounts["chemitoapi"]
	// Only vendors that hand back a directly playable URL are registered
	// here. n9m and castmaster need a remux into a media server, so they
	// live in the media-MTX service and are reached over HTTP - see
	// services.IngestClient.
	registry := vendors.NewRegistry(
		sumithliveadapter.New(sumithliveadapter.Config{
			BaseURL:          sumithliveAcct.BaseURL,
			Username:         sumithliveAcct.Username,
			Password:         sumithliveAcct.Password,
			DefaultProjectID: sumithliveAcct.Extra["projectId"],
		}),
		chemitoapiadapter.New(chemitoapiadapter.Config{
			BaseURL:  chemitoapiAcct.BaseURL,
			Username: chemitoapiAcct.Username,
			Password: chemitoapiAcct.Password,
		}),
	)
	streamSvc := &services.StreamService{Registry: registry}
	if cfg.ingestURL != "" {
		streamSvc.Ingest = services.NewIngestClient(cfg.ingestURL)
	} else {
		log.Printf("no ingest service configured (INGEST_URL empty): " +
			"serving only vendors that need no media server")
	}
	ubrs := newUnifiedBridgeServer(streamSvc, buses)

	// One-time auto-start on process boot (redeploy/restart), so cams are
	// already coming up by the time anyone looks instead of needing a
	// first explicit request — but NOT a recurring background poll: some
	// devices (Chemito) have a small real concurrent-live-session
	// capacity, and continuously re-triggering burns through it for
	// channels nobody's watching. After this one pass, everything is
	// on-demand (GET /api/stream/{id}?cam=N, POST /api/bridge/start).
	go runStartupAutoStart(streamSvc, buses)

	// So GET /api/fleet (and /api/bus/{id}, /api/stream/{id}) also see
	// buses live via an embed-kind vendor, which never touch MediaMTX —
	// both ones someone has explicitly started (directKeys) and everything
	// a vendor account reports knowing about (vendorRoster).
	api.directKeys = streamSvc.ActiveDirectKeys
	api.vendorRoster = streamSvc.VendorRoster
	api.channelCounts = streamSvc.ChannelCounts

	// GET /api/stream/{id}?cam=N starts the bridge on demand if it isn't
	// already active — the frontend never has to call
	// POST /api/bridge/start itself; that endpoint is internal now (kept
	// below for admin/debug and as what this hook calls under the hood).
	api.ensureStream = ubrs.ensureStream

	// Vendor HTTP-FLV byte pipe for KindFLV sources (Chemito): the page
	// plays this path with mpegts.js. Not MediaMTX — see handleFLVProxy.
	mux.HandleFunc("GET /api/flv/{key}", ubrs.handleFLVProxy)

	// Live view of the FLV fan-out: channels, viewers, reconnects, drops.
	mux.HandleFunc("GET /api/hub", ubrs.handleHub)

	mux.HandleFunc("POST /api/bridge/start", ubrs.handleStart)
	// POST stop stays either way: it also drops a tracked direct (FLV/HLS)
	// session, which exists with no ingest side at all. GET /api/bridge
	// lists ingest remux jobs only, so it is empty by construction here.
	mux.HandleFunc("POST /api/bridge/stop", ubrs.handleStop)
	if ingestEnabled {
		mux.HandleFunc("GET /api/bridge", ubrs.handleList)
	}

	// Admin/debug only - direct per-vendor calls with credentials in the
	// request, kept off the "/" endpoint listing. The castmaster/n9m
	// equivalents moved to the media-MTX service (POST /start there).
	mux.HandleFunc("POST /api/bridge/sumithlive/start", handleSumithLiveStart)
	mux.HandleFunc("GET /api/bridge/sumithlive/vehicles", handleSumithLiveVehicles)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// "/" in Go's ServeMux is a catch-all, so without this every
		// unmatched path — a typo, or a route deliberately not registered
		// because this deployment has no ingest side — answered 200 with
		// the endpoint listing. That looks like success to a caller.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Frontend-facing only. The per-vendor /api/bridge/sumithlive/...
		// routes above still work but are admin/debug - left off this
		// listing on purpose.
		endpoints := []string{
			"GET /api/fleet",
			"GET /api/fleet/stream",
			"GET /api/bus/{id}",
			"GET /api/stream/{id}",
			"GET /api/hub",
			"POST /api/bridge/stop?key=",
			"GET /health",
		}
		if ingestEnabled {
			endpoints = append(endpoints,
				"GET /api/stream/{id}/recording?from=&to=",
				"GET /api/bridge")
		}
		writeJSON(w, map[string]any{
			"service":   "fleet-bms-api",
			"endpoints": endpoints,
		})
	})

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           cors(cfg.corsOrigin)(requireToken(cfg.apiToken)(logRequests(mux))),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("fleet-bms-api listening on %s", cfg.addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// startupAutoStartChannelsPerBus caps how many channels get started per bus
// at boot, regardless of what the vendor's own device-count reports — a
// DVR/NVR's reported channel count is how many camera inputs it has wired,
// not how many it can stream live simultaneously (Chemito reports 9 for
// DLPD8611 but has a real concurrent-session capacity far below that).
// ponytail: conservative guess, not a confirmed device spec.
const startupAutoStartChannelsPerBus = 4

// maxConcurrentBusStarts bounds how many buses start in parallel at boot —
// different buses are usually different vendor accounts/physical devices,
// so running them concurrently is safe and makes a redeploy come up faster;
// this just caps the burst against any one vendor's login endpoint.
const maxConcurrentBusStarts = 3

// runStartupAutoStart runs once, at process boot, so a redeploy comes up
// with cams already live instead of waiting for a first explicit request.
// Concurrency is bounded with a channel used as a semaphore (buffered chan
// struct{}) plus a sync.WaitGroup to know when every bus has finished —
// across buses/vendors run in parallel (bounded), but within one bus,
// channels start sequentially with a short stagger, since those share one
// device's limited concurrent-session capacity.
func runStartupAutoStart(streamSvc *services.StreamService, buses map[string]vendorconfig.Bus) {
	ctx := context.Background()
	sem := make(chan struct{}, maxConcurrentBusStarts)
	var wg sync.WaitGroup

	startBusChannels := func(busID, vendor string, params map[string]string, n int) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()

		for cam := 1; cam <= n; cam++ {
			if _, err := streamSvc.StartStream(ctx, domain.StreamRequest{
				Bus:          busID,
				Cam:          cam,
				Vendor:       vendor,
				Main:         true,
				Audio:        true, // see ensureStream — the hub corrects mic-less cameras
				VendorParams: params,
			}); err != nil {
				log.Printf("startup-auto-start: %s cam %d: %v", busID, cam, err)
			}
			time.Sleep(2 * time.Second)
		}
	}

	seen := make(map[string]bool)
	for _, entry := range streamSvc.VendorRoster(ctx) {
		busID := strings.TrimSuffix(entry.Key, "_1")
		seen[busID] = true
		n := entry.Channels
		if n == 0 || n > startupAutoStartChannelsPerBus {
			n = startupAutoStartChannelsPerBus
		}
		wg.Add(1)
		go startBusChannels(busID, entry.Vendor, entry.VendorParams, n)
	}

	// config/buses.json is only a fallback here, for vendors that can't be
	// listed (N9M devices connect in themselves; Castmaster has no
	// ListCameras support) — anything the roster already found is skipped.
	for busID, cfg := range buses {
		if seen[busID] {
			continue
		}
		wg.Add(1)
		go startBusChannels(busID, cfg.Vendor, cfg.VendorParams, startupAutoStartChannelsPerBus)
	}

	wg.Wait()
	log.Printf("startup-auto-start: done")
}

// envAllowEmpty is env() but honors a variable that is set to an empty
// string instead of falling back. The distinction matters for INGEST_URL:
// empty means "there is no ingest service", a real configuration rather
// than an omission, and silently substituting a default there produces a
// process that retries something deliberately not running.
func envAllowEmpty(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func reverseProxy(target string, stripPrefix string, decorate func(http.Header)) http.Handler {
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Fatalf("invalid proxy target %q: %v", target, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Director = func(req *http.Request) {
		incomingPath := req.URL.Path
		if stripPrefix != "" {
			incomingPath = strings.TrimPrefix(incomingPath, stripPrefix)
		}

		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.URL.Path = singleJoiningSlash(targetURL.Path, incomingPath)
		req.Host = targetURL.Host
		if targetURL.RawQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetURL.RawQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetURL.RawQuery + "&" + req.URL.RawQuery
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// MediaMTX sets its own CORS headers on HLS/WebRTC/playback
		// responses; the outer cors() middleware already sets the
		// canonical one for this app. httputil.ReverseProxy adds (not
		// replaces) headers when copying the upstream response, so
		// without this the client sees two Access-Control-Allow-Origin
		// values — which browsers correctly treat as invalid and refuse.
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Access-Control-Allow-Methods")
		resp.Header.Del("Access-Control-Allow-Headers")
		resp.Header.Del("Vary")

		if decorate != nil {
			decorate(resp.Header)
		}
		rewriteProxyLocation(resp.Header, targetURL, stripPrefix)
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error for %s: %v", r.URL.Path, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	return proxy
}

func rewriteProxyLocation(header http.Header, targetURL *url.URL, stripPrefix string) {
	if stripPrefix == "" {
		return
	}

	location := header.Get("Location")
	if location == "" {
		return
	}

	if strings.HasPrefix(location, "/") {
		if !strings.HasPrefix(location, stripPrefix+"/") && location != stripPrefix {
			header.Set("Location", stripPrefix+location)
		}
		return
	}

	locationURL, err := url.Parse(location)
	if err != nil || locationURL.Scheme == "" || locationURL.Host == "" {
		return
	}
	if locationURL.Scheme != targetURL.Scheme || locationURL.Host != targetURL.Host {
		return
	}

	locationURL.Scheme = ""
	locationURL.Host = ""
	if !strings.HasPrefix(locationURL.Path, stripPrefix+"/") && locationURL.Path != stripPrefix {
		locationURL.Path = stripPrefix + locationURL.Path
	}
	header.Set("Location", locationURL.String())
}

func noCache(header http.Header) {
	header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
}

func singleJoiningSlash(base, next string) string {
	switch {
	case base == "":
		return next
	case next == "":
		return base
	case strings.HasSuffix(base, "/") && strings.HasPrefix(next, "/"):
		return base + next[1:]
	case !strings.HasSuffix(base, "/") && !strings.HasPrefix(next, "/"):
		return base + "/" + next
	default:
		return base + next
	}
}

// cors allows a browser-hosted frontend on a different origin to call this
// API directly (fetch to /api/*, plus the proxied /live, /whep, /playback
// paths). allowedOrigin is a single origin (or "*") from CORS_ALLOWED_ORIGIN;
// "*" is fine for local dev but should be pinned to the real frontend origin
// in production.
func cors(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
