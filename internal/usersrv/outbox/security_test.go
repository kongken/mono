package outbox

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/mailer"
)

func TestExternalProofCipherBindsContextRowPurposeAndKey(t *testing.T) {
	k := testKeys(t, "a", "a", "b")
	codec, err := k.ProofCipher("b")
	if err != nil {
		t.Fatal(err)
	}
	id, context, plain := uuid.New(), bytes.Repeat([]byte{5}, 32), []byte("private subject and candidate email")
	cipher, key, err := codec.SealProof(id, context, plain)
	if err != nil || key != "b" || bytes.Contains(cipher, plain) {
		t.Fatal("proof encryption failed")
	}
	if opened, err := codec.OpenProof(key, id, context, cipher); err != nil || !bytes.Equal(opened, plain) {
		t.Fatal("proof roundtrip failed")
	}
	wrongContext := bytes.Repeat([]byte{6}, 32)
	for _, tc := range []struct {
		key             string
		id              uuid.UUID
		context, cipher []byte
	}{{"a", id, context, cipher}, {"missing", id, context, cipher}, {key, uuid.New(), context, cipher}, {key, id, wrongContext, cipher}, {key, id, context, cipher[:12]}} {
		if _, err := codec.OpenProof(tc.key, tc.id, tc.context, tc.cipher); err == nil {
			t.Fatal("proof escaped row/context/key binding")
		}
	}
	if _, err := k.open(key, id.String(), string(mailer.KindExternalRegistration), cipher); err == nil {
		t.Fatal("proof accepted as mail ciphertext")
	}
	mailCipher, mailKey, err := k.seal(id.String(), string(mailer.KindExternalRegistration), plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.OpenProof(mailKey, id, context, mailCipher); err == nil {
		t.Fatal("mail accepted as external proof")
	}
}

func TestPreflightRefusesMissingExternalProofKeyAndExpiryErasesIt(t *testing.T) {
	e := newEnv(t)
	s := newService(t, e, &fakeSender{}, testKeys(t, "a", "a"))
	id := uuid.New()
	e.db.AuthChallenge.Create().SetID(id).SetKind(authchallenge.KindExternalRegistration).SetProvider(authchallenge.ProviderGoogle).SetIssuer("https://accounts.google.com").SetProviderConfigVersion(1).
		SetContextHash(bytes.Repeat([]byte{1}, 32)).SetStatus(authchallenge.StatusReady).SetProofKeyID("missing").SetProofCiphertext([]byte("opaque fixture")).SetCreatedAt(e.clock()).SetExpiresAt(e.clock().Add(time.Minute)).ExecX(ctx)
	if err := s.Preflight(ctx); err == nil {
		t.Fatal("startup silently lost a pending proof")
	}
	e.db.AuthChallenge.UpdateOneID(id).ClearProofKeyID().ClearProofCiphertext().SetStatus(authchallenge.StatusFailed).ExecX(ctx)
	if err := s.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
}
