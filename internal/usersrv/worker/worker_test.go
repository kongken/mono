package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLoopRunsReportsReadinessAndStops(t *testing.T) {
	var runs atomic.Int32
	l := New("t", 5*time.Millisecond, func(context.Context) error { runs.Add(1); return nil })
	if err := l.Check(context.Background()); err == nil {
		t.Fatal("a loop that never started is ready")
	}
	l.Start()
	l.Start() // second start is a no-op
	waitFor(t, func() bool { return runs.Load() >= 3 })
	if err := l.Check(context.Background()); err != nil {
		t.Fatalf("running loop: %v", err)
	}
	l.Stop()
	l.Stop()
	if err := l.Check(context.Background()); err == nil {
		t.Fatal("stopped loop is ready")
	}
	n := runs.Load()
	time.Sleep(30 * time.Millisecond)
	if runs.Load() != n {
		t.Fatal("loop ran after Stop")
	}
}

func TestLoopSurvivesFailuresAndPanics(t *testing.T) {
	var runs atomic.Int32
	l := New("t", time.Millisecond, func(context.Context) error {
		switch runs.Add(1) {
		case 1:
			return errors.New("boom")
		case 2:
			panic("boom")
		}
		return nil
	})
	l.Start()
	defer l.Stop()
	waitFor(t, func() bool { return runs.Load() >= 4 })
	if err := l.Check(context.Background()); err != nil {
		t.Fatalf("loop not ready after failures: %v", err)
	}
}

func TestStalledLoopIsNotReady(t *testing.T) {
	l := New("t", time.Hour, func(context.Context) error { return nil })
	l.Start()
	defer l.Stop()
	waitFor(t, func() bool { return l.Check(context.Background()) == nil })
	base := time.Now()
	l.now = func() time.Time { return base.Add(5*time.Hour + 2*time.Minute) }
	if err := l.Check(context.Background()); err == nil {
		t.Fatal("a loop that missed several ticks is ready")
	}
}
