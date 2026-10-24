package identity

import (
	"strings"
	"testing"
)

func TestNormalizeEmailMergesOnlyCaseAndOuterSpace(t *testing.T) {
	a, err := NormalizeEmail(" Alice+tag@Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeEmail("alice+tag@example.com")
	if err != nil || a != b || a != "alice+tag@example.com" {
		t.Fatalf("got %q %q %v", a, b, err)
	}
	// Spec scenario 1: no tag stripping, no dot folding, no alias guessing.
	seen := map[string]string{}
	for _, in := range []string{"alice@example.com", "a.lice@example.com", "alice+tag@example.com", "alice@example.org", "alice@mail.example.com"} {
		got, err := NormalizeEmail(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if prev, dup := seen[got]; dup {
			t.Fatalf("%q and %q were merged into %q", prev, in, got)
		}
		seen[got] = in
	}
	if got, _ := NormalizeEmail("\tA@XN--FSQ.example\t"); got != "a@xn--fsq.example" {
		t.Fatalf("punycode domain: %q", got)
	}
}

func TestNormalizeEmailRejectsEverythingElse(t *testing.T) {
	long64 := strings.Repeat("a", 64)
	bad := map[string]string{
		"empty":                "",
		"spaces only":          "   ",
		"no at":                "alice.example.com",
		"two at":               "a@b@example.com",
		"two addresses":        "a@example.com,b@example.com",
		"two addresses spaced": "a@example.com b@example.com",
		"display name":         "Alice <a@example.com>",
		"comment":              "alice(comment)@example.com",
		"quoted local":         `"alice smith"@example.com`,
		"crlf trailing":        "a@example.com\r\n",
		"crlf injection":       "a@example.com\r\nBcc: x@example.com",
		"lf only":              "a@example.com\nb",
		"nul":                  "a\x00@example.com",
		"tab inside":           "a\t@example.com",
		"idn domain":           "a@例え.jp",
		"idn local":            "用户@example.com",
		"latin1 local":         "é@example.com",
		"ip literal":           "a@[127.0.0.1]",
		"bare ip":              "a@127.0.0.1",
		"single label":         "a@localhost",
		"leading dot local":    ".a@example.com",
		"trailing dot local":   "a.@example.com",
		"double dot local":     "a..b@example.com",
		"empty local":          "@example.com",
		"empty domain":         "a@",
		"leading hyphen label": "a@-example.com",
		"trailing hyphen":      "a@example-.com",
		"empty label":          "a@example..com",
		"trailing dot domain":  "a@example.com.",
		"underscore domain":    "a@exa_mple.com",
		"local too long":       long64 + "a@example.com",
		"label too long":       "a@" + strings.Repeat("a", 64) + ".com",
		"total too long":       long64 + "@" + strings.Repeat(strings.Repeat("b", 63)+".", 3) + "com",
		"huge":                 strings.Repeat("a", 5000) + "@example.com",
	}
	for name, in := range bad {
		if got, err := NormalizeEmail(in); err == nil {
			t.Errorf("%s: %q accepted as %q", name, in, got)
		} else if v, ok := IsValidation(err); !ok || v.Field != "email" {
			t.Errorf("%s: not an email validation error: %v", name, err)
		} else if strings.Contains(v.Error(), "example") {
			t.Errorf("%s: error echoes the input: %v", name, v)
		}
	}
	if _, err := NormalizeEmail(long64 + "@example.com"); err != nil {
		t.Fatalf("64-byte local part must be accepted: %v", err)
	}
	// 254 bytes in total is the limit: 64 + 1 + 189.
	domain := strings.Repeat("b", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("b", 57) + ".com"
	if n := len(long64) + 1 + len(domain); n != 254 {
		t.Fatalf("test address is %d bytes", n)
	}
	if _, err := NormalizeEmail(long64 + "@" + domain); err != nil {
		t.Fatalf("254-byte address rejected: %v", err)
	}
	if _, err := NormalizeEmail(long64 + "@x." + domain); err == nil {
		t.Fatal("255-byte address accepted")
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{"  小明  ": "小明", "A": "A", strings.Repeat("字", 40): strings.Repeat("字", 40), "😀😀": "😀😀"} {
		if got, err := NormalizeDisplayName(in, false); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if got, err := NormalizeDisplayName("   ", true); err != nil || got != DefaultDisplayName {
		t.Fatalf("default: %q %v", got, err)
	}
	for _, in := range []string{"", "   ", strings.Repeat("字", 41), "a\x00b", "a\nb", "a\u2028b", "\xff\xfe", "a\u0085b"} {
		if _, err := NormalizeDisplayName(in, false); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestAvatarURL(t *testing.T) {
	for _, in := range []string{"", "https://cdn.example.com/a.png", "https://example.com:8443/a?x=1", "HTTPS://example.com/a.png"} {
		if _, err := NormalizeAvatarURL(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, in := range []string{
		"http://example.com/a.png", "https://user:pw@example.com/a.png", "https://user@example.com/a.png", "//example.com/a.png",
		"javascript:alert(1)", "data:image/png;base64,AAAA", "https:///a.png", "https://", "ftp://example.com/a",
		"https://example.com/a b", "https://example.com/\n", "https://example.com/é", "https://example.com/a#frag",
		"https://example.com/" + strings.Repeat("a", 500), "example.com/a.png", "https:example.com",
	} {
		if _, err := NormalizeAvatarURL(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	if _, err := NormalizeAvatarURL("https://example.com/" + strings.Repeat("a", 480)); err != nil {
		t.Errorf("under 500 bytes rejected: %v", err)
	}
}

func TestPasswordPolicy(t *testing.T) {
	ok := []string{
		strings.Repeat("a", 12),
		"  leading and trailing spaces kept  ",
		strings.Repeat("密", 12),
		strings.Repeat("🙂", 12),
		strings.Repeat("a", 128),
		"alllowercasenodigits",
	}
	for _, p := range ok {
		if err := ValidatePassword("password", p); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	bad := []string{
		"", strings.Repeat("a", 11), strings.Repeat("密", 11), strings.Repeat("a", 129),
		strings.Repeat("🙂", 129), strings.Repeat("a", 513), "abcdefghijk\xff", strings.Repeat("密", 129),
	}
	for _, p := range bad {
		if err := ValidatePassword("password", p); err == nil {
			t.Errorf("%d-byte password accepted", len(p))
		}
	}
	// 128 four-byte code points is 512 bytes: exactly the byte limit.
	if err := ValidatePassword("password", strings.Repeat("🙂", 128)); err != nil {
		t.Errorf("128 code points / 512 bytes rejected: %v", err)
	}
	if err := ValidatePassword("password", strings.Repeat("密", 128)); err != nil {
		t.Errorf("128 three-byte code points rejected: %v", err)
	}
	if err := ValidatePassword("password", strings.Repeat("🙂", 128)+"a"); err == nil {
		t.Error("over 512 bytes accepted")
	}
}

func TestCheckPasswordInputOnlyBoundsSize(t *testing.T) {
	if err := CheckPasswordInput("password", "short"); err != nil {
		t.Fatalf("short comparison input rejected: %v", err)
	}
	for _, p := range []string{"", strings.Repeat("a", 513), strings.Repeat("a", 129), "\xff"} {
		if err := CheckPasswordInput("password", p); err == nil {
			t.Errorf("%d-byte comparison input accepted", len(p))
		}
	}
}

func TestMutationID(t *testing.T) {
	for _, in := range []string{"m-1", "A.b_c-9", strings.Repeat("x", 64)} {
		if _, err := NormalizeMutationID(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, in := range []string{"", strings.Repeat("x", 65), "a b", "a/b", "é", "a\n"} {
		if _, err := NormalizeMutationID(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestTokenDigestOnlyForIssuedShape(t *testing.T) {
	tok := strings.Repeat("A", 43)
	d, ok := TokenDigest(tok)
	if !ok || len(d) != 32 {
		t.Fatalf("%x %v", d, ok)
	}
	if d2, _ := TokenDigest(tok); string(d2) != string(d) {
		t.Fatal("digest is not deterministic")
	}
	for _, bad := range []string{"", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("A", 42) + "=", strings.Repeat("A", 42) + "+", strings.Repeat("A", 42) + "\n", strings.Repeat("é", 43)} {
		if _, ok := TokenDigest(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
