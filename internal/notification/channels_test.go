package notification

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

// fakeSMTP is a minimal plaintext SMTP server that records one message.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	from string
	to   []string
	data string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = fmt.Fprintf(conn, "%s\r\n", line) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.from = strings.TrimSpace(line[len("MAIL FROM:"):])
			s.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			s.to = append(s.to, strings.TrimSpace(line[len("RCPT TO:"):]))
			s.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func TestSMTPSendsOneMessageToTheVerifiedRecipientOnly(t *testing.T) {
	server := newFakeSMTP(t)
	s := &SMTP{Host: "127.0.0.1", Port: server.port(), From: "山海足迹 <noreply@example.com>", Security: "none", Timeout: 5 * time.Second}
	err := s.Send(context.Background(), Mail{To: "Alice@Example.com", Subject: "标题", Text: "正文", UnsubscribeURL: "https://trip.example.com/api/notifications/unsubscribe?t=abc"})
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.from != "<noreply@example.com>" || len(server.to) != 1 || server.to[0] != "<alice@example.com>" {
		t.Fatalf("envelope: %q %v", server.from, server.to)
	}
	if !strings.Contains(server.data, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") || !strings.Contains(server.data, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("message:\n%s", server.data)
	}
	if err := (&SMTP{Host: "127.0.0.1", Port: 1, From: "noreply@example.com", Security: "none", Timeout: time.Second}).Send(context.Background(), Mail{To: "a@example.com", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("expected a connection error")
	}
	if err := s.Send(context.Background(), Mail{To: "not an address", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("accepted a bad recipient")
	}
}

func TestWebPushEncryptsSignsAndMapsStatuses(t *testing.T) {
	vapidPrivate, vapidPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	browser, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	target := PushTarget{P256dh: base64.RawURLEncoding.EncodeToString(browser.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(secret)}
	if err := ValidPushKeys(target.P256dh, target.Auth); err != nil {
		t.Fatal(err)
	}

	status := http.StatusCreated
	var gotAuth, gotEncoding, gotTTL, gotTopic string
	var gotBody int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotEncoding, gotTTL, gotTopic = r.Header.Get("Authorization"), r.Header.Get("Content-Encoding"), r.Header.Get("TTL"), r.Header.Get("Topic")
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		gotBody = n
		w.WriteHeader(status)
	}))
	defer server.Close()
	target.Endpoint = server.URL + "/push/abc"

	w := NewWebPush(vapidPublic, vapidPrivate, "mailto:ops@example.com")
	w.client = server.Client()
	payload := PushPayload{Title: "山海足迹 · 一年前的今天", Body: "一年前的今天，你有 1 段旅行记忆。", URL: "https://trip.example.com/", Tag: "memory"}
	if err := w.send(context.Background(), target, payload); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gotAuth, "vapid t=") || gotEncoding != "aes128gcm" || gotTTL != strconv.Itoa(12*3600) || gotTopic != "memory" || gotBody < 100 {
		t.Fatalf("request: auth=%q enc=%q ttl=%q topic=%q body=%d", gotAuth, gotEncoding, gotTTL, gotTopic, gotBody)
	}
	for code, want := range map[int]error{http.StatusGone: ErrSubscriptionGone, http.StatusNotFound: ErrSubscriptionGone} {
		status = code
		if err := w.send(context.Background(), target, payload); !errors.Is(err, want) {
			t.Errorf("%d: %v", code, err)
		}
	}
	status = http.StatusInternalServerError
	if err := w.send(context.Background(), target, payload); err == nil || errors.Is(err, ErrSubscriptionGone) {
		t.Errorf("500: %v", err)
	}
	// The public entry point refuses endpoints that fail validation before any request.
	if err := w.Send(context.Background(), PushTarget{Endpoint: "https://127.0.0.1/x", P256dh: target.P256dh, Auth: target.Auth}, payload); err == nil {
		t.Fatal("sent to an IP literal")
	}
}
