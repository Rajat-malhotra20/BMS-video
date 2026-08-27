package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vendorconfig "mediamtx-console/config"
	"mediamtx-console/domain"
	"mediamtx-console/services"
	"mediamtx-console/vendors"
)

// flvStubAdapter stands in for chemitoapi: a KindFLV source whose Upstream
// resolves to a test server and counts how often it was asked, so a test
// can assert how many vendor sessions a given number of viewers costs.
type flvStubAdapter struct {
	upstreamURL string
	resolves    int32
}

func (a *flvStubAdapter) Name() string { return "flvstub" }

func (a *flvStubAdapter) ResolveLiveSource(_ context.Context, _ domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV, Upstream: func(context.Context) (string, error) {
		atomic.AddInt32(&a.resolves, 1)
		return a.upstreamURL, nil
	}}, nil
}

func (a *flvStubAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	return nil, nil
}

// flvTag assembles one FLV tag the way a real stream carries it.
func flvTag(tagType byte, data []byte) []byte {
	tag := []byte{tagType, byte(len(data) >> 16), byte(len(data) >> 8), byte(len(data)), 0, 0, 0, 0, 0, 0, 0}
	tag = append(tag, data...)
	total := 11 + len(data)
	return append(tag, byte(total>>24), byte(total>>16), byte(total>>8), byte(total))
}

// flvHeader is a bare FLV file header plus PreviousTagSize0, video-only.
const flvHeader = "FLV\x01\x01\x00\x00\x00\x09\x00\x00\x00\x00"

// blockingUpstream writes body, then holds the connection open the way a
// live vendor stream does, so the reconnect loop doesn't fire mid-test.
func blockingUpstream(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
}

// viewer runs one GET against the proxy for d, then disconnects, and
// returns what it received.
func viewer(mux http.Handler, path string, d time.Duration) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	return rec
}

func flvTestServer(t *testing.T, bus, vendor string, adapter vendors.Adapter) (*unifiedBridgeServer, http.Handler) {
	t.Helper()
	svc := &services.StreamService{Registry: vendors.NewRegistry(adapter)}
	u := newUnifiedBridgeServer(svc, map[string]vendorconfig.Bus{bus: {Vendor: vendor}})
	// Production holds a device session for 10s after the last viewer, so
	// a page reload doesn't cost a new one. Tests would just wait on it.
	u.hub.idleGrace = 100 * time.Millisecond
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)
	return u, mux
}

// The whole point of the hub: viewers are free. Three people watching one
// camera must cost exactly ONE vendor session, because these devices ration
// concurrent channels and offer no way to close one.
func TestFLVProxy_OneUpstreamServesManyViewers(t *testing.T) {
	// 0x05 = the vendor's lie: "this stream has audio and video". The proxy
	// must rewrite it to 0x01 for an audio=0 request, since no audio tags
	// ever follow and players stall waiting for the promised track.
	upstream := blockingUpstream([]byte("FLV\x01\x05fake-tag-bytes"))
	defer upstream.Close()
	const wantBody = "FLV\x01\x01fake-tag" // header + PreviousTagSize0, flag corrected

	stub := &flvStubAdapter{upstreamURL: upstream.URL}
	_, mux := flvTestServer(t, "BUS1", "flvstub", stub)

	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 3)
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = viewer(mux, "/api/flv/BUS1_2", 700*time.Millisecond)
		}(i)
	}
	wg.Wait()

	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Fatalf("viewer %d: status = %d, want 200 (body %q)", i, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "video/x-flv" {
			t.Errorf("viewer %d: Content-Type = %q, want video/x-flv", i, got)
		}
		if got := rec.Body.String(); got != wantBody {
			t.Errorf("viewer %d: body = %q, want %q (audio flag cleared)", i, got, wantBody)
		}
	}

	if got := atomic.LoadInt32(&stub.resolves); got != 1 {
		t.Errorf("vendor resolves = %d, want 1 - three viewers must share one device session", got)
	}
}

