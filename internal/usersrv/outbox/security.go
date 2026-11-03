package outbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/mailer"
)

// ProofCipher uses independent associated data from mail encryption, binding
// every short-lived identity proof to its challenge and browser context.
type ProofCipher struct {
	keys   *Keyring
	active string
}

func (k *Keyring) ProofCipher(keyID string) (*ProofCipher, error) {
	if keyID == "" {
		keyID = k.active
	}
	if !k.Has(keyID) {
		return nil, errors.New("external proof key is not configured")
	}
	return &ProofCipher{keys: k, active: keyID}, nil
}
func proofAAD(id uuid.UUID, context []byte) []byte {
	return []byte("usersrv-auth-proof:v1\x00" + id.String() + "\x00" + base64.RawURLEncoding.EncodeToString(context))
}
func (p *ProofCipher) SealProof(id uuid.UUID, context, plain []byte) ([]byte, string, error) {
	if id == uuid.Nil || len(context) != 32 {
		return nil, "", errDecrypt
	}
	a := p.keys.keys[p.active]
	nonce := make([]byte, 1+nonceBytes)
	nonce[0] = formatV1
	if _, err := rand.Read(nonce[1:]); err != nil {
		return nil, "", err
	}
	return a.Seal(nonce, nonce[1:], plain, proofAAD(id, context)), p.active, nil
}
func (p *ProofCipher) OpenProof(key string, id uuid.UUID, context, cipher []byte) ([]byte, error) {
	a, ok := p.keys.keys[key]
	if !ok || len(context) != 32 || len(cipher) < minSealedLen || cipher[0] != formatV1 {
		return nil, errDecrypt
	}
	plain, err := a.Open(nil, cipher[1:1+nonceBytes], cipher[1+nonceBytes:], proofAAD(id, context))
	if err != nil {
		return nil, errDecrypt
	}
	return plain, nil
}
func (s *Service) ProofCipher(keyID string) (*ProofCipher, error) { return s.keys.ProofCipher(keyID) }

func (s *Service) QueueSecurity(ctx context.Context, tx *ent.Tx, sourceID uuid.UUID, email, token, template string, expires time.Time, notification bool) error {
	if notification {
		expires = s.now().Add(24 * time.Hour)
	}
	email, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	kind := mailer.Kind(template)
	if !kind.Valid() || !s.Central() || !expires.After(s.now()) {
		return errors.New("invalid security mail")
	}
	source := mailjob.SourceKindAuthChallenge
	if notification {
		source = mailjob.SourceKindNotification
		if !kind.Notification() || token != "" {
			return errors.New("invalid notification")
		}
	} else if kind.Notification() {
		return errors.New("invalid challenge template")
	}
	id := uuid.New()
	plain, err := json.Marshal(payload{Version: 1, To: email, AppID: mailer.CentralApp, Token: token})
	if err != nil {
		return err
	}
	defer clear(plain)
	cipher, key, err := s.keys.seal(id.String(), template, plain)
	if err != nil {
		return err
	}
	return tx.MailJob.Create().SetID(id).SetSourceKind(source).SetChallengeID(sourceID).SetProvider(s.mailer.Provider()).SetGeneration(s.mailer.Generation()).SetTemplate(template).
		SetCiphertext(cipher).SetKeyID(key).SetNextAttemptAt(s.now()).SetExpiresAt(expires).SetCreatedAt(s.now()).SetUpdatedAt(s.now()).Exec(ctx)
}
func (s *Service) recheckSecurity(ctx context.Context, job *ent.MailJob, pl payload, now time.Time) (*skip, error) {
	if job.SourceKind == mailjob.SourceKindNotification {
		e, err := s.db.AuthNotification.Get(ctx, job.ChallengeID)
		if ent.IsNotFound(err) {
			return &skip{mailjob.StatusCancelled, "source_missing"}, nil
		}
		if err != nil {
			return nil, err
		}
		if !mailer.Kind(job.Template).Notification() || string(e.NotificationKind) != job.Template || pl.Token != "" {
			return &skip{mailjob.StatusCancelled, "template_mismatch"}, nil
		}
		u, err := s.db.User.Get(ctx, e.UserID)
		if ent.IsNotFound(err) {
			return &skip{mailjob.StatusCancelled, "account_gone"}, nil
		}
		if err != nil {
			return nil, err
		}
		if u.EmailCanonical != pl.To || u.Status == user.StatusDeleting {
			return &skip{mailjob.StatusCancelled, "account_changed"}, nil
		}
		return nil, nil
	}
	c, err := s.db.AuthChallenge.Get(ctx, job.ChallengeID)
	if ent.IsNotFound(err) {
		return &skip{mailjob.StatusCancelled, "challenge_missing"}, nil
	}
	if err != nil {
		return nil, err
	}
	digest, ok := identity.TokenDigest(pl.Token)
	if !ok || !bytes.Equal(digest, c.EmailTokenHash) || c.ConsumedAt != nil || c.ConfirmedAt != nil || c.Status != authchallenge.StatusReady {
		return &skip{mailjob.StatusCancelled, "challenge_consumed"}, nil
	}
	if !c.ExpiresAt.After(now) || c.EmailExpiresAt == nil || !c.EmailExpiresAt.After(now) {
		return &skip{mailjob.StatusExpired, "challenge_expired"}, nil
	}
	if (job.Template == "external_registration") != (c.Kind == authchallenge.KindExternalRegistration) || (job.Template != "external_registration" && job.Template != "identity_operation") {
		return &skip{mailjob.StatusCancelled, "template_mismatch"}, nil
	}
	if c.UserID != nil {
		u, err := s.db.User.Get(ctx, *c.UserID)
		if ent.IsNotFound(err) {
			return &skip{mailjob.StatusCancelled, "account_gone"}, nil
		}
		if err != nil {
			return nil, err
		}
		if u.Status != user.StatusActive || c.AuthVersion == nil || u.AuthVersion != *c.AuthVersion || u.EmailCanonical != pl.To {
			return &skip{mailjob.StatusCancelled, "account_changed"}, nil
		}
	}
	return nil, nil
}
