package app

import (
	"context"

	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/outbox"
)

// AccountMail adapts the outbox and its limits to the account core, which
// declares the small interfaces it needs (account.Mail, account.MailGuard) and
// does not import the outbox.
func AccountMail(svc *outbox.Service, guard *outbox.Guard) (account.Mail, account.MailGuard) {
	return outboxMail{svc}, outboxGuard{guard}
}

type outboxMail struct{ svc *outbox.Service }

func (m outboxMail) Central() bool { return m.svc.Central() }

func (m outboxMail) Issue(ctx context.Context, tx *ent.Tx, in account.MailRequest) error {
	_, err := m.svc.Issue(ctx, tx, outbox.IssueInput{
		Purpose: string(in.Purpose), Email: in.Email, UserID: in.UserID, AuthVersion: in.AuthVersion,
		ReturnAppID: in.Return.AppID, ReturnPath: in.Return.Path,
	})
	return err
}

type outboxGuard struct{ g *outbox.Guard }

func (g outboxGuard) Check(ctx context.Context, purpose account.MailPurpose, email, clientIP string) (account.MailVerdict, error) {
	d, err := g.g.Check(ctx, string(purpose), email, clientIP)
	if err != nil {
		return account.MailLimited, err
	}
	switch d {
	case outbox.Allow:
		return account.MailAllow, nil
	case outbox.Suppress:
		return account.MailSuppress, nil
	}
	return account.MailLimited, nil
}
