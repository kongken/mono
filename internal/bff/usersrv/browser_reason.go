package usersrv

import (
	"connectrpc.com/connect"
	"errors"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// Preserve fixed domain reasons across the internal/browser boundary, without
// copying provider text or metadata. Callers must never classify prose.
func browserReason(err *connect.Error, reason string) *connect.Error {
	if reason != "" {
		if d, e := connect.NewErrorDetail(&errdetails.ErrorInfo{Domain: errorDomain, Reason: reason}); e == nil {
			err.AddDetail(d)
		}
	}
	return err
}
func BrowserReason(err error) string {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return ""
	}
	for _, d := range ce.Details() {
		v, e := d.Value()
		if e == nil {
			if i, ok := v.(*errdetails.ErrorInfo); ok && i.GetDomain() == errorDomain {
				return i.GetReason()
			}
		}
	}
	return ""
}
