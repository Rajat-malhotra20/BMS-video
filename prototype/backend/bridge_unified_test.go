package main

import (
	"context"

	"net/http"
	"net/http/httptest"
	"testing"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/domain"
	"mediamtx-console/services"
	"mediamtx-console/vendors"
)

// flvStubAdapter stands in for chemitoapi: a KindFLV source whose Upstream
// resolves to a test server and counts how often it was asked, so the test
// can assert the token is re-resolved per viewer rather than cached.
type flvStubAdapter struct {
	upstreamURL string
	resolves    int
}

func (a *flvStubAdapter) Name() string { return "flvstub" }

func (a *flvStubAdapter) ResolveLiveSource(_ context.Context, _ domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV, Upstream: func(context.Context) (string, error) {
		a.resolves++
		return a.upstreamURL, nil
	}}, nil
}

func (a *flvStubAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	return nil, nil
}

func TestFLVProxy_StreamsUpstreamAndReresolvesPerViewer(t *testing.T) {
	// 0x05 = the vendor's lie: "this stream has audio and video". The proxy
	// must rewrite it to 0x01 for an audio=0 request, since no audio tags
	// ever follow and players stall waiting for the promised track.
	const payload = "FLV\x01\x05fake-tag-bytes"
	const wantBody = "FLV\x01\x01fake-tag-bytes"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	stub := &flvStubAdapter{upstreamURL: upstream.URL}
	svc := &services.StreamService{
		Registry: vendors.NewRegistry(stub),
	}
	u := newUnifiedBridgeServer(svc, map[string]vendorconfig.Bus{
		"BUS1": {Vendor: "flvstub"},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)

	// First viewer: nothing started yet, so the proxy must start the bus
	// itself (the bookmark/fresh-process path) and still serve bytes.
	for viewer := 1; viewer <= 2; viewer++ {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flv/BUS1_2", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("viewer %d: status = %d, want 200 (body %q)", viewer, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "video/x-flv" {
			t.Errorf("viewer %d: Content-Type = %q, want video/x-flv", viewer, got)
		}
		if got := rec.Body.String(); got != wantBody {
			t.Errorf("viewer %d: body = %q, want %q (audio flag cleared)", viewer, got, wantBody)
		}
	}

	// The point of Upstream being a func: each viewer gets a freshly
	// resolved vendor URL, since Chemito's token is single-use.
	if stub.resolves != 2 {
		t.Errorf("Upstream resolves = %d, want 2 (one per viewer)", stub.resolves)
	}

	// StartStream must have run exactly once — a second run would re-trigger
	// the vendor's real start-streaming side effect on every connect.
	if !svc.IsActive("BUS1_2") {
		t.Error("BUS1_2 not tracked as active after proxying")
	}
	entry, ok := svc.DirectEntryFor("BUS1_2")
	if !ok || entry.Kind != domain.KindFLV || entry.HLSURL != "/api/flv/BUS1_2" {
		t.Errorf("DirectEntryFor = %+v, %v; want KindFLV with /api/flv/BUS1_2", entry, ok)
	}
}

func TestFLVProxy_RejectsBadKey(t *testing.T) {
	u := newUnifiedBridgeServer(&services.StreamService{
		Registry: vendors.NewRegistry(),
	}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flv/not-a-key", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// deadAdapter's Upstream points at a server that always fails, standing in
// for a Chemito device that has gone offline (HTTP 408 from the relay).
type deadAdapter struct {
	url      string
	resolves int
}

func (a *deadAdapter) Name() string { return "deadstub" }

func (a *deadAdapter) ResolveLiveSource(_ context.Context, _ domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV, Upstream: func(context.Context) (string, error) {
		a.resolves++
		return a.url, nil
	}}, nil
}

func (a *deadAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	return nil, nil
}

// A failing channel must not be retried against the vendor on every
// reconnect: Chemito can't close sessions, so an unthrottled retry loop
// leaks one per attempt and takes the whole device down.
func TestFLVProxy_FailureCooldownStopsVendorRetries(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout) // what a stalled device really returns
	}))
	defer relay.Close()

	dead := &deadAdapter{url: relay.URL}
	svc := &services.StreamService{
		Registry: vendors.NewRegistry(dead),
	}
	u := newUnifiedBridgeServer(svc, map[string]vendorconfig.Bus{"BUS9": {Vendor: "deadstub"}})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)

	req := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flv/BUS9_1", nil))
		return rec
	}

	// First attempt reaches the vendor and reports why it failed.
	if rec := req(); rec.Code != http.StatusBadGateway {
		t.Fatalf("first attempt: status = %d, want 502", rec.Code)
	}

	// The next several reconnects must be refused locally — no new vendor
	// call, so no new phantom session.
	for i := 0; i < 5; i++ {
		rec := req()
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("retry %d: status = %d, want 503 (cooling down)", i, rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("retry %d: missing Retry-After header", i)
		}
	}

	if dead.resolves != 1 {
		t.Errorf("vendor resolves = %d, want 1 (5 reconnects absorbed by the cooldown)", dead.resolves)
	}
}

// A stream genuinely requested with audio must keep its audio flag — the
// header correction is for the audio=0 case only, not a blanket rewrite.
func TestFLVProxy_KeepsAudioFlagWhenAudioRequested(t *testing.T) {
	const payload = "FLV\x01\x05real-audio-stream"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	svc := &services.StreamService{
		Registry: vendors.NewRegistry(&audioAdapter{url: upstream.URL}),
	}
	u := newUnifiedBridgeServer(svc, map[string]vendorconfig.Bus{"BUS7": {Vendor: "audiostub"}})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flv/BUS7_1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != payload {
		t.Errorf("body = %q, want %q unchanged", got, payload)
	}
}

type audioAdapter struct{ url string }

func (a *audioAdapter) Name() string { return "audiostub" }

func (a *audioAdapter) ResolveLiveSource(_ context.Context, _ domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV, HasAudio: true, Upstream: func(context.Context) (string, error) {
		return a.url, nil
	}}, nil
}

func (a *audioAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	return nil, nil
}
