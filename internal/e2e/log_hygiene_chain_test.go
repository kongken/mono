package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/gin-gonic/gin"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/status"
)

// logSink collects everything the stack writes to its log sinks.
type logSink struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureLogs points every log sink the BFF and usersrv can write to at one buffer, at the
// most verbose level: the structured logger (slog, which butterfly installs in production),
// the standard logger (net/http server errors), gin's writers (Recovery's panic report;
// production discards gin's access log) and gRPC's own logger. In production the same calls
// write to stderr.
func captureLogs(t *testing.T) *logSink {
	t.Helper()
	sink := &logSink{}
	prevSlog := slog.Default()
	prevOut, prevFlags := log.Writer(), log.Flags()
	prevGin, prevGinErr := gin.DefaultWriter, gin.DefaultErrorWriter
	slog.SetDefault(slog.New(slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true})))
	log.SetOutput(sink)
	gin.DefaultWriter, gin.DefaultErrorWriter = sink, sink
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(sink, sink, sink, 99))
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		gin.DefaultWriter, gin.DefaultErrorWriter = prevGin, prevGinErr
		grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, os.Stderr))
	})
	return sink
}

// Every value a user types or a mail carries is synthetic and unique here, so a leak into a
// log line is unambiguous. Each secret must appear in no log line, whichever way the flow
// ended: success, a refusal, a limit, an outage, an internal error, a refusing vendor.
func TestChainLogsNeverCarryTokensPasswordsOrBodies(t *testing.T) {
	sink := captureLogs(t)
	ctx := context.Background()

	const (
		pwGood   = "Synth-Pw-7f3a9c-correct-horse"
		pwWeak   = "weak-Q7x"
		pwWrong  = "Wrong-Synth-8e2f-password"
		pwNext   = "Fresh-Synth-5d0c-battery"
		pwNext2  = "Other-Synth-2b6e-staple-x"
		display  = "Synth Display ZQ91"
		ctxPath  = "/synth-path-ZQ91"
		emailMix = "logcheck.synth.zq91@example.com"
	)
	bogusToken := strings.Repeat("SynthTokenZQ91", 4)[:43]
	secrets := map[string]string{
		"good password": pwGood, "weak password": pwWeak, "wrong password": pwWrong, "next password": pwNext,
		"second next password": pwNext2, "display name": display, "return path": ctxPath, "bogus token": bogusToken,
	}
	token := func(label, tok string) { secrets[label] = tok }

	h := NewChainHarness(t) // no navigation registry: a context is refused (FAILED_PRECONDITION)
	b := h.FreshBrowser()
	email := emailFor("logcheck")

	// Registration: a refused context, a request, a weak password, a bogus token, the real
	// completion, a replay, and wrong and right sign-ins.
	if err := b.requestRegistrationFrom(ctx, email, "metro", ctxPath); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("context without registry: %v", err)
	}
	if err := b.requestRegistrationFrom(ctx, email, "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	reg := h.Inbox.TokenIn(t, email)
	token("registration token", reg)
	if err := b.CompleteRegistration(ctx, reg, pwWeak, display); err == nil {
		t.Fatal("a weak password was accepted")
	}
	if err := b.CompleteRegistration(ctx, bogusToken, pwGood, display); err == nil {
		t.Fatal("a bogus token was accepted")
	}
	if err := b.CompleteRegistration(ctx, reg, pwGood, display); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteRegistration(ctx, reg, pwGood, display); err == nil {
		t.Fatal("a replay was accepted")
	}
	if _, err := b.LoginWithEmail(ctx, email, pwWrong); err == nil {
		t.Fatal("a wrong password signed in")
	}
	if _, err := b.LoginWithEmail(ctx, email, pwGood); err != nil {
		t.Fatal(err)
	}

	// Reset: request, a bogus token, a weak password, the real reset, a replay.
	anon := h.FreshBrowser()
	if err := anon.requestResetFrom(ctx, email, "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	rst := h.Inbox.TokenIn(t, email)
	token("reset token", rst)
	if err := anon.ResetPassword(ctx, bogusToken, pwNext); err == nil {
		t.Fatal("a bogus reset token was accepted")
	}
	if err := anon.ResetPassword(ctx, rst, pwWeak); err == nil {
		t.Fatal("a weak new password was accepted")
	}
	if err := anon.ResetPassword(ctx, rst, pwNext); err != nil {
		t.Fatal(err)
	}
	if err := anon.ResetPassword(ctx, rst, pwNext2); err == nil {
		t.Fatal("a replayed reset token was accepted")
	}

	// Registration closed: a challenge issued before the switch is refused at completion.
	closing := emailFor("logcheck-closed")
	if err := anon.requestRegistrationFrom(ctx, closing, "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	closed := h.Inbox.TokenIn(t, closing)
	token("closed-registration token", closed)
	h.Accounts.SetRegistrationOpen(false)
	if err := anon.CompleteRegistration(ctx, closed, pwGood, display); err == nil {
		t.Fatal("registration completed while closed")
	}
	if err := anon.requestRegistrationFrom(ctx, emailFor("logcheck-closed2"), "", ""); err == nil {
		t.Fatal("registration requested while closed")
	}
	h.Accounts.SetRegistrationOpen(true)

	// An outage and an internal error inside usersrv, with real bodies in flight.
	for _, fail := range []error{status.Error(codes.Unavailable, "synthetic outage"), status.Error(codes.Internal, "synthetic internal"), errors.New("synthetic plain error")} {
		h.FailUsersrv(func(string) error { return fail })
		_ = anon.requestRegistrationFrom(ctx, emailFor("logcheck-down"), "", "")
		_ = anon.CompleteRegistration(ctx, bogusToken, pwGood, display)
		_ = anon.ResetPassword(ctx, bogusToken, pwNext)
		_, _ = anon.LoginWithEmail(ctx, email, pwWrong)
	}
	h.FailUsersrv(nil)

	// A vendor that refuses and echoes the whole message, link and token included.
	var echoed string
	h.Inbox.FailWith(func(m mailer.Message) error {
		echoed = linkToken.FindStringSubmatch(m.Text)[1]
		return &mailer.Error{Class: mailer.ClassPermanent, Provider: "sendgrid", Code: "http_400", Detail: m.Text}
	})
	if err := anon.requestRegistrationFrom(ctx, emailFor("logcheck-vendor"), "", ""); err != nil {
		t.Fatal(err)
	}
	h.DeliverMail()
	h.Inbox.FailWith(nil)
	if echoed == "" {
		t.Fatal("the refusing vendor was never offered a message")
	}
	token("token in a refused mail", echoed)

	// Positive control: the sink does see structured log lines, and the check below would
	// see a secret in one. Evaluate what was logged before the control line is written.
	logged := sink.String()
	slog.Error("canary", "token", reg)
	if !strings.Contains(sink.String(), reg) {
		t.Fatal("the capture does not see slog output")
	}
	if !strings.Contains(logged, "usersrv mail job") {
		t.Fatalf("the capture saw no mail-job log line at all:\n%s", logged)
	}

	for label, secret := range secrets {
		if i := strings.Index(logged, secret); i >= 0 {
			from := max(0, i-200)
			t.Errorf("%s appears in the logs: ...%s...", label, logged[from:min(len(logged), i+len(secret)+80)])
		}
	}
	t.Logf("checked %d synthetic secrets against %d bytes of log output; lines by message: %v", len(secrets), len(logged), messageCounts(logged))
}

var logMessage = regexp.MustCompile(`"msg":"([^"]*)"`)

// messageCounts summarises which log messages the scenario produced, so the evidence shows
// the error paths really logged (and that the check read them).
func messageCounts(logged string) map[string]int {
	counts := map[string]int{}
	for _, m := range logMessage.FindAllStringSubmatch(logged, -1) {
		counts[m[1]]++
	}
	return counts
}

// The same, for the limits: a limited request and a limited submission log no secret either.
func TestChainRateLimitedAccountRequestsLogNoSecrets(t *testing.T) {
	sink := captureLogs(t)
	ctx := context.Background()
	limits := usersrvLimits()
	limits.Mail.RegistrationPerIP.Limit = 2
	limits.TokenSubmitPerToken.Limit = 1
	h := NewChainHarness(t, ChainUsersrvLimits(limits))
	b := h.FreshBrowser()

	const pw, display = "Synth-Pw-9d4e1a-limited-horse", "Synth Limited ZQ92"
	bogus := strings.Repeat("SynthLimitZQ92x", 4)[:43]
	var limited int
	for i := 0; i < 6; i++ {
		if err := b.requestRegistrationFrom(ctx, emailFor("limited"+string(rune('a'+i))), "", ""); connect.CodeOf(err) == connect.CodeResourceExhausted {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("the mail request limit never applied")
	}
	h.DeliverMail()
	var submitLimited int
	for i := 0; i < 4; i++ {
		if err := b.CompleteRegistration(ctx, bogus, pw, display); connect.CodeOf(err) == connect.CodeResourceExhausted {
			submitLimited++
		}
	}
	if submitLimited == 0 {
		t.Fatal("the token submission limit never applied")
	}
	time.Sleep(50 * time.Millisecond)
	logged := sink.String()
	for label, secret := range map[string]string{"password": pw, "display name": display, "token": bogus} {
		if i := strings.Index(logged, secret); i >= 0 {
			t.Errorf("%s appears in the logs: ...%s...", label, logged[max(0, i-200):min(len(logged), i+len(secret)+80)])
		}
	}
}
