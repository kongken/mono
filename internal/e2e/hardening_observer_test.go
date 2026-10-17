package e2e_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kongken/mono/internal/platform/rpcauth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type downstreamSpy struct {
	mu     sync.Mutex
	events []downstreamEvent
}

type downstreamEvent struct {
	method   string
	call     rpcauth.Call
	deadline time.Duration
}

func (s *downstreamSpy) interceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		ev := downstreamEvent{method: info.FullMethod, call: rpcauth.CallFrom(ctx)}
		if d, ok := ctx.Deadline(); ok {
			ev.deadline = time.Until(d)
		}
		s.mu.Lock()
		s.events = append(s.events, ev)
		s.mu.Unlock()
		return next(ctx, req)
	}
}

func (s *downstreamSpy) lastMethod(method string) (downstreamEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].method == method {
			return s.events[i], true
		}
	}
	return downstreamEvent{}, false
}

func (s *downstreamSpy) countMethod(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.events {
		if e.method == method {
			n++
		}
	}
	return n
}

func failFirstUnavailable(fullMethod string) grpc.UnaryServerInterceptor {
	var n atomic.Int32
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if info.FullMethod == fullMethod && n.Add(1) == 1 {
			return nil, status.Error(codes.Unavailable, "probe down")
		}
		return next(ctx, req)
	}
}
