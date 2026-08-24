package bridge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorRestartsOnFailure(t *testing.T) {
	s := NewSupervisor()
	var calls int32

	run := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return errors.New("boom")
		}
		<-ctx.Done()
		return ctx.Err()
	}

	if err := s.Start("job1", run, 5*time.Millisecond); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.StopAndWait("job1")

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&calls) < 3 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for 3 attempts, got %d", atomic.LoadInt32(&calls))
		case <-time.After(10 * time.Millisecond):
		}
	}

	// The 3rd invocation is intentionally still running (blocked on
	// ctx.Done()) at this point, so job.attempts only reflects the 2 prior
	// failed-and-returned runs, not the in-flight one.
	statuses := s.List()
	if len(statuses) != 1 || statuses[0].Key != "job1" {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}
	if statuses[0].Attempts < 2 {
		t.Fatalf("expected >=2 completed attempts, got %d", statuses[0].Attempts)
	}
}

func TestSupervisorStartTwiceRejected(t *testing.T) {
	s := NewSupervisor()
	block := make(chan struct{})
	run := func(ctx context.Context) error {
		<-block
		return nil
	}

	if err := s.Start("job1", run, time.Second); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() {
		close(block)
		s.StopAndWait("job1")
	}()

	err := s.Start("job1", run, time.Second)
	if err == nil {
		t.Fatal("expected ErrAlreadyRunning")
	}
	if _, ok := err.(*ErrAlreadyRunning); !ok {
		t.Fatalf("expected *ErrAlreadyRunning, got %T: %v", err, err)
	}
}

func TestSupervisorStopAndWaitStopsCleanly(t *testing.T) {
	s := NewSupervisor()
	started := make(chan struct{})
	run := func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	if err := s.Start("job1", run, time.Second); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	if !s.Running("job1") {
		t.Fatal("expected job to be running")
	}
	if !s.StopAndWait("job1") {
		t.Fatal("expected StopAndWait to report true")
	}
	if s.Running("job1") {
		t.Fatal("expected job to be removed after stop")
	}
	if s.Stop("job1") {
		t.Fatal("expected second Stop to report false")
	}
}

// TestBackoffResetsAfterHealthyRun is the check on the reset-on-success rule:
// a run that lasted >= healthyRun must not keep escalating the backoff. Rather
// than wait a real minute, it asserts on the retry cadence — a job whose runs
// each look healthy should retry at initialBackoff every time, never doubling.
func TestBackoffResetsAfterHealthyRun(t *testing.T) {
	// Shrink the "looks healthy" bar for the test; restore after.
	orig := healthyRun
	healthyRun = 20 * time.Millisecond
	defer func() { healthyRun = orig }()

	s := NewSupervisor()
	starts := make(chan time.Time, 8)

	run := func(ctx context.Context) error {
		starts <- time.Now()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Millisecond): // > healthyRun: counts as working
		}
		return errors.New("upstream dropped")
	}

	const initial = 10 * time.Millisecond
	if err := s.Start("bus_1", run, initial); err != nil {
		t.Fatal(err)
	}
	defer s.StopAndWait("bus_1")

	var prev time.Time
	for i := 0; i < 4; i++ {
		select {
		case at := <-starts:
			if !prev.IsZero() {
				// Each gap is one healthy run (~30ms) plus the backoff. If
				// the backoff were still doubling, gap 4 would be ~30+80ms.
				if gap := at.Sub(prev); gap > 30*time.Millisecond+4*initial {
					t.Fatalf("attempt %d waited %v: backoff escalated despite a healthy run", i+1, gap)
				}
			}
			prev = at
		case <-time.After(2 * time.Second):
			t.Fatalf("attempt %d never started", i+1)
		}
	}
}

// TestStatusReportsStreaming pins the fix for the misleading-status bug: a job
// mid-run must report Streaming, even while LastErr still holds an older
// failure.
func TestStatusReportsStreaming(t *testing.T) {
	s := NewSupervisor()
	entered := make(chan struct{}, 2)
	attempt := 0

	run := func(ctx context.Context) error {
		attempt++
		if attempt == 1 {
			return errors.New("first attempt failed")
		}
		entered <- struct{}{}
		<-ctx.Done() // stay "streaming" until stopped
		return ctx.Err()
	}

	if err := s.Start("bus_1", run, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	defer s.StopAndWait("bus_1")

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("second attempt never started")
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("want 1 status, got %d", len(list))
	}
	if !list[0].Streaming {
		t.Error("job is mid-run but Streaming is false — status still lies")
	}
	if list[0].LastErr == "" {
		t.Error("want the prior failure preserved in LastErr as history")
	}
}
