// Package bridge supervises long-running jobs that pull video from a vendor
// source (Castmaster FLV/HLS URL, or an N9M media channel) and republish it
// into MediaMTX under this project's "{busID}_{cam}" path convention, so the
// existing fleet/api handlers pick it up with no changes.
package bridge

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RunFunc performs one attempt of a job and blocks until it exits (success,
// failure, or ctx cancellation). It must return promptly after ctx is
// canceled.
type RunFunc func(ctx context.Context) error

type job struct {
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	lastErr  error
	attempts int
}

// Supervisor runs a set of named RunFuncs, restarting each with backoff until
// explicitly stopped.
type Supervisor struct {
	mu   sync.Mutex
	jobs map[string]*job
}

// NewSupervisor returns an empty Supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{jobs: make(map[string]*job)}
}

// ErrAlreadyRunning is returned by Start when key is already active.
type ErrAlreadyRunning struct{ Key string }

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("bridge: job %q already running", e.Key)
}

// maxBackoff caps the exponential backoff below so a permanently failing
// job (e.g. a vendor channel that isn't wired to a real camera) settles
// down to one attempt every 5 minutes rather than hammering the vendor
// forever at the initial rate. There's no reset-on-success heuristic here:
// a vendor timeout (itself often 30s+) is indistinguishable by duration
// alone from a genuine connect, so backoff only ever grows for a job's
// lifetime — cheap insurance against hammering a vendor, at the cost of a
// flaky-but-working stream retrying slower after its first drop.
const maxBackoff = 5 * time.Minute

// giveUpAfterMaxouts / giveUpCooldown: some vendor devices have no way to
// release a session once opened (Chemito's API has no stop/close call), so
// every retry of a permanently-broken channel (e.g. an unwired camera slot)
// leaves another phantom session open server-side — even at maxBackoff's
// slow rate, that adds up over a long-lived process. After the backoff has
// maxed out this many times in a row (a strong signal the job is never
// going to succeed, not just having a bad minute), fall back hard to
// giveUpCooldown before giving it one more fast-retry chance.
const giveUpAfterMaxouts = 3
const giveUpCooldown = 1 * time.Hour

// Start launches run under key, restarting it after an exponentially
// growing backoff (starting at initialBackoff, doubling per attempt up to
// maxBackoff) whenever it returns a non-nil error, until Stop(key) is
// called. It returns ErrAlreadyRunning if key is already active.
func (s *Supervisor) Start(key string, run RunFunc, initialBackoff time.Duration) error {
	s.mu.Lock()
	if _, exists := s.jobs[key]; exists {
		s.mu.Unlock()
		return &ErrAlreadyRunning{Key: key}
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{cancel: cancel, done: make(chan struct{})}
	s.jobs[key] = j
	s.mu.Unlock()

	go func() {
		defer close(j.done)
		backoff := initialBackoff
		consecutiveMaxouts := 0
		for {
			err := run(ctx)
			j.mu.Lock()
			j.lastErr = err
			j.attempts++
			j.mu.Unlock()

			if ctx.Err() != nil {
				return
			}

			sleepFor := backoff
			if backoff >= maxBackoff {
				consecutiveMaxouts++
				if consecutiveMaxouts >= giveUpAfterMaxouts {
					sleepFor = giveUpCooldown
					consecutiveMaxouts = 0
					backoff = initialBackoff // fresh, fast chance after the cooldown
				}
			} else {
				consecutiveMaxouts = 0
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepFor):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}()
	return nil
}

// Stop cancels and removes the job registered under key. It reports whether
// a job was found. Stop does not wait for the job's current attempt to
// finish exiting; use StopAndWait if that matters.
func (s *Supervisor) Stop(key string) bool {
	s.mu.Lock()
	j, ok := s.jobs[key]
	if ok {
		delete(s.jobs, key)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	j.cancel()
	return true
}

// StopAndWait is like Stop but blocks until the job's goroutine has fully
// exited (its current RunFunc invocation has returned).
func (s *Supervisor) StopAndWait(key string) bool {
	s.mu.Lock()
	j, ok := s.jobs[key]
	if ok {
		delete(s.jobs, key)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	j.cancel()
	<-j.done
	return true
}

// Running reports whether key is currently registered (it may be between
// restart attempts).
func (s *Supervisor) Running(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.jobs[key]
	return ok
}

// Status summarizes one job's state for observability endpoints.
type Status struct {
	Key      string `json:"key"`
	Attempts int    `json:"attempts"`
	LastErr  string `json:"lastError,omitempty"`
}

// List returns a status snapshot for every active job, ordered arbitrarily.
func (s *Supervisor) List() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.jobs))
	for key, j := range s.jobs {
		j.mu.Lock()
		st := Status{Key: key, Attempts: j.attempts}
		if j.lastErr != nil {
			st.LastErr = j.lastErr.Error()
		}
		j.mu.Unlock()
		out = append(out, st)
	}
	return out
}
