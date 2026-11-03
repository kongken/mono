package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/authnotification"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
)

const ExternalFlowTTL = 15 * time.Minute
const ConfirmationTTL = 10 * time.Minute
const OperationReceiptTTL = 24 * time.Hour

// FlowBinding is trusted BFF context, never a bearer credential. Authenticated
// calls get UserID from the verified service credential, not the request body.
type FlowBinding struct {
	FlowID                   uuid.UUID
	UserID                   uuid.UUID
	ContextHash, SessionHash []byte
	AuthVersion              int64
}

func (FlowBinding) String() string         { return "[flow binding redacted]" }
func (f FlowBinding) GoString() string     { return f.String() }
func (f FlowBinding) LogValue() slog.Value { return slog.StringValue(f.String()) }

type ChallengeRef struct {
	ID uuid.UUID
	FlowBinding
}

func (ChallengeRef) String() string         { return "[challenge reference redacted]" }
func (f ChallengeRef) GoString() string     { return f.String() }
func (f ChallengeRef) LogValue() slog.Value { return slog.StringValue(f.String()) }

type ProofCodec interface {
	SealProof(uuid.UUID, []byte, []byte) ([]byte, string, error)
	OpenProof(string, uuid.UUID, []byte, []byte) ([]byte, error)
}
type ProviderPolicy struct {
	Enabled       bool
	ConfigVersion int64
	RedirectURI   string
}
type ExternalSettings struct {
	Providers map[externalprovider.ProviderName]externalprovider.ExternalProvider
	Policies  map[externalprovider.ProviderName]ProviderPolicy
	Codec     ProofCodec
}

// ConfigureExternal atomically replaces local configuration. A reported gate
// closure has drained every admitted transaction in this instance. Deployments
// replacing startup-only config must also drain every old instance (ADR §6.3).
func (s *Service) ConfigureExternal(in ExternalSettings) {
	s.admissionMu.Lock()
	defer s.admissionMu.Unlock()
	copy := ExternalSettings{Codec: in.Codec, Providers: map[externalprovider.ProviderName]externalprovider.ExternalProvider{}, Policies: map[externalprovider.ProviderName]ProviderPolicy{}}
	for k, v := range in.Providers {
		copy.Providers[k] = v
	}
	for k, v := range in.Policies {
		copy.Policies[k] = v
	}
	s.external = copy
}

type ChallengeMail struct {
	ID                 uuid.UUID
	Email, Token, Kind string
	ExpiresAt          time.Time
}

func (ChallengeMail) String() string         { return "[challenge mail redacted]" }
func (m ChallengeMail) GoString() string     { return m.String() }
func (m ChallengeMail) LogValue() slog.Value { return slog.StringValue(m.String()) }

type NotificationMail struct {
	ID          uuid.UUID
	Email, Kind string
}

func (NotificationMail) String() string         { return "[notification mail redacted]" }
func (m NotificationMail) GoString() string     { return m.String() }
func (m NotificationMail) LogValue() slog.Value { return slog.StringValue(m.String()) }

type SecurityMail interface {
	QueueChallenge(context.Context, *ent.Tx, ChallengeMail) error
	QueueNotification(context.Context, *ent.Tx, NotificationMail) error
}

func (s *Service) SetSecurityMail(m SecurityMail) { s.securityMail = m }

type verifiedProof struct {
	Identity     externalprovider.ProviderIdentity
	ContactEmail string
	EmailTrusted bool
}

func (verifiedProof) String() string         { return "[external proof redacted]" }
func (p verifiedProof) GoString() string     { return p.String() }
func (p verifiedProof) LogValue() slog.Value { return slog.StringValue(p.String()) }