// A viewer joining a stream already in progress must get the codec headers
// and the current group-of-pictures, or its decoder has nothing to start
// from and the tile stays black until the next keyframe.
func TestFLVProxy_LateJoinerGetsCodecHeadersAndGOP(t *testing.T) {
	script := flvTag(0x12, []byte("onMetaData-ish"))
	videoSeq := flvTag(0x09, []byte{0x17, 0x00, 0, 0, 0, 'c', 'f', 'g'}) // AVC sequence header
	keyframe := flvTag(0x09, []byte{0x17, 0x01, 0, 0, 0, 'k', 'e', 'y'})
	interFrame := flvTag(0x09, []byte{0x27, 0x01, 0, 0, 0, 'i', 'n', 't'})

	stream := []byte(flvHeader)
	for _, tag := range [][]byte{script, videoSeq, keyframe, interFrame} {
		stream = append(stream, tag...)
	}
	upstream := blockingUpstream(stream)
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS2", "flvstub", &flvStubAdapter{upstreamURL: upstream.URL})

	// First viewer establishes the channel and lets it fill.
	go viewer(mux, "/api/flv/BUS2_1", time.Second)
	time.Sleep(300 * time.Millisecond)

	// Second viewer arrives mid-stream.
	late := viewer(mux, "/api/flv/BUS2_1", 400*time.Millisecond)
	if late.Code != http.StatusOK {
		t.Fatalf("late joiner: status = %d, want 200", late.Code)
	}

	want := []byte(flvHeader)
	for _, tag := range [][]byte{script, videoSeq, keyframe, interFrame} {
		want = append(want, tag...)
	}
	if got := late.Body.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("late joiner got %d bytes, want %d (header + script + AVC seq header + GOP)", len(got), len(want))
	}
}

// The connection must survive the camera going away. A vendor drop is the
// normal case on a moving vehicle; the viewer's socket staying open is what
// lets a tile recover on its own instead of erroring out.
func TestFLVProxy_ViewerSurvivesUpstreamDrop(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			_, _ = w.Write([]byte(flvHeader)) // first connection delivers a header, then dies
			return
		}
		w.WriteHeader(http.StatusRequestTimeout) // device stays dark afterwards
	}))
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS3", "flvstub", &flvStubAdapter{upstreamURL: upstream.URL})

	rec := viewer(mux, "/api/flv/BUS3_1", 5*time.Second)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 - a viewer that started playing must never be dropped", rec.Code)
	}
	// The viewer's own context ended it, not the vendor, and the socket was
	// kept warm with keep-alive tags while the device was dark.
	if !bytes.Contains(rec.Body.Bytes(), []byte("onKeepAlive")) {
		t.Error("no keep-alive tags during the outage - the connection would time out")
	}
}

// A camera that has never delivered video must fail fast rather than
// hanging a tile, and must not cost one vendor session per viewer.
func TestFLVProxy_DeadCameraFailsFastWithoutPerViewerRetries(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestTimeout) // what a stalled device really returns
	}))
	defer relay.Close()

	dead := &deadAdapter{url: relay.URL}
	u, mux := flvTestServer(t, "BUS9", "deadstub", dead)
	u.hub.idleGrace = 2 * time.Second // keep the channel alive across this test's viewers

	for i := 0; i < 6; i++ {
		rec := viewer(mux, "/api/flv/BUS9_1", 2*time.Second)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("viewer %d: status = %d, want 502", i, rec.Code)
		}
	}

	// One backing-off reconnect loop, not one vendor session per viewer.
	// Six per-viewer sessions is exactly what killed a device in the field.
	if got := atomic.LoadInt32(&dead.resolves); got > 3 {
		t.Errorf("vendor resolves = %d for 6 viewers, want <= 3 (single backing-off loop)", got)
	}
}

// Audio is requested for every camera now, but the header's audio bit must
// reflect what the stream actually carries. A camera with no microphone
// advertises audio and sends none; a player that believes the header waits
// forever for that track and never renders a frame.
func TestFLVProxy_ClearsAudioFlagWhenNoAudioArrives(t *testing.T) {
	upstream := blockingUpstream(append([]byte{'F', 'L', 'V', 0x01, 0x05}, "real-audio-stream"...))
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS7", "audiostub", &audioAdapter{url: upstream.URL})

	rec := viewer(mux, "/api/flv/BUS7_1", 3*time.Second)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := append([]byte{'F', 'L', 'V', 0x01, 0x01}, "real-aud"...)
	if got := rec.Body.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("body = %q, want %q - audio bit must be cleared, no audio tag arrived", got, want)
	}
}

