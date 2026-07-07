package main

import (
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type config struct {
	addr             string
	mediaMTXHLS      string
	mediaMTXWHEP     string
	mediaMTXAPI      string
	mediaMTXPlayback string
}

func main() {
	cfg := config{
		addr:             env("ADDR", ":8080"),
		mediaMTXHLS:      env("MEDIAMTX_HLS_URL", "http://localhost:8888"),
		mediaMTXWHEP:     env("MEDIAMTX_WEBRTC_URL", "http://localhost:8889"),
		mediaMTXAPI:      env("MEDIAMTX_API_URL", "http://localhost:9997/v3"),
		mediaMTXPlayback: env("MEDIAMTX_PLAYBACK_URL", "http://localhost:9996"),
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

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"service": "fleet-bms-api",
			"endpoints": []string{
				"GET /api/fleet",
				"GET /api/bus/{id}",
				"GET /api/stream/{id}",
				"GET /api/stream/{id}/recording?from=&to=",
				"GET /health",
			},
		})
	})

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           logRequests(mux),
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

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
