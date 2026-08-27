package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"
)

// discardWriter is an http.ResponseWriter that throws the body away, so
// the heap number below measures the hub and not the test harness. A
// ResponseRecorder would buffer every byte every viewer receives — 150
// viewers x 200KB/s makes the harness dwarf the thing under test.
type discardWriter struct {
	mu     sync.Mutex
	code   int
	header http.Header
	n      int64
}

func (d *discardWriter) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}

func (d *discardWriter) Write(b []byte) (int, error) {
	d.mu.Lock()
	d.n += int64(len(b))
	d.mu.Unlock()
	return len(b), nil
}

func (d *discardWriter) WriteHeader(code int) { d.code = code }
func (d *discardWriter) Flush()               {}

func (d *discardWriter) status() int {
	if d.code == 0 {
		return http.StatusOK
	}
	return d.code
}

// A fleet-sized load has never been run against this hub, and the failure
// modes that matter (goroutine growth, unbounded GOP buffers, viewers
// starving each other) only appear with many channels and many viewers at
// once. Synthetic upstreams, so it exercises our code at a scale the real
// devices would refuse.
func TestFLVHubScale_ManyChannelsManyViewers(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}

	const (
		channels       = 30
		viewersPerChan = 5
		runFor         = 6 * time.Second
	)

	// One synthetic camera pushing ~25 tags/sec of 8KB each (~200KB/s,
	// comparable to a real main codestream), keyframe every 25 tags.
	payload := make([]byte, 8<<10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(flvHeader))
		f, _ := w.(http.Flusher)
		if f != nil {
			f.Flush()
		}
		var ts uint32
		for i := 0; ; i++ {
			frame := byte(0x27) // inter frame
			if i%25 == 0 {
				frame = 0x17 // keyframe
			}
			tag := flvTag(0x09, append([]byte{frame, 0x01, 0, 0, 0}, payload...))
			setTagTimestamp(tag, ts)
			ts += 40
			if _, err := w.Write(tag); err != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(40 * time.Millisecond):
			}
		}
	}))
	defer upstream.Close()

	stub := &flvStubAdapter{upstreamURL: upstream.URL}
	u, mux := flvTestServer(t, "SCALE", "flvstub", stub)
	u.hub.idleGrace = 200 * time.Millisecond

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	goroutinesBefore := runtime.NumGoroutine()

	var wg sync.WaitGroup
	codes := make([]int, channels*viewersPerChan)
	for ch := 1; ch <= channels; ch++ {
		for v := 0; v < viewersPerChan; v++ {
			wg.Add(1)
			go func(ch, idx int) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), runFor)
				defer cancel()
				w := &discardWriter{}
				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/flv/SCALE_%d", ch), nil)
				mux.ServeHTTP(w, req.WithContext(ctx))
				codes[idx] = w.status()
			}(ch, (ch-1)*viewersPerChan+v)
		}
	}

	// Sample while the load is running, not after it drains.
	time.Sleep(runFor / 2)
	stats := u.hub.stats()
	var mid runtime.MemStats
	runtime.ReadMemStats(&mid)
	goroutinesMid := runtime.NumGoroutine()
	wg.Wait()

	served := 0
	for _, c := range codes {
		if c == http.StatusOK {
			served++
		}
	}
	t.Logf("served %d/%d viewers across %d channels", served, len(codes), channels)
	t.Logf("heap %.1fMB -> %.1fMB | goroutines %d -> %d",
		float64(before.HeapAlloc)/1e6, float64(mid.HeapAlloc)/1e6, goroutinesBefore, goroutinesMid)

	live, drops := 0, int64(0)
	for _, s := range stats {
		if s.Live {
			live++
		}
		drops += s.DroppedTags
	}
	t.Logf("hub: %d channels live, %d dropped tags total", live, drops)

	if served != len(codes) {
		t.Errorf("served %d of %d viewers, want all", served, len(codes))
	}
	if live != channels {
		t.Errorf("%d channels live, want %d", live, channels)
	}
	// One vendor session per channel regardless of viewer count - the whole
	// premise. Reconnects can add a few, so allow a small margin.
	if got := int(stub.resolves); got > channels*2 {
		t.Errorf("vendor resolves = %d for %d channels x %d viewers, want ~%d",
			got, channels, viewersPerChan, channels)
	}
	// Measured 18MB for this load (30 channels x 5 viewers, ~200KB/s each),
	// roughly 600KB per channel. 64MB leaves generous headroom while still
	// failing if the GOP cache or a viewer queue starts leaking.
	if mid.HeapAlloc > 64<<20 {
		t.Errorf("heap %.0fMB under load, want under 64MB - something is retaining buffers",
			float64(mid.HeapAlloc)/1e6)
	}
	// The design is one goroutine per viewer plus one reader per channel.
	// The count also carries the synthetic upstream's own server handler
	// and client transport goroutines (a few per channel), which is why the
	// bound is not tight — it is here to catch per-reconnect leaks, where
	// the number would climb with time rather than sit flat.
	if limit := channels*viewersPerChan + channels*6 + 50; goroutinesMid > limit {
		t.Errorf("goroutines = %d (limit %d) for %d viewers + %d channels - suspect a leak",
			goroutinesMid, limit, channels*viewersPerChan, channels)
	}
}
