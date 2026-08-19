package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/services"
	"mediamtx-console/vendorclients/n9mserver"
	"mediamtx-console/vendors"
	castmasteradapter "mediamtx-console/vendors/castmaster"
	chemitoapiadapter "mediamtx-console/vendors/chemitoapi"
	n9madapter "mediamtx-console/vendors/n9m"
	sumithliveadapter "mediamtx-console/vendors/sumithlive"
)

type config struct {
	addr             string
	mediaMTXHLS      string
	mediaMTXWHEP     string
	mediaMTXAPI      string
	mediaMTXPlayback string
	mediaMTXRTSP     string
	n9mSignalAddr    string
	n9mMediaAddr     string
	corsOrigin       string
	vendorsConfig    string
	busesConfig      string
}

func main() {
	cfg := config{
		addr:             env("ADDR", ":8080"),
		mediaMTXHLS:      env("MEDIAMTX_HLS_URL", "http://localhost:8888"),
		mediaMTXWHEP:     env("MEDIAMTX_WEBRTC_URL", "http://localhost:8889"),
		mediaMTXAPI:      env("MEDIAMTX_API_URL", "http://localhost:9997/v3"),
		mediaMTXPlayback: env("MEDIAMTX_PLAYBACK_URL", "http://localhost:9996"),
		mediaMTXRTSP:     env("MEDIAMTX_RTSP_PUBLISH_URL", "rtsp://localhost:8554"),
		n9mSignalAddr:    env("N9M_SIGNAL_ADDR", ":9500"),
		n9mMediaAddr:     env("N9M_MEDIA_ADDR", ":9501"),
		corsOrigin:       env("CORS_ALLOWED_ORIGIN", "*"),
		vendorsConfig:    env("VENDORS_CONFIG", "config/vendors.json"),
		busesConfig:      env("BUSES_CONFIG", "config/buses.json"),
	}

	// n9mSrv is the Chemito/N9M device server: accepts OBU signaling +
	// media connections on two ports (see n9mSignalAddr/n9mMediaAddr
	// below).
	n9mSrv := n9mserver.NewServer(log.Default())
	if ln, err := net.Listen("tcp", cfg.n9mSignalAddr); err != nil {
		log.Printf("n9m: signaling listener disabled: %v", err)
	} else {
		log.Printf("n9m signaling listening on %s", cfg.n9mSignalAddr)
		go func() {
			if err := n9mSrv.ServeSignaling(ln); err != nil {
				log.Printf("n9m: signaling listener stopped: %v", err)
			}
		}()
	}
	if ln, err := net.Listen("tcp", cfg.n9mMediaAddr); err != nil {
		log.Printf("n9m: media listener disabled: %v", err)
	} else {
		log.Printf("n9m media listening on %s", cfg.n9mMediaAddr)
		go func() {
			if err := n9mSrv.ServeMedia(ln); err != nil {
				log.Printf("n9m: media listener stopped: %v", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/live/", reverseProxy(cfg.mediaMTXHLS, "/live", noCache))
	mux.Handle("/whep/", reverseProxy(cfg.mediaMTXWHEP, "/whep", nil))
	mux.Handle("/mtx-api/", reverseProxy(cfg.mediaMTXAPI, "/mtx-api", nil))

	api := newAPIServer(cfg.mediaMTXAPI)
	mux.HandleFunc("GET /api/fleet", api.handleFleet)
	mux.HandleFunc("GET /api/bus/{id}", api.handleBusDetail)
	mux.HandleFunc("GET /api/stream/{id}", api.handleStreamLive)
	mux.HandleFunc("GET /api/stream/{id}/recording", api.handleStreamRecording)
	mux.Handle("/playback/", reverseProxy(cfg.mediaMTXPlayback, "/playback", noCache))

	brs := newBridgeServer(cfg.mediaMTXRTSP, n9mSrv)

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
	castmasterAcct := vendorAccounts["castmaster"]
	sumithliveAcct := vendorAccounts["sumithlive"]
	chemitoapiAcct := vendorAccounts["chemitoapi"]
	registry := vendors.NewRegistry(
		castmasteradapter.New(castmasteradapter.Config{
			BaseURL:  castmasterAcct.BaseURL,
			Username: castmasterAcct.Username,
			Password: castmasterAcct.Password,
		}),
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
		n9madapter.New(n9mSrv),
	)
	streamSvc := &services.StreamService{
		Registry:       registry,
		Supervisor:     brs.supervisor,
		RTSPPublish:    cfg.mediaMTXRTSP,
		RestartBackoff: brs.restartBackoff,
	}
	ubrs := newUnifiedBridgeServer(streamSvc, buses)

	// Streams are on-demand only, for every vendor — nothing starts until
	// GET /api/stream/{id}?cam=N (or POST /api/bridge/start) explicitly
	// asks for it. An earlier version eagerly auto-started every channel
	// in the background, but some devices (Chemito) have a small real
	// concurrent-live-session capacity, and holding channels open that
	// nobody's watching burns through it — better to only occupy a slot
	// for a channel someone's actually viewing. /api/fleet's bus listing
	// (which buses exist at all) still comes from api.vendorRoster below,
	// independent of whether anything's actually streaming.

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

	mux.HandleFunc("POST /api/bridge/start", ubrs.handleStart)
	mux.HandleFunc("POST /api/bridge/stop", ubrs.handleStop)
	mux.HandleFunc("GET /api/bridge", ubrs.handleList)

	// Admin/debug only — direct per-vendor calls. Not for frontend use, so
	// they're kept off the "/" endpoint listing; still routed for
	// debugging one vendor in isolation and for looking up device ids
	// (e.g. GET /api/bridge/n9m/devices) to put into config/buses.json.
	// Jobs started this way still show up in GET /api/bridge and stop via
	// POST /api/bridge/stop above — same underlying Supervisor.
	mux.HandleFunc("POST /api/bridge/castmaster/start", brs.handleCastmasterStart)
	mux.HandleFunc("POST /api/bridge/n9m/start", brs.handleN9mStart)
	mux.HandleFunc("POST /api/bridge/sumithlive/start", brs.handleSumithLiveStart)
	mux.HandleFunc("GET /api/bridge/sumithlive/vehicles", brs.handleSumithLiveVehicles)
	mux.HandleFunc("GET /api/bridge/n9m/devices", brs.handleN9mDevices)

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		// Frontend-facing only. The per-vendor /api/bridge/{castmaster,n9m,
		// sumithlive}/... routes above still work but are admin/debug —
		// left off this listing on purpose.
		writeJSON(w, map[string]any{
			"service": "fleet-bms-api",
			"endpoints": []string{
				"GET /api/fleet",
				"GET /api/bus/{id}",
				"GET /api/stream/{id}",
				"GET /api/stream/{id}/recording?from=&to=",
				"POST /api/bridge/stop?key=",
				"GET /api/bridge",
				"GET /health",
			},
		})
	})

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           cors(cfg.corsOrigin)(logRequests(mux)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("fleet-bms-api listening on %s", cfg.addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
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
