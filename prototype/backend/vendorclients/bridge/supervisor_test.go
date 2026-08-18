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