func (s *Service) policy(name externalprovider.ProviderName, version int64) (externalprovider.ExternalProvider, error) {
	p, ok := s.external.Policies[name]
	adapter := s.external.Providers[name]
	if !ok || !p.Enabled || adapter == nil {
		return nil, ErrProviderDisabled
	}
	if version != 0 && p.ConfigVersion != version {
		return nil, ErrChallengeInvalid
	}
	if s.external.Codec == nil {
		return nil, ErrUnavailable
	}
	return adapter, nil
}
func validBinding(b FlowBinding, external bool) bool {
	if len(b.ContextHash) != 32 || (external && b.FlowID == uuid.Nil) {
		return false
	}
	if b.UserID == uuid.Nil {
		return b.AuthVersion == 0 && len(b.SessionHash) == 0
	}
	return b.AuthVersion > 0 && len(b.SessionHash) == 32
}
func matches(c *ent.AuthChallenge, ref ChallengeRef) bool {
	if c.ID != ref.ID || !bytes.Equal(c.ContextHash, ref.ContextHash) {
		return false
	}
	if c.BffFlowID != nil && *c.BffFlowID != ref.FlowID {
		return false
	}
	if c.UserID == nil {
		return ref.UserID == uuid.Nil && ref.AuthVersion == 0 && len(ref.SessionHash) == 0
	}
	return *c.UserID == ref.UserID && c.AuthVersion != nil && *c.AuthVersion == ref.AuthVersion && bytes.Equal(c.SessionBindingHash, ref.SessionHash)
}
func (s *Service) usable(c *ent.AuthChallenge, ref ChallengeRef, kind authchallenge.Kind) error {
	if !matches(c, ref) || c.Kind != kind || c.ConsumedAt != nil || !c.ExpiresAt.After(s.now()) || c.Status != authchallenge.StatusReady {
		return ErrChallengeInvalid
	}
	return nil
}
func (s *Service) readProof(c *ent.AuthChallenge) (verifiedProof, error) {
	var p verifiedProof
	if s.external.Codec == nil || c.ProofKeyID == nil || len(c.ProofCiphertext) == 0 {
		return p, ErrChallengeInvalid
	}
	plain, err := s.external.Codec.OpenProof(*c.ProofKeyID, c.ID, c.ContextHash, c.ProofCiphertext)
	if err != nil {
		return p, ErrChallengeInvalid
	}
	defer clear(plain)
	if json.Unmarshal(plain, &p) != nil || c.Provider == nil || string(p.Identity.Provider) != string(*c.Provider) || c.Issuer == nil || p.Identity.Issuer != *c.Issuer {
		return verifiedProof{}, ErrChallengeInvalid
	}
	return p, nil
}
func (s *Service) writeProof(ctx context.Context, tx *ent.Tx, c *ent.AuthChallenge, p verifiedProof) error {
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}
	defer clear(plain)
	cipher, key, err := s.external.Codec.SealProof(c.ID, c.ContextHash, plain)
	if err != nil {
		return ErrUnavailable
	}
	return tx.AuthChallenge.UpdateOneID(c.ID).SetProofCiphertext(cipher).SetProofKeyID(key).Exec(ctx)
}
func externalKey(i externalprovider.ProviderIdentity) string {
	return string(i.Provider) + "\x00" + i.Issuer + "\x00" + i.Subject
}
func lockExternal(ctx context.Context, tx *ent.Tx, i externalprovider.ProviderIdentity) error {
	d := sha256.Sum256([]byte(externalKey(i)))
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(d[:8])))
	return err
}
func findIdentity(ctx context.Context, tx *ent.Tx, i externalprovider.ProviderIdentity) (*ent.ExternalIdentity, error) {
	return tx.ExternalIdentity.Query().Where(externalidentity.ProviderEQ(externalidentity.Provider(i.Provider)), externalidentity.Issuer(i.Issuer), externalidentity.Subject(i.Subject)).Only(ctx)
}
func (s *Service) createIdentity(ctx context.Context, tx *ent.Tx, uid uuid.UUID, i externalprovider.ProviderIdentity) (*ent.ExternalIdentity, error) {
	return tx.ExternalIdentity.Create().SetUserID(uid).SetProvider(externalidentity.Provider(i.Provider)).SetIssuer(i.Issuer).SetSubject(i.Subject).
		SetEmailSnapshot(i.Email).SetEmailVerifiedAt(s.now()).SetDisplayNameSnapshot(i.DisplayName).SetAvatarURLSnapshot(i.AvatarURL).
		SetLinkedAt(s.now()).SetLastAuthenticatedAt(s.now()).SetUpdatedAt(s.now()).Save(ctx)
}
func lockChallenges(ctx context.Context, tx *ent.Tx, ids ...uuid.UUID) (map[uuid.UUID]*ent.AuthChallenge, error) {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	rows, err := tx.AuthChallenge.Query().Where(authchallenge.IDIn(ids...)).Order(ent.Asc(authchallenge.FieldID)).ForUpdate().All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]*ent.AuthChallenge{}
	for _, c := range rows {
		out[c.ID] = c
	}
	for _, id := range ids {
		if out[id] == nil {
			return nil, ErrChallengeInvalid
		}
	}
	return out, nil
}
func (s *Service) consume(ctx context.Context, tx *ent.Tx, id uuid.UUID) error {
	return tx.AuthChallenge.UpdateOneID(id).SetStatus(authchallenge.StatusCompleted).SetConsumedAt(s.now()).ClearProofCiphertext().ClearProofKeyID().ClearEmailTokenHash().Exec(ctx)
}
func (s *Service) notify(ctx context.Context, tx *ent.Tx, u *ent.User, source string, sourceID uuid.UUID, kind string) error {
	if s.securityMail == nil {
		return ErrUnavailable
	}
	e, err := tx.AuthNotification.Create().SetUserID(u.ID).SetSourceKind(authnotification.SourceKind(source)).SetSourceID(sourceID).SetNotificationKind(authnotification.NotificationKind(kind)).SetCreatedAt(s.now()).Save(ctx)
	if err != nil {
		return err
	}
	return s.securityMail.QueueNotification(ctx, tx, NotificationMail{ID: e.ID, Email: u.EmailCanonical, Kind: kind})
}
func lockedUser(ctx context.Context, tx *ent.Tx, id uuid.UUID) (*ent.User, error) {
	u, err := tx.User.Query().Where(user.ID(id)).WithRoles().ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if u.Status != user.StatusActive {
		return nil, ErrNotActive
	}
	return u, nil
}
func externalError(err error) error {
	if err == nil {
		return nil
	}
	// Never log a SQL unique-constraint detail containing an external key/email.
	if isUniqueViolation(err, "") {
		return ErrConflict
	}
	if isOutage(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrUnavailable
	}
	return err
}
