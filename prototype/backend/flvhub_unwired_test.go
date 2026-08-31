package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
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

	first := &discardWriter{}
	req := httptest.NewRequest(http.MethodGet, "/api/flv/BUS_8", nil)
	hub.serve(first, req, "BUS_8", resolve, false)
	if first.status() != http.StatusBadGateway {
		t.Fatalf("first viewer status = %d, want 502", first.status())
	}
	if !hub.Unwired("BUS_8") {
		t.Fatal("channel not remembered as unwired after a connect-time EOF")
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
