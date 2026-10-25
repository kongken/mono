// Package worker runs usersrv's in-process background loops and reports their
// local state for /readyz. The state is local on purpose: it says whether the
// loop is running and recently ticked, and never calls out to any vendor.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Loop calls run every interval until stopped. It runs once right after Start.
type Loop struct {
	name     string
	interval time.Duration
	run      func(context.Context) error
	now      func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running atomic.Bool
	lastRun atomic.Int64 // unix nanos of the last tick start
}

// New returns a stopped loop.
func New(name string, interval time.Duration, run func(context.Context) error) *Loop {
	return &Loop{name: name, interval: interval, run: run, now: time.Now}
}

// Start launches the loop. It does nothing when the loop already runs.
func (l *Loop) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.done = cancel, make(chan struct{})
	l.running.Store(true)
	l.lastRun.Store(l.now().UnixNano())
	go l.loop(ctx, l.done)
}

func (l *Loop) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer l.running.Store(false)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		l.tick(ctx)
		timer.Reset(l.interval)
	}
}

// tick runs one iteration. A failure or panic is logged and the loop goes on;
// the next interval retries.
func (l *Loop) tick(ctx context.Context) {
	l.lastRun.Store(l.now().UnixNano())
	defer func() {
		if r := recover(); r != nil {
			slog.Error("usersrv worker panic", "worker", l.name, "panic", r)
		}
	}()
	if err := l.run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("usersrv worker run failed", "worker", l.name, "error", err)
	}
}

// Stop ends the loop and waits for the running iteration to return.
func (l *Loop) Stop() {
	l.mu.Lock()
	cancel, done := l.cancel, l.done
	l.cancel, l.done = nil, nil
	l.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Check reports local readiness: the loop runs and ticked within a few
// intervals. It does not look at the outcome of the work.
func (l *Loop) Check(context.Context) error {
	if !l.running.Load() {
		return errors.New("worker not running")
	}
	last := time.Unix(0, l.lastRun.Load())
	if l.now().Sub(last) > 3*l.interval+time.Minute {
		return errors.New("worker stalled")
	}
	return nil
}
