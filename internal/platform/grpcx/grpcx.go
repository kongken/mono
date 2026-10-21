// Package grpcx runs a business service's native gRPC server: it owns the
// listener (TLS by default, h2c only when explicitly configured), the message
// limits and the graceful shutdown, instead of using a fixed-port, plaintext
// server.
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// LegitimatePayloadBytes is the largest request body the product allows on the
// public edge (footprint batches, media). Connect handlers should use this for
// read limits; proxies may enforce the same cap.
const LegitimatePayloadBytes = 16 * 1024 * 1024

// DefaultMaxMessageBytes leaves room above LegitimatePayloadBytes for protobuf
// framing on internal gRPC, instead of relying on gRPC's 4 MiB default.
const DefaultMaxMessageBytes = 20 * 1024 * 1024

// Transports for internal gRPC. TLS is the default; h2c (HTTP/2 cleartext with
// prior knowledge) must be chosen explicitly and is never inferred from missing
// certificates or a failed handshake.
const (
	TransportTLS = "tls"
	TransportH2C = "h2c"
)

// NormalizeTransport maps an unset transport to TLS and rejects anything that
// is not exactly "tls" or "h2c".
func NormalizeTransport(t string) (string, error) {
	switch t {
	case "", TransportTLS:
		return TransportTLS, nil
	case TransportH2C:
		return TransportH2C, nil
	}
	return "", fmt.Errorf("unknown grpc transport %q (want %q or %q)", t, TransportTLS, TransportH2C)
}

// ServerConfig is the gRPC listener section of a service's configuration.
type ServerConfig struct {
	// Transport is "tls" (default) or "h2c". h2c serves unencrypted HTTP/2 and
	// is only for an explicitly approved trusted internal network; it gives no
	// server authentication, encryption or integrity. Call credentials are
	// verified in both modes.
	Transport string `yaml:"transport"`
	// Listen is the internal address, for example :18083.
	Listen string `yaml:"listen"`
	// CertFile and KeyFile are required for tls and must be empty for h2c.
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// MaxMessageBytes caps one request or response; default DefaultMaxMessageBytes.
	MaxMessageBytes int `yaml:"max_message_bytes"`
}

func (c ServerConfig) Validate() error {
	transport, err := NormalizeTransport(c.Transport)
	if err != nil {
		return fmt.Errorf("grpc.transport: %w", err)
	}
	if c.Listen == "" {
		return errors.New("grpc requires listen")
	}
	switch transport {
	case TransportTLS:
		if c.CertFile == "" || c.KeyFile == "" {
			return errors.New("grpc transport tls requires cert_file and key_file")
		}
	case TransportH2C:
		if c.CertFile != "" || c.KeyFile != "" {
			return errors.New("grpc transport h2c is unencrypted: remove cert_file and key_file, or set transport to tls")
		}
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
	srv       *grpc.Server
	ln        net.Listener
	transport string
}

// Start listens (TLS unless the config selects h2c) and serves in the
// background. register adds services before serving begins.
func Start(cfg ServerConfig, register func(grpc.ServiceRegistrar), unary []grpc.UnaryServerInterceptor, stream []grpc.StreamServerInterceptor) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	transport, _ := NormalizeTransport(cfg.Transport) // Validate accepted it
	var creds credentials.TransportCredentials
	if transport == TransportH2C {
		creds = insecure.NewCredentials()
	} else {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("grpc tls: %w", err)
		}
		creds = credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	}
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
	if transport == TransportH2C {
		slog.Warn("grpc listening without TLS (h2c); restrict it to a trusted internal network", "listen", ln.Addr().String())
	}
	return &Server{srv: srv, ln: ln, transport: transport}, nil
}

// Transport is the transport the server was started with ("tls" or "h2c").
func (s *Server) Transport() string { return s.transport }

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
