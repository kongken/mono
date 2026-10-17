// Package grpcx runs a business service's native gRPC server: it owns the TLS
// listener, the message limits and the graceful shutdown, instead of using a
// fixed-port, plaintext server.
package grpcx

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

// DefaultMaxMessageBytes leaves room above the 16 MiB legitimate footprint and
// media requests for protobuf framing, instead of relying on gRPC's 4 MiB default.
const DefaultMaxMessageBytes = 20 * 1024 * 1024

// ServerConfig is the gRPC listener section of a service's configuration.
type ServerConfig struct {
	// Listen is the internal address, for example :18083.
	Listen   string `yaml:"listen"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// MaxMessageBytes caps one request or response; default DefaultMaxMessageBytes.
	MaxMessageBytes int `yaml:"max_message_bytes"`
}

func (c ServerConfig) Validate() error {
	if c.Listen == "" || c.CertFile == "" || c.KeyFile == "" {
		return errors.New("grpc requires listen, cert_file and key_file")
	}
	if c.MaxMessageBytes < 0 {
		return errors.New("grpc.max_message_bytes must not be negative")
	}
	return nil
}

func (c ServerConfig) maxBytes() int {
	if c.MaxMessageBytes > 0 {
		return c.MaxMessageBytes
	}
	return DefaultMaxMessageBytes
}

// Server is a started gRPC server.
type Server struct {
	srv *grpc.Server
	ln  net.Listener
}

// Start listens with TLS and serves in the background. register adds services
// before serving begins.
func Start(cfg ServerConfig, register func(grpc.ServiceRegistrar), unary []grpc.UnaryServerInterceptor, stream []grpc.StreamServerInterceptor) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("grpc tls: %w", err)
	}
	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	srv := grpc.NewServer(
		grpc.Creds(creds),
		grpc.MaxRecvMsgSize(cfg.maxBytes()), grpc.MaxSendMsgSize(cfg.maxBytes()),
		grpc.ChainUnaryInterceptor(unary...), grpc.ChainStreamInterceptor(stream...),
		grpc.KeepaliveParams(keepalive.ServerParameters{MaxConnectionIdle: 5 * time.Minute}),
	)
	register(srv)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			slog.Error("grpc server stopped", "error", err)
		}
	}()
	return &Server{srv: srv, ln: ln}, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Shutdown stops accepting calls and waits for running ones up to timeout, then
// cuts them off.
func (s *Server) Shutdown(timeout time.Duration) {
	done := make(chan struct{})
	go func() { s.srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		s.srv.Stop()
		<-done
	}
}
