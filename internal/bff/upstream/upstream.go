// Package upstream holds the BFF's gRPC connections to the business services:
// per-service transport (TLS verified against a configured certificate authority
// by default, h2c only when a service explicitly selects it), a fresh signed
// credential on every call, and no automatic retries.
package upstream

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/platform/grpcx"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/platform/servicetoken"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Clients are the open connections, keyed by service name.
type Clients struct{ conns map[string]*grpc.ClientConn }

// Dial prepares a connection for each named service. Connections are opened
// lazily, so the BFF starts before its services are up.
func Dial(cfg *config.Config, services []string) (*Clients, error) {
	key, err := servicetoken.ReadPrivateKey(cfg.InternalAuth.PrivateKey, cfg.InternalAuth.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("internal_auth private key: %w", err)
	}
	signer := &servicetoken.Signer{Issuer: cfg.InternalAuth.Issuer, KID: cfg.InternalAuth.KID, Key: key}
	// Resolve every transport first: a bad value fails before anything is dialed,
	// and the CA is read only when some service actually uses TLS.
	transports := make(map[string]string, len(services))
	needsCA := false
	for _, name := range services {
		t, err := grpcx.NormalizeTransport(cfg.GRPC.Services[name].Transport)
		if err != nil {
			return nil, fmt.Errorf("grpc.services.%s.transport: %w", name, err)
		}
		transports[name] = t
		needsCA = needsCA || t == grpcx.TransportTLS
	}
	var pool *x509.CertPool
	if needsCA {
		pem, err := os.ReadFile(cfg.GRPC.CAFile)
		if err != nil {
			return nil, fmt.Errorf("grpc.ca_file: %w", err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("grpc.ca_file holds no certificate")
		}
	}
	limit := cfg.GRPC.MaxMessageBytes
	if limit == 0 {
		limit = grpcx.DefaultMaxMessageBytes
	}
	c := &Clients{conns: map[string]*grpc.ClientConn{}}
	for _, name := range services {
		svc := cfg.GRPC.Services[name]
		// h2c is HTTP/2 prior knowledge over plaintext TCP; the choice is the
		// service's explicit config, never a fallback from a failed handshake.
		creds := insecure.NewCredentials()
		if transports[name] == grpcx.TransportTLS {
			creds = credentials.NewTLS(&tls.Config{RootCAs: pool, ServerName: svc.ServerName, MinVersion: tls.VersionTLS13})
		}
		conn, err := grpc.NewClient(svc.Address,
			grpc.WithTransportCredentials(creds),
			grpc.WithChainUnaryInterceptor(rpcauth.UnaryClientInterceptor(signer, name)),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(limit), grpc.MaxCallSendMsgSize(limit)),
			// Retrying is not enabled until a method is explicitly known to be safe.
			grpc.WithDisableRetry(),
		)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("grpc.services.%s: %w", name, err)
		}
		c.conns[name] = conn
		slog.Info("grpc upstream", "service", name, "transport", transports[name])
	}
	return c, nil
}

// Conn returns the connection to a service, or nil.
func (c *Clients) Conn(name string) *grpc.ClientConn { return c.conns[name] }

// Close closes every connection.
func (c *Clients) Close() error {
	var errs []error
	for _, conn := range c.conns {
		errs = append(errs, conn.Close())
	}
	return errors.Join(errs...)
}
