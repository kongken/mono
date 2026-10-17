package tiles

import (
	"net/http"
	"strings"
)

const (
	maxValidatorHeader = 2048
	maxEntityTags      = 8
	maxEntityTagLength = 256
)

// entityTag is an HTTP entity tag in the form Mapbox issued it: opaque is what sits
// between the quotes.
type entityTag struct {
	weak   bool
	opaque string
}

func (e entityTag) String() string {
	if e.weak {
		return `W/"` + e.opaque + `"`
	}
	return `"` + e.opaque + `"`
}

// parseEntityTag reads one entity tag. Anything that is not a plain quoted string
// of visible characters is refused rather than guessed at.
func parseEntityTag(s string) (entityTag, bool) {
	s = strings.TrimSpace(s)
	var tag entityTag
	if rest, ok := strings.CutPrefix(s, "W/"); ok {
		tag.weak, s = true, rest
	}
	if len(s) < 2 || len(s) > maxEntityTagLength+2 || s[0] != '"' || s[len(s)-1] != '"' {
		return entityTag{}, false
	}
	tag.opaque = s[1 : len(s)-1]
	for i := 0; i < len(tag.opaque); i++ {
		if c := tag.opaque[i]; c <= ' ' || c == '"' || c == 0x7f {
			return entityTag{}, false
		}
	}
	return tag, true
}

// scopeETag marks an upstream tag with the style it belongs to. The browser only ever
// holds scoped tags; this is what keeps a validator from one style out of another.
func (h *Handler) scopeETag(e entityTag) string {
	return entityTag{weak: e.weak, opaque: h.fingerprint + "-" + e.opaque}.String()
}

// conditionalHeaders are the validators that may go to Mapbox for one request.
type conditionalHeaders struct {
	tags          []entityTag
	modifiedSince string
}

func (c conditionalHeaders) sent() bool { return len(c.tags) > 0 || c.modifiedSince != "" }

func (c conditionalHeaders) apply(h http.Header) {
	if len(c.tags) > 0 {
		parts := make([]string, len(c.tags))
		for i, tag := range c.tags {
			parts[i] = tag.String()
		}
		h.Set("If-None-Match", strings.Join(parts, ", "))
	}
	if c.modifiedSince != "" {
		h.Set("If-Modified-Since", c.modifiedSince)
	}
}

// conditional derives what to ask Mapbox from the browser's validators. Only entity
// tags scoped to the current style are passed on, unscoped; If-Modified-Since is used
// only when the browser sent no If-None-Match (which takes precedence), as a valid
// HTTP date. A request whose validators do not belong to this style is answered in
// full.
func (h *Handler) conditional(in http.Header) conditionalHeaders {
	if values := in.Values("If-None-Match"); len(values) > 0 {
		return conditionalHeaders{tags: h.ownTags(strings.Join(values, ","))}
	}
	if values := in.Values("If-Modified-Since"); len(values) == 1 {
		if since, err := http.ParseTime(values[0]); err == nil {
			return conditionalHeaders{modifiedSince: since.UTC().Format(http.TimeFormat)}
		}
	}
	return conditionalHeaders{}
}

// ownTags returns the tags in an If-None-Match value that this style issued, with the
// scope removed. A malformed list, "*", or an oversized header yields nothing.
func (h *Handler) ownTags(value string) []entityTag {
	if len(value) > maxValidatorHeader {
		return nil
	}
	prefix := h.fingerprint + "-"
	var tags []entityTag
	for rest := strings.TrimSpace(value); rest != ""; {
		end := closingQuote(rest)
		if end < 0 {
			return nil
		}
		tag, ok := parseEntityTag(rest[:end+1])
		if !ok {
			return nil
		}
		if inner, own := strings.CutPrefix(tag.opaque, prefix); own && inner != "" && len(tags) < maxEntityTags {
			tags = append(tags, entityTag{weak: tag.weak, opaque: inner})
		}
		rest = strings.TrimLeft(rest[end+1:], " \t")
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return nil
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return tags
}

// closingQuote finds the quote that ends the entity tag at the start of s, or -1.
func closingQuote(s string) int {
	start := strings.IndexByte(s, '"')
	if start < 0 || (start != 0 && s[:start] != "W/") {
		return -1
	}
	end := strings.IndexByte(s[start+1:], '"')
	if end < 0 {
		return -1
	}
	return start + 1 + end
}
