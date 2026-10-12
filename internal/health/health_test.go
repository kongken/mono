package health

import (
	"context"
	"errors"
	"testing"
)

func TestReadiness(t *testing.T) {
	checker := New(Probe{Name: "postgres", Check: func(context.Context) error { return nil }}, Probe{Name: "redis", Check: func(context.Context) error { return errors.New("redis unavailable: secret should never leave probe") }})
	statuses := checker.Check(context.Background())
	if Ready(statuses) || len(statuses) != 2 || !statuses[0].Ready || statuses[1].Ready {
		t.Fatalf("unexpected readiness: %+v", statuses)
	}
	if Ready(nil) {
		t.Fatal("empty check cannot be ready")
	}
}
func TestCanceledCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	statuses := New(Probe{Name: "s3", Check: func(ctx context.Context) error { return ctx.Err() }}).Check(ctx)
	if Ready(statuses) {
		t.Fatal("canceled probe reported ready")
	}
}
