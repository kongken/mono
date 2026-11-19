package accesstoken

import (
	"net/http"
	"testing"
)

func TestOriginsPickOneSurfaceOnlyFromOneExactOrigin(t *testing.T) {
	o := Origins{OzID: "https://id.example.com", Console: "https://admin.example.com"}
	header := func(values ...string) http.Header {
		h := http.Header{}
		for _, v := range values {
			h.Add("Origin", v)
		}
		return h
	}
	for _, c := range []struct {
		name   string
		h      http.Header
		want   surface
		wantRo string
	}{
		{"user center", header("https://id.example.com"), surfaceSelfService, ""},
		{"console", header("https://admin.example.com"), surfaceConsole, RoleMetroAdmin},
		{"product", header("https://trip.example.com"), surfaceNone, ""},
		{"no origin", header(), surfaceNone, ""},
		{"empty origin", header(""), surfaceNone, ""},
		{"two origins", header("https://id.example.com", "https://admin.example.com"), surfaceNone, ""},
		{"trailing slash", header("https://admin.example.com/"), surfaceNone, ""},
		{"other scheme", header("http://admin.example.com"), surfaceNone, ""},
	} {
		if got := o.surface(c.h); got != c.want {
			t.Errorf("%s: surface %v, want %v", c.name, got, c.want)
		}
		if got := o.RequiredRole(c.h); got != c.wantRo {
			t.Errorf("%s: required role %q, want %q", c.name, got, c.wantRo)
		}
	}
	// An unregistered console or user center never matches an empty Origin.
	none := Origins{}
	if none.surface(header("")) != surfaceNone || none.surface(header("https://admin.example.com")) != surfaceNone {
		t.Fatal("an empty registration must not admit anything")
	}
}
