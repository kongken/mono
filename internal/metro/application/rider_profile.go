package application

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kongken/mono/internal/metro/ent"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// profileDoc is a rider profile's editable fields.
type profileDoc struct {
	Handle       string
	DisplayName  string
	Subtitle     string
	Bio          string
	Location     string
	HeaderImages []string
	Twitter      string
	Instagram    string
	Weibo        string
	Facebook     string
	Github       string
	IsPublic     bool
}

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// reservedHandles cannot be taken: they are routes of the site or names that
// could be mistaken for the operator.
var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "root": true, "me": true, "api": true, "u": true, "user": true, "users": true,
	"login": true, "logout": true, "signin": true, "signup": true, "register": true, "auth": true, "oauth": true,
	"www": true, "static": true, "assets": true, "public": true, "help": true, "support": true, "about": true,
	"settings": true, "profile": true, "metro": true, "kongken": true, "system": true, "null": true, "undefined": true,
	"staff": true, "mod": true, "moderator": true, "official": true,
}

// normalizeHandle is how a handle is compared and stored: trimmed and lowercase.
func normalizeHandle(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func profileFromInput(in *metrov1.ProfileInput) profileDoc {
	return profileDoc{
		Handle: normalizeHandle(in.GetHandle()), DisplayName: in.GetDisplayName(), Subtitle: in.GetSubtitle(), Bio: in.GetBio(),
		Location: in.GetLocation(), HeaderImages: slices.Clone(in.GetHeaderImages()), Twitter: in.GetTwitter(),
		Instagram: in.GetInstagram(), Weibo: in.GetWeibo(), Facebook: in.GetFacebook(), Github: in.GetGithub(),
		IsPublic: in.GetIsPublic(),
	}
}

func profileDocOf(p *ent.RiderProfile) profileDoc {
	d := profileDoc{
		DisplayName: p.DisplayName, Subtitle: p.Subtitle, Bio: p.Bio, Location: p.Location,
		HeaderImages: slices.Clone(p.HeaderImages), Twitter: p.Twitter, Instagram: p.Instagram, Weibo: p.Weibo,
		Facebook: p.Facebook, Github: p.Github, IsPublic: p.IsPublic,
	}
	if p.Handle != nil {
		d.Handle = *p.Handle
	}
	return d
}

func profileInputOf(d profileDoc) *metrov1.ProfileInput {
	return &metrov1.ProfileInput{
		Handle: d.Handle, DisplayName: d.DisplayName, Subtitle: d.Subtitle, Bio: d.Bio, Location: d.Location,
		HeaderImages: slices.Clone(d.HeaderImages), Twitter: d.Twitter, Instagram: d.Instagram, Weibo: d.Weibo,
		Facebook: d.Facebook, Github: d.Github, IsPublic: d.IsPublic,
	}
}

func publicProfileOf(d profileDoc) *metrov1.PublicProfile {
	return &metrov1.PublicProfile{
		Handle: d.Handle, DisplayName: d.DisplayName, Subtitle: d.Subtitle, Bio: d.Bio, Location: d.Location,
		HeaderImages: slices.Clone(d.HeaderImages), Twitter: d.Twitter, Instagram: d.Instagram, Weibo: d.Weibo,
		Facebook: d.Facebook, Github: d.Github,
	}
}

// optionalText checks a text that may be empty: at most max characters and no
// control characters (newlines allowed when multiline), no leading/trailing blanks.
func (l *errList) optionalText(field, s string, max int, multiline bool) {
	switch {
	case !utf8.ValidString(s):
		l.add(field, "must be valid UTF-8")
		return
	case utf8.RuneCountInString(s) > max:
		l.add(field, "must be at most %d characters", max)
		return
	case strings.TrimSpace(s) != s:
		l.add(field, "must not start or end with blanks")
		return
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			l.add(field, "must not contain control characters")
			return
		}
	}
}

// platform describes how one social network's account is written.
type platform struct {
	user  *regexp.Regexp
	hosts []string
}

var platforms = map[string]platform{
	"twitter":   {regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`), []string{"twitter.com", "x.com"}},
	"instagram": {regexp.MustCompile(`^[A-Za-z0-9._]{1,30}$`), []string{"instagram.com"}},
	"weibo":     {regexp.MustCompile(`^[\p{L}\p{N}_-]{1,30}$`), []string{"weibo.com", "weibo.cn"}},
	"facebook":  {regexp.MustCompile(`^[A-Za-z0-9.]{5,50}$`), []string{"facebook.com", "fb.com"}},
	"github":    {regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`), []string{"github.com"}},
}

const maxSocialLength = 200

// social checks one social account: empty, a username valid on that platform, or
// an https link to the platform's own site.
func (l *errList) social(field, v string) {
	if v == "" {
		return
	}
	p := platforms[field]
	if len(v) > maxSocialLength {
		l.add(field, "must be at most %d characters", maxSocialLength)
		return
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			l.add(field, "must not contain blanks or control characters")
			return
		}
	}
	if !strings.Contains(v, "/") && !strings.Contains(v, ":") {
		if !p.user.MatchString(v) {
			l.add(field, "is not a valid %s username (or give an https link)", field)
		}
		return
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		l.add(field, "must be a username or an https link")
		return
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	for _, h := range p.hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return
		}
	}
	l.add(field, "link must point to %s", strings.Join(p.hosts, " or "))
}

func (d profileDoc) validate(l *errList) {
	if d.Handle != "" {
		switch {
		case !handlePattern.MatchString(d.Handle):
			l.add("handle", "must be 3 to 32 lowercase letters, digits or underscores")
		case reservedHandles[d.Handle]:
			l.add("handle", "is reserved")
		}
	}
	l.optionalText("display_name", d.DisplayName, 32, false)
	l.optionalText("subtitle", d.Subtitle, 64, false)
	l.optionalText("location", d.Location, 64, false)
	l.optionalText("bio", d.Bio, 500, true)
	if len(d.HeaderImages) > maxHeaderImages {
		l.add("header_images", "at most %d images", maxHeaderImages)
	}
	for i, u := range d.HeaderImages {
		if !httpsURL(u) {
			l.add(fmt.Sprintf("header_images[%d]", i), "must be an https address of at most %d characters", maxURLLength)
		}
	}
	l.social("twitter", d.Twitter)
	l.social("instagram", d.Instagram)
	l.social("weibo", d.Weibo)
	l.social("facebook", d.Facebook)
	l.social("github", d.Github)
	if d.IsPublic && d.Handle == "" {
		l.add("is_public", "set a handle before making the profile public")
	}
}
