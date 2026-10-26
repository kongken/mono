// Package navpath holds the canonical return-pathname rule shared by the BFF
// navigation registry and the return context usersrv stores with a mail challenge.
// Neither side may accept a path the other would refuse.
package navpath

import (
	"errors"
	"fmt"
	"strings"
)

// MaxLen is the longest accepted pathname in bytes.
const MaxLen = 256

// Check reports whether p is a canonical return pathname: it starts with "/", is at
// most 256 bytes and uses only ASCII letters, digits and "-", ".", "_", "~", "/". That
// excludes by construction a scheme, host, "//", backslash, control characters,
// percent-encoding (so "%2e%2e" and "%2f" tricks), query and fragment; "." and ".."
// segments are refused as well.
func Check(p string) error {
	if p == "" || p[0] != '/' {
		return errors.New("path must start with /")
	}
	if len(p) > MaxLen {
		return fmt.Errorf("path is longer than %d bytes", MaxLen)
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '.' || c == '_' || c == '~' || c == '/'
		if !ok {
			return errors.New("path may only contain letters, digits and -._~/")
		}
	}
	if strings.Contains(p, "//") {
		return errors.New("path must not contain //")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return errors.New("path must not contain . or .. segments")
		}
	}
	return nil
}