// ...and a camera that does have a microphone must keep its audio bit and
// deliver the audio tags.
func TestFLVProxy_KeepsAudioFlagWhenAudioTagsArrive(t *testing.T) {
	audioSeq := flvTag(0x08, []byte{0xaf, 0x00, 0x12, 0x10}) // AAC sequence header
	videoSeq := flvTag(0x09, []byte{0x17, 0x00, 0, 0, 0, 'c', 'f', 'g'})
	audioTag := flvTag(0x08, []byte{0xaf, 0x01, 's', 'o', 'u', 'n', 'd'})

	stream := append([]byte(nil), []byte(flvHeader)...)
	stream[4] = 0x05 // this camera advertises audio and really has it
	for _, tag := range [][]byte{videoSeq, audioSeq, audioTag} {
		stream = append(stream, tag...)
	}
	upstream := blockingUpstream(stream)
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS8", "audiostub", &audioAdapter{url: upstream.URL})

	rec := viewer(mux, "/api/flv/BUS8_1", 2*time.Second)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.Bytes()
	if len(body) < 5 || body[4] != 0x05 {
		t.Fatalf("header flags = 0x%02x, want 0x05 kept - this camera really does send audio", body[4])
	}
	if !bytes.Contains(body, audioTag) {
		t.Error("audio tag never reached the viewer")
	}
	if !bytes.Contains(body, audioSeq) {
		t.Error("AAC sequence header never reached the viewer - a late joiner could not decode audio")
	}
}

func TestFLVProxy_RejectsBadKey(t *testing.T) {
	u := newUnifiedBridgeServer(&services.StreamService{Registry: vendors.NewRegistry()}, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/flv/{key}", u.handleFLVProxy)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/flv/not-a-key", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// deadAdapter's Upstream points at a server that always fails, standing in
// for a device that has gone offline (HTTP 408 from the relay).
type deadAdapter struct {
	url      string
	resolves int32
}

func (a *deadAdapter) Name() string { return "deadstub" }

func (a *deadAdapter) ResolveLiveSource(_ context.Context, _ domain.StreamRequest) (domain.LiveSource, error) {
	return domain.LiveSource{Kind: domain.KindFLV, Upstream: func(context.Context) (string, error) {
		atomic.AddInt32(&a.resolves, 1)
		return a.url, nil
	}}, nil
}

func (a *deadAdapter) ListCameras(context.Context, map[string]string) ([]domain.Camera, error) {
	return nil, nil
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

// A camera that was dark when first opened and came back later must serve
// the next viewer, not keep reporting the original failure. Both the
// "playable" and "dead on arrival" signals are latched, so a naive select
// between them picks at random and 502s a camera that is streaming fine.
func TestFLVProxy_RecoveredCameraStopsReporting502(t *testing.T) {
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) <= 2 {
			w.WriteHeader(http.StatusRequestTimeout) // device asleep
			return
		}
		_, _ = w.Write([]byte(flvHeader)) // device wakes up
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	u, mux := flvTestServer(t, "BUS4", "flvstub", &flvStubAdapter{upstreamURL: upstream.URL})
	u.hub.idleGrace = 3 * time.Second // keep the same channel across both viewers

	if rec := viewer(mux, "/api/flv/BUS4_1", 2*time.Second); rec.Code != http.StatusBadGateway {
		t.Fatalf("first viewer: status = %d, want 502 while the device is dark", rec.Code)
	}

	// Let the reconnect loop reach the now-awake device.
	time.Sleep(1500 * time.Millisecond)

	rec := viewer(mux, "/api/flv/BUS4_1", 500*time.Millisecond)
	if rec.Code != http.StatusOK {
		t.Fatalf("second viewer: status = %d, want 200 once the camera recovered", rec.Code)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("FLV")) {
		t.Errorf("second viewer body = %q, want an FLV stream", rec.Body.String())
	}
}

// Reconnecting upstream must not push a second FLV file header at viewers
// who are already mid-stream: it is not a tag, so a player parsing the
// stream reads those bytes as tag data and desyncs.
func TestFLVProxy_ReconnectDoesNotReinjectFLVHeader(t *testing.T) {
	first := flvTag(0x09, []byte{0x17, 0x01, 0, 0, 0, 'o', 'n', 'e'})
	second := flvTag(0x09, []byte{0x17, 0x01, 0, 0, 0, 't', 'w', 'o'})

	var conns int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := append([]byte(flvHeader), first...)
		if atomic.AddInt32(&conns, 1) > 1 {
			body = append([]byte(flvHeader), second...)
		}
		_, _ = w.Write(body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if atomic.LoadInt32(&conns) > 1 {
			<-r.Context().Done() // second connection stays up
		}
	}))
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS5", "flvstub", &flvStubAdapter{upstreamURL: upstream.URL})

	rec := viewer(mux, "/api/flv/BUS5_1", 1500*time.Millisecond)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.Bytes()
	if n := bytes.Count(body, []byte("FLV\x01")); n != 1 {
		t.Errorf("FLV file header appears %d times, want exactly 1 (reconnect re-injected it)", n)
	}
	if !bytes.Contains(body, second) {
		t.Error("tags from the reconnected upstream never reached the viewer")
	}
}

