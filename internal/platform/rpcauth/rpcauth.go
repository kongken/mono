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
	// MetadataTripmapSchema and MetadataTripmapRegions carry the browser client's
	// declared footprint schema and region catalog version from the BFF.
	MetadataTripmapSchema  = "x-tripmap-schema"
	MetadataTripmapRegions = "x-tripmap-regions"
	// MetadataChannel and MetadataTokenID say how an administrator reached the
	// BFF (the browser or an MCP management token) for audit records. A service
	// reads them only after the credential verifies.
	MetadataChannel = "x-bff-channel"
	MetadataTokenID = "x-bff-token-id"
)

// The values of Call.Channel.
const (
	ChannelWeb = "web"
	ChannelMCP = "mcp"
)

// Call is what a handler may know about the caller of the current request.
type Call struct {
	// UserID is empty for anonymous calls.
	UserID string
	// Roles are the role names the BFF verified for UserID. On the service side
	// they are readable only after the credential verified; on the BFF side they
	// are signed into the credential only for methods that declare required_role.
	Roles []string
	// ClientIP is the end client's address as resolved by the BFF; empty if absent.
	ClientIP  string
	RequestID string
	// TraceParent is the W3C traceparent the BFF sent; empty if absent.
	TraceParent string
	// TripmapSchema and TripmapRegions are forwarded from the browser when set.
	TripmapSchema  string
	TripmapRegions string
	// Channel is ChannelWeb or ChannelMCP; empty on the BFF side means web. A
	// service always sees one of the two.
	Channel string
	// TokenID identifies the management token behind an MCP call; empty otherwise.
	TokenID string
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

// RequiredRole returns the role proto declares for fullMethod through the
// required_role option, or "" when the method needs no role. Unknown methods
// return "" here and are rejected by the anonymous policy instead.
func RequiredRole(fullMethod string) string {
	service, method, ok := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
	if !ok || strings.Contains(method, "/") {
		return ""
	}
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service + "." + method))
	if err != nil {
		return ""
	}
	md, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return ""
	}
	opts, _ := md.Options().(*descriptorpb.MethodOptions)
	if opts == nil {
		return ""
	}
	role, _ := proto.GetExtension(opts, platformv1.E_RequiredRole).(string)
	return role
}

// HasRole reports whether roles contains role.
func HasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// NormalizeChannel returns ChannelMCP or ChannelWeb; anything unknown is web.
func NormalizeChannel(c string) string {
	if c == ChannelMCP {
		return ChannelMCP
	}
	return ChannelWeb
}

// NormalizeTokenID keeps a token ID only when it is short and made of safe
// characters, like a request ID; otherwise it returns "".
func NormalizeTokenID(id string) string {
	if requestIDPattern.MatchString(id) {
		return id
	}
	return ""
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

func NormalizeTraceparent(tp string) string {
	tp = strings.TrimSpace(tp)
	if traceparentPattern.MatchString(tp) {
		return tp
	}
	return ""
}

// newTraceparent starts a trace when no tracing is configured, so a call can
// still be correlated across the BFF and the service logs.
func newTraceparent() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "00-" + hex.EncodeToString(b[:16]) + "-" + hex.EncodeToString(b[16:]) + "-00"
}
