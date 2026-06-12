package main

import (
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type config struct {
	addr              string
	staticDir         string
	mediaMTXHLS       string
	mediaMTXWHEP      string
	mediaMTXAPI       string
	mediaMTXPlayback  string
}

func main() {
	cfg := config{
		addr:         env("ADDR", ":8080"),
		staticDir:    env("STATIC_DIR", "./frontend"),
		mediaMTXHLS:  env("MEDIAMTX_HLS_URL", "http://localhost:8888"),
		mediaMTXWHEP: env("MEDIAMTX_WEBRTC_URL", "http://localhost:8889"),
		mediaMTXAPI:      env("MEDIAMTX_API_URL", "http://localhost:9997/v3"),
		mediaMTXPlayback: env("MEDIAMTX_PLAYBACK_URL", "http://localhost:9996"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/live/", reverseProxy(cfg.mediaMTXHLS, "", noCache))
	mux.Handle("/whep/", reverseProxy(cfg.mediaMTXWHEP, "", nil))
	mux.Handle("/mtx-api/", reverseProxy(cfg.mediaMTXAPI, "/mtx-api", nil))

	api := newAPIServer(cfg.mediaMTXAPI)
	mux.HandleFunc("GET /api/fleet", api.handleFleet)
	mux.HandleFunc("GET /api/bus/{id}", api.handleBusDetail)
	mux.Handle("/playback/", reverseProxy(cfg.mediaMTXPlayback, "/playback", noCache))

	mux.Handle("/", staticSPA(cfg.staticDir))

	server := &http.Server{
		Addr:              cfg.addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("MediaMTX console backend listening on %s", cfg.addr)
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
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error for %s: %v", r.URL.Path, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	return proxy
}

func noCache(header http.Header) {
	header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
}

func staticSPA(root string) http.Handler {
	files := http.Dir(root)
	fileServer := http.FileServer(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
			if file, err := files.Open(name); err == nil {
				_ = file.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
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
