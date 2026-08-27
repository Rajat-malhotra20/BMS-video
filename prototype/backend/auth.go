package main

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

// requireToken gates every route behind a shared bearer token.
//
// This exists because the API is reachable from the public internet on a
// NodePort and, until now, needed nothing to watch any camera in the fleet
// or open sessions on any device. Confirmed not theoretical: an internet
// scanner probed GET /.env and POST / on the deployed pod on 2026-08-26.
//
// The token travels either as "Authorization: Bearer <token>" or as a
// ?token= query parameter. The query form is not decoration: a browser
// playing HLS through a plain <video src> cannot attach a header, so a
// header-only scheme would make those cameras unplayable.
//
// An empty token leaves the API open and says so loudly at boot. That is
// deliberate — turning auth on by default would black out the running
// deployment the moment this ships, which is a worse failure than the one
// it prevents. Set API_TOKEN in the deployment to close it.
func requireToken(token string) func(http.Handler) http.Handler {
	if token == "" {
		log.Printf("WARNING: API_TOKEN is not set - this API is UNAUTHENTICATED. " +
			"Anyone who can reach it can view every camera and open vendor sessions. " +
			"Set API_TOKEN before exposing it beyond a trusted network.")
		return func(next http.Handler) http.Handler { return next }
	}

	want := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Kubernetes probes and CORS preflights must not need a token:
			// the probe cannot carry one, and a 401 on preflight breaks the
			// real request that follows.
			if r.URL.Path == "/health" || r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got == "" {
				got = r.URL.Query().Get("token")
			}
			// Constant time: a length-independent compare would leak the
			// token a character at a time to anyone who can measure.
			if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="fleet-bms-api"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