// A reconnect must not send the player backwards in time. Each upstream
// connection restarts its timestamps near zero; handed through unchanged,
// a viewer 5 minutes in receives a tag timestamped 0 and MSE stalls - bytes
// still flowing, picture frozen. Exactly the "it stops mid-stream" failure.
func TestFLVProxy_TimestampsStayMonotonicAcrossReconnect(t *testing.T) {
	tagAt := func(ts uint32, marker byte) []byte {
		tag := flvTag(0x09, []byte{0x17, 0x01, 0, 0, 0, marker})
		setTagTimestamp(tag, ts)
		return tag
	}

	var conns int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&conns, 1)
		body := append([]byte(flvHeader), tagAt(0, 'a')...)
		body = append(body, tagAt(1000, 'b')...) // first connection reaches t=1s
		if n > 1 {
			// Second connection restarts at zero, as the device really does.
			body = append([]byte(flvHeader), tagAt(0, 'c')...)
			body = append(body, tagAt(500, 'd')...)
		}
		_, _ = w.Write(body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if n > 1 {
			<-r.Context().Done()
		}
	}))
	defer upstream.Close()

	_, mux := flvTestServer(t, "BUS6", "flvstub", &flvStubAdapter{upstreamURL: upstream.URL})

	rec := viewer(mux, "/api/flv/BUS6_1", 1500*time.Millisecond)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Walk the tags the viewer actually received and check the timeline.
	body := rec.Body.Bytes()[13:] // past the FLV file header
	var seen []uint32
	for len(body) >= 11 {
		size := int(body[1])<<16 | int(body[2])<<8 | int(body[3])
		if len(body) < 11+size+4 {
			break
		}
		if body[0] == 0x09 {
			seen = append(seen, tagTimestamp(body[:11+size+4]))
		}
		body = body[11+size+4:]
	}

	// Three, not four: the viewer subscribes after the first keyframe has
	// already been superseded in the GOP cache, so it starts from the
	// second one. What matters is that the reconnect's tags continue the
	// timeline rather than restarting at zero.
	if len(seen) < 3 {
		t.Fatalf("received %d video tags (%v), want tags from both connections", len(seen), seen)
	}
	if seen[len(seen)-1] < 1000 {
		t.Errorf("last timestamp %d, want it past the first connection's 1000 - reconnect restarted the clock", seen[len(seen)-1])
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Fatalf("timestamps went backwards at tag %d: %v - the player would stall here", i, seen)
		}
	}
}

// A page refresh must be seamless: the viewer's socket closes and a new one
// opens a moment later. The channel has to survive that gap, or every
// refresh costs a fresh vendor session and a reconnect delay on a device
// that rations sessions and cannot close them.
func TestFLVProxy_RefreshReusesTheDeviceSession(t *testing.T) {
	upstream := blockingUpstream(append([]byte(flvHeader), flvTag(0x09, []byte{0x17, 0x01, 0, 0, 0, 'k'})...))
	defer upstream.Close()

	stub := &flvStubAdapter{upstreamURL: upstream.URL}
	u, mux := flvTestServer(t, "BUSR", "flvstub", stub)
	u.hub.idleGrace = flvIdleGrace // production value: survive a refresh

	if rec := viewer(mux, "/api/flv/BUSR_1", 400*time.Millisecond); rec.Code != http.StatusOK {
		t.Fatalf("first load: status = %d, want 200", rec.Code)
	}
	time.Sleep(700 * time.Millisecond) // the gap while the page reloads
	rec := viewer(mux, "/api/flv/BUSR_1", 400*time.Millisecond)
	if rec.Code != http.StatusOK {
		t.Fatalf("after refresh: status = %d, want 200", rec.Code)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("FLV")) {
		t.Error("after refresh: no FLV header - viewer did not resume cleanly")
	}

	if got := atomic.LoadInt32(&stub.resolves); got != 1 {
		t.Errorf("vendor resolves = %d, want 1 - a refresh must not cost a new device session", got)
	}
}
