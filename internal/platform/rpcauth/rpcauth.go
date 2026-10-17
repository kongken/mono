// Package rpcauth is the trust boundary of internal gRPC calls. The BFF's client
// interceptor attaches a signed credential plus a fixed set of metadata; a
// service's server interceptor verifies the credential, enforces the anonymous
// policy declared in proto, and exposes only the verified facts to handlers.
//
// Metadata is a closed list. Anything else a caller sends, including headers
// that look like identity (x-user-id and the like), is never read.
package rpcauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/netip"
	"regexp"
	"strings"

	platformv1 "github.com/kongken/mono/pkg/proto/platform/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The metadata keys the BFF writes and a service reads. Nothing else is trusted.
const (
	MetadataAuthorization = "authorization"
	MetadataRequestID     = "x-request-id"
	MetadataTraceparent   = "traceparent"
	// MetadataClientIP carries the client address the BFF resolved from its trusted
	// proxy configuration. A service believes it only after the credential verifies.
	MetadataClientIP = "x-bff-client-ip"
)

// Call is what a handler may know about the caller of the current request.
type Call struct {
	// UserID is empty for anonymous calls.
	UserID string
	// ClientIP is the end client's address as resolved by the BFF; empty if absent.
	ClientIP  string
	RequestID string
	// TraceParent is the W3C traceparent the BFF sent; empty if absent.
	TraceParent string
}

type callKey struct{}

// WithCall stores the caller facts in ctx. The BFF uses it to describe the
// outgoing call; the server interceptor uses it for the verified incoming call.
func WithCall(ctx context.Context, c Call) context.Context {
	return context.WithValue(ctx, callKey{}, c)
}

// CallFrom returns the caller facts, or the zero Call.
func CallFrom(ctx context.Context) Call {
	c, _ := ctx.Value(callKey{}).(Call)
	return c
}

// UserIDFrom returns the signed-in user of the request, if any.
func UserIDFrom(ctx context.Context) (string, bool) {
	c := CallFrom(ctx)
	return c.UserID, c.UserID != ""
}

// Anonymous reports whether the proto declares fullMethod (such as
// /tripmap.v1.SystemService/GetStatus) callable without a signed-in user.
// Unknown methods are protected.
func Anonymous(fullMethod string) bool {
	service, method, ok := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	if !ok || strings.Contains(method, "/") {
		return false
	}
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service + "." + method))
	if err != nil {
		return false
	}
	md, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return false
	}
	opts, _ := md.Options().(*descriptorpb.MethodOptions)
	if opts == nil {
		return false
	}
	anonymous, _ := proto.GetExtension(opts, platformv1.E_Anonymous).(bool)
	return anonymous
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// NormalizeRequestID keeps a caller-supplied request ID only when it is short
// and made of safe characters, so it can go into logs and headers unescaped;
// otherwise it returns a fresh one.
func NormalizeRequestID(id string) string {
	if requestIDPattern.MatchString(id) {
		return id
	}
	return NewRequestID()
}

// NewRequestID returns a random 32-character hex ID.
func NewRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// validIP returns the canonical form of a textual address, or "".
func validIP(s string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return addr.Unmap().String()
}

var traceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// newTraceparent starts a trace when no tracing is configured, so a call can
// still be correlated across the BFF and the service logs.
func newTraceparent() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "00-" + hex.EncodeToString(b[:16]) + "-" + hex.EncodeToString(b[16:]) + "-00"
}
