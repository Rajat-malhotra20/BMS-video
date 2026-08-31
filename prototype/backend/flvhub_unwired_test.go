package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A DVR reports more channels than it has cameras (9 reported, 7 wired on
// DLPD8611). The empty ones accept the TCP connection and immediately EOF,
// and every attempt on one still costs a session against the device's small
// concurrent budget — which is what pushed working channels 6 and 7 into
// HTTP 408 on 2026-08-31. So the first EOF has to be the last connection we
// spend on that channel for a while.
func TestFLVHubUnwiredChannelIsAttemptedOnce(t *testing.T) {
	attempts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// Hijack and close without writing: the vendor's relay accepting
		// the connection and dropping it, which is a client-side EOF.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer upstream.Close()

	hub := newFLVHub()
	resolve := func(context.Context) (string, error) { return upstream.URL, nil }

	// A channel of this bus that is streaming, which is what makes the EOF
	// on BUS_8 mean "no camera here" rather than "this device is asleep".
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(flvHeader))
		_, _ = w.Write(flvTag(0x09, []byte("\x17\x00\x00\x00\x00config")))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer live.Close()
	hub.idleGrace = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.serve(&discardWriter{}, httptest.NewRequest(http.MethodGet, "/api/flv/BUS_1", nil).WithContext(ctx),
		"BUS_1", func(context.Context) (string, error) { return live.URL, nil }, false)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l, known := hub.Live("BUS_1"); known && l {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if l, known := hub.Live("BUS_1"); !known || !l {
		t.Fatal("sibling channel never went live, test cannot distinguish the two failures")
	}

	first := &discardWriter{}
	req := httptest.NewRequest(http.MethodGet, "/api/flv/BUS_8", nil)
	hub.serve(first, req, "BUS_8", resolve, false)
	if first.status() != http.StatusBadGateway {
		t.Fatalf("first viewer status = %d, want 502", first.status())
	}
	if !hub.Unwired("BUS_8") {
		t.Fatal("channel not remembered as unwired after a connect-time EOF beside a streaming sibling")
	}

	spent := attempts
	second := &discardWriter{}
	hub.serve(second, req, "BUS_8", resolve, false)
	if second.status() != http.StatusBadGateway {
		t.Fatalf("second viewer status = %d, want 502", second.status())
	}
	if attempts != spent {
		t.Fatalf("second viewer cost %d more device connection(s), want 0", attempts-spent)
	}

	if hub.Unwired("BUS_1") {
		t.Fatal("an untried channel must not be marked unwired")
	}
}

// A reconnect can drop us into the middle of a stream that never repeats
// its AVCDecoderConfigurationRecord — Chemito does exactly this. A viewer
// arriving after that reconnect still needs the record or its decoder never
// initializes, which is a black tile fed by a perfectly healthy 150KB/s of
// keyframes (measured on DLPD8611_6, 2026-08-31). So the channel must keep
// the last configuration it saw across connections.
//
// Run for H.265 as well as H.264: DLPD8611 mixes them (cams 1-5 H.264,
// 6-7 H.265) and the H.265 tags differ only in the codec nibble, which is
// exactly the sort of difference a classifier written against one codec
// silently drops.
func TestFLVHubKeepsDecoderConfigAcrossReconnect(t *testing.T) {
	for _, codec := range []struct {
		name string
		seq  string
		key  string
	}{
		{"h264", "\x17\x00\x00\x00\x00config", "\x17\x01\x00\x00\x00frame"},
		{"h265", "\x1c\x00\x00\x00\x00config", "\x1c\x01\x00\x00\x00frame"},
	} {
		t.Run(codec.name, func(t *testing.T) { keepsDecoderConfig(t, codec.seq, codec.key) })
	}
}

func keepsDecoderConfig(t *testing.T, avcSeq, keyfrm string) {
	connections := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connections++
		first := connections == 1
		_, _ = w.Write([]byte(flvHeader))
		if first {
			_, _ = w.Write(flvTag(0x09, []byte(avcSeq))) // only the first connection sends it
		}
		for i := 0; i < 3; i++ {
			_, _ = w.Write(flvTag(0x09, []byte(keyfrm)))
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if first {
			return // drop the connection: the hub reconnects
		}
		<-r.Context().Done()
	}))
	defer upstream.Close()

	hub := newFLVHub()
	hub.idleGrace = time.Millisecond // release the upstream as soon as the viewer goes, so Close doesn't block
	resolve := func(context.Context) (string, error) { return upstream.URL, nil }

	// Hold a viewer on the channel while it reconnects underneath, then
	// read what a viewer joining afterwards would be handed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held := &discardWriter{}
	go hub.serve(held, httptest.NewRequest(http.MethodGet, "/api/flv/BUS_1", nil).WithContext(ctx), "BUS_1", resolve, false)

	deadline := time.Now().Add(10 * time.Second)
	for connections < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if connections < 2 {
		t.Fatalf("upstream reconnected %d time(s), test needs 2", connections)
	}

	hub.mu.Lock()
	c := hub.chans["BUS_1"]
	hub.mu.Unlock()
	if c == nil {
		t.Fatal("channel disappeared")
	}
	// Give the second connection's tags time to land.
	time.Sleep(200 * time.Millisecond)
	_, backlog := c.subscribe()
	if !bytes.Contains(backlog, []byte("config")) {
		t.Fatal("a viewer joining after the reconnect gets no decoder configuration — its tile stays black")
	}
}

