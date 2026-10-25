package mailer_test

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- HTTP vendor fake -------------------------------------------------------

type recorded struct {
	Host   string // the host the adapter addressed, before the test rewrite
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// httpFake is a controlled HTTP server standing in for a vendor. Its transport
// rewrites the request to the local server but remembers the host the adapter
// really addressed, so the tests can prove the fixed official endpoints.
type httpFake struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []recorded
	handler http.HandlerFunc
}

func newHTTPFake(t *testing.T, h http.HandlerFunc) *httpFake {
	t.Helper()
	f := &httpFake{handler: h}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, recorded{Host: r.Header.Get("X-Test-Original-Host"), Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		f.mu.Unlock()
		f.handler(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *httpFake) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

type rewriteTransport struct {
	target string // host:port to really connect to
	base   http.RoundTripper
}

func (f *httpFake) transport() http.RoundTripper { return rewriteTo(f.srv.Listener.Addr().String()) }

func rewriteTo(target string) http.RoundTripper {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DisableKeepAlives = true
	return &rewriteTransport{target: target, base: base}
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context()) // keeps the context, hence the adapter's write trace
	clone.Header.Set("X-Test-Original-Host", req.URL.Host)
	clone.URL.Scheme = "http"
	clone.URL.Host = r.target
	return r.base.RoundTrip(clone)
}

func formValue(t *testing.T, body []byte, key string) string {
	t.Helper()
	v, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatal(err)
	}
	return v.Get(key)
}

// ---- SMTP fake --------------------------------------------------------------

type smtpBehavior struct {
	mode          string // "plain", "implicit", "starttls"
	offerSTARTTLS bool   // starttls mode: false simulates a stripped extension
	authMechs     string // advertised AUTH mechanisms, "" for none
	greeting      string // default "220 fake ESMTP"
	authFail      string // reply instead of 235
	mailReply     string
	rcptReply     string
	dataReply     string // reply to DATA, default "354 go ahead"
	finalReply    string // default "250 queued"; used unless drop/hang
	dropAfterData bool   // close the connection after the terminator
	hangAfterData bool   // never answer the terminator
	startTLSFails bool
}

type smtpSession struct {
	TLS      bool
	EHLO     string
	User     string
	Pass     string
	From     string
	Rcpts    []string
	Data     string
	Clear    string // everything received before TLS (to prove nothing leaked in clear text)
	AuthUsed string
}

type fakeSMTP struct {
	ln       net.Listener
	b        smtpBehavior
	cert     tls.Certificate
	pool     *x509.CertPool
	mu       sync.Mutex
	sessions []*smtpSession
	wg       sync.WaitGroup
}

func newCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "usersrv test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func newSMTPFake(t *testing.T, b smtpBehavior) *fakeSMTP {
	t.Helper()
	f := &fakeSMTP{b: b}
	f.cert, f.pool = newCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() { defer f.wg.Done(); f.serve(c) }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); f.wg.Wait() })
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) last(t *testing.T) *smtpSession {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sessions) == 0 {
		t.Fatal("no SMTP session")
	}
	return f.sessions[len(f.sessions)-1]
}

func (f *fakeSMTP) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.sessions) }

func (f *fakeSMTP) serve(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	sess := &smtpSession{}
	f.mu.Lock()
	f.sessions = append(f.sessions, sess)
	f.mu.Unlock()
	var conn net.Conn = raw
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12}
	if f.b.mode == "implicit" {
		conn = tls.Server(raw, tlsCfg)
		sess.TLS = true
	}
	r := bufio.NewReader(conn)
	w := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	greeting := f.b.greeting
	if greeting == "" {
		greeting = "220 fake ESMTP"
	}
	w(greeting)
	if strings.HasPrefix(greeting, "4") || strings.HasPrefix(greeting, "5") {
		return
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if !sess.TLS {
			sess.Clear += line + "\n"
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			sess.EHLO = strings.TrimSpace(line[4:])
			w("250-fake")
			if f.b.mode == "starttls" && !sess.TLS && f.b.offerSTARTTLS {
				w("250-STARTTLS")
			}
			if f.b.authMechs != "" {
				w("250-AUTH " + f.b.authMechs)
			}
			w("250 8BITMIME")
		case cmd == "STARTTLS":
			if f.b.startTLSFails {
				w("454 TLS not available")
				continue
			}
			w("220 ready")
			conn = tls.Server(raw, tlsCfg)
			r = bufio.NewReader(conn)
			w = func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
			sess.TLS = true
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			sess.AuthUsed = "PLAIN"
			parts := strings.Fields(line)
			blob := ""
			if len(parts) >= 3 {
				blob = parts[2]
			} else {
				w("334 ")
				blob, _ = r.ReadString('\n')
				blob = strings.TrimSpace(blob)
			}
			dec, _ := base64.StdEncoding.DecodeString(blob)
			fields := bytes.Split(dec, []byte{0})
			if len(fields) == 3 {
				sess.User, sess.Pass = string(fields[1]), string(fields[2])
			}
			f.authReply(w)
		case strings.HasPrefix(cmd, "AUTH LOGIN"):
			sess.AuthUsed = "LOGIN"
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := r.ReadString('\n')
			w("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := r.ReadString('\n')
			du, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
			dp, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
			sess.User, sess.Pass = string(du), string(dp)
			f.authReply(w)
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			sess.From = angleAddress(line[len("MAIL FROM:"):])
			f.reply(w, f.b.mailReply, "250 OK")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			rc := angleAddress(line[len("RCPT TO:"):])
			reply := f.b.rcptReply
			if reply == "" || strings.HasPrefix(reply, "2") {
				sess.Rcpts = append(sess.Rcpts, rc)
			}
			f.reply(w, reply, "250 OK")
		case cmd == "DATA":
			dr := f.b.dataReply
			if dr == "" {
				dr = "354 go ahead"
			}
			w(dr)
			if !strings.HasPrefix(dr, "354") {
				continue
			}
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, "."))
			}
			sess.Data = data.String()
			switch {
			case f.b.dropAfterData:
				return
			case f.b.hangAfterData:
				_, _ = io.Copy(io.Discard, conn) // until the client gives up and closes
				return
			}
			f.reply(w, f.b.finalReply, "250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		case cmd == "RSET", cmd == "NOOP":
			w("250 OK")
		default:
			w("502 unsupported")
		}
	}
}

func (f *fakeSMTP) reply(w func(string), custom, def string) {
	if custom != "" {
		w(custom)
		return
	}
	w(def)
}

func (f *fakeSMTP) authReply(w func(string)) { f.reply(w, f.b.authFail, "235 ok") }

func portString(p int) string { return fmt.Sprint(p) }

// angleAddress returns the address between < and > of a MAIL/RCPT argument.
func angleAddress(arg string) string {
	arg = strings.TrimSpace(arg)
	if i := strings.Index(arg, ">"); i >= 0 {
		arg = arg[:i]
	}
	return strings.TrimPrefix(arg, "<")
}
