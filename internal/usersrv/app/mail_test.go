package app_test

import (
	"context"
	"encoding/base64"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kongken/mono/internal/usersrv/app"
	"github.com/kongken/mono/internal/usersrv/config"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/mailer"
	"github.com/kongken/mono/internal/usersrv/outbox"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

type countingSender struct{ n atomic.Int32 }

func (c *countingSender) Send(context.Context, mailer.Message) (mailer.Result, error) {
	c.n.Add(1)
	return mailer.Result{}, nil
}

// The in-process worker delivers queued mail, /readyz reports it locally, and
// shutdown stops it; none of this contacts a vendor.
func TestMailWorkerRunsInProcessAndReportsReadiness(t *testing.T) {
	db := testutil.DB(t)
	rdb, prefix := testutil.Redis(t)
	sender := &countingSender{}
	m, err := mailer.NewWithSender(mailer.Config{Provider: "sendgrid", FromEmail: "no-reply@example.com", SendGrid: mailer.SendGridConfig{APIKey: "SG.key"}}, sender)
	if err != nil {
		t.Fatal(err)
	}
	links, _ := mailer.NewLinks(map[string]string{"trip-map": "https://map.example.com"}, false)
	keys, err := (outbox.EncryptionConfig{ActiveKeyID: "k1", Keys: []outbox.KeyConfig{
		{ID: "k1", Key: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))}}}).Keyring()
	if err != nil {
		t.Fatal(err)
	}
	mail, err := outbox.New(db, keys, links, m, outbox.Settings{RegistrationTTL: 30 * time.Minute, ResetTTL: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.NewResources(db, testutil.Hasher(t), rdb, prefix, []byte(strings.Repeat("h", 32)), limits(),
		func(ctx context.Context) error { return nil }, app.WithMail(mail))
	if err != nil {
		t.Fatal(err)
	}
	if res.Mail == nil || res.MailGuard == nil {
		t.Fatal("mail services missing from the resources")
	}
	h := app.New(&config.AppConfig{})
	h.SetResources(res)

	ready := func() map[string]bool {
		got := map[string]bool{}
		for _, st := range h.Check(context.Background()) {
			got[st.Name] = st.Ready
		}
		return got
	}
	if got := ready(); got["mail_outbox"] {
		t.Fatalf("mail worker reported ready before it was started: %v", got)
	}
	for _, w := range res.Workers {
		w.Start()
	}
	if got := ready(); !got["mail_outbox"] || !got["worker"] || !got["postgres"] {
		t.Fatalf("readiness: %v", got)
	}

	out, err := res.Mail.IssueStandalone(context.Background(), outbox.IssueInput{Purpose: outbox.PurposeRegistration, Email: "alice@example.com", AppID: "trip-map"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		j := db.MailJob.GetX(context.Background(), out.JobID)
		if j.Status == mailjob.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the background worker did not deliver the job: %+v", j)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if sender.n.Load() != 1 {
		t.Fatalf("%d sends", sender.n.Load())
	}
	if err := h.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