// The failure that shipped on 2026-08-31: DLPD8611's device stopped
// answering, every channel EOF'd on connect, and marking on the EOF alone
// concluded that all nine channels were cameraless — camsAvailable 0, the
// whole bus dark for ten minutes after the device recovered. An EOF with no
// sibling streaming is a device outage and must decide nothing.
func TestFLVHubDeviceOutageDoesNotBlacklistChannels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer upstream.Close()

	hub := newFLVHub()
	hub.idleGrace = time.Millisecond
	resolve := func(context.Context) (string, error) { return upstream.URL, nil }

	// Every channel of the bus is down, exactly as in the outage.
	for _, cam := range []string{"1", "2", "3"} {
		key := "BUS_" + cam
		rec := &discardWriter{}
		hub.serve(rec, httptest.NewRequest(http.MethodGet, "/api/flv/"+key, nil), key, resolve, false)
		if rec.status() != http.StatusBadGateway {
			t.Fatalf("%s status = %d, want 502", key, rec.status())
		}
	}
	for _, cam := range []string{"1", "2", "3"} {
		if hub.Unwired("BUS_" + cam) {
			t.Fatalf("BUS_%s blacklisted during a whole-device outage — the bus goes dark for %s", cam, flvUnwiredMemory)
		}
	}
}

// And once a channel does deliver video, any earlier mark must go — a
// camera wired up (or a device that was lying) must not wait out the TTL.
func TestFLVHubLiveVideoClearsTheUnwiredMark(t *testing.T) {
	hub := newFLVHub()
	hub.idleGrace = time.Millisecond
	hub.markUnwired("BUS_4")
	if !hub.Unwired("BUS_4") {
		t.Fatal("mark did not take")
	}

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(flvHeader))
		_, _ = w.Write(flvTag(0x09, []byte("\x17\x00\x00\x00\x00config")))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer live.Close()

	// serve() would refuse a marked key outright, so drive the channel the
	// way a resumed attempt does once the mark has expired.
	c := hub.channel("BUS_4", "", func(context.Context) (string, error) { return live.URL, nil }, false)
	// Stop the upstream before live.Close(), which otherwise waits on it.
	defer c.cancel()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && hub.Unwired("BUS_4") {
		time.Sleep(20 * time.Millisecond)
	}
	if hub.Unwired("BUS_4") {
		t.Fatal("channel delivered video and is still marked as having no camera")
	}
}

// A grid tile on the device's sub-stream and a fullscreen viewer on the main
// stream are two different vendor connections of one camera. They must not
// share a channel — sharing means whoever connects first decides everyone's
// quality — and the rest of the system must still see one camera, not two,
// or GET /api/fleet starts reporting cams that don't exist.
func TestFLVHubVariantsAreSeparateChannelsOfOneCamera(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	stream := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			asked[name]++
			mu.Unlock()
			_, _ = w.Write([]byte(flvHeader))
			_, _ = w.Write(flvTag(0x09, []byte("\x17\x00\x00\x00\x00config")))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		}))
	}
	main, sub := stream("main"), stream("sub")
	defer main.Close()
	defer sub.Close()

	hub := newFLVHub()
	hub.idleGrace = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := func(path string) *http.Request {
		return httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	}
	go hub.serveVariant(&discardWriter{}, req("/api/flv/BUS_1"), "BUS_1", "",
		func(context.Context) (string, error) { return main.URL, nil }, true)
	go hub.serveVariant(&discardWriter{}, req("/api/flv/BUS_1?sub=1&audio=0"), "BUS_1", "sub,noaudio",
		func(context.Context) (string, error) { return sub.URL, nil }, false)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		both := asked["main"] > 0 && asked["sub"] > 0
		mu.Unlock()
		if both {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	gotMain, gotSub := asked["main"], asked["sub"]
	mu.Unlock()
	if gotMain == 0 || gotSub == 0 {
		t.Fatalf("main asked %d times, sub %d — each quality needs its own upstream", gotMain, gotSub)
	}

	hub.mu.Lock()
	channels := len(hub.chans)
	hub.mu.Unlock()
	if channels != 2 {
		t.Fatalf("hub holds %d channels, want 2 (one per quality)", channels)
	}

	// One camera, whichever quality is streaming: this is what the fleet
	// view reads, and it must not double-count or miss a sub-only tile.
	if live, known := hub.Live("BUS_1"); !known || !live {
		t.Fatalf("Live(BUS_1) = (%v, %v), want live and known", live, known)
	}
	seen := map[string]bool{}
	for _, s := range hub.stats() {
		if s.Key != "BUS_1" {
			t.Fatalf("stats reported key %q, want the camera key without the quality", s.Key)
		}
		seen[s.Variant] = true
	}
	if !seen[""] || !seen["sub,noaudio"] {
		t.Fatalf("hub stats variants = %v, want both the main and sub lines", seen)
	}
}
