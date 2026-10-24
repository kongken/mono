// Package outbox is the reliable mail queue of usersrv. A challenge (an
// account_tokens row) and its mail_jobs row are created in one database
// transaction; a background worker, with a database lease so that several
// replicas never send one job twice at the same time, decrypts the job, checks
// that the challenge and the account still allow the mail, sends it through the
// configured vendor and records the classified result.
//
// The recipient, the template parameters and the plaintext token are stored only
// as AES-256-GCM ciphertext, with a key that lives in the service configuration
// (never in the database) and associated data that binds each ciphertext to its
// job ID and template. A job's ciphertext is erased the moment the job reaches a
// final state.
package outbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// EncryptionConfig is mail.encryption. Keys is the set of keys that can still
// decrypt queued jobs; ActiveKeyID chooses the one that encrypts new jobs. To
// rotate: add a new key, make it active, and keep the old key listed until no
// job encrypted with it can be pending any more.
type EncryptionConfig struct {
	ActiveKeyID string      `yaml:"active_key_id"`
	Keys        []KeyConfig `yaml:"keys"`
}

// KeyConfig is one AES-256 key: 32 random bytes in standard base64, inline or
// from a file (a mounted Secret). Generate one with: head -c 32 /dev/urandom | base64
type KeyConfig struct {
	ID      string `yaml:"id"`
	Key     string `yaml:"key"`
	KeyFile string `yaml:"key_file"`
}

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

const (
	keyBytes     = 32
	formatV1     = byte(1)
	aadPrefix    = "usersrv-mail-job:v1\x00"
	nonceBytes   = 12
	minSealedLen = 1 + nonceBytes + 16
)

// Keyring holds the encryption keys.
type Keyring struct {
	active string
	keys   map[string]cipher.AEAD
	raw    map[string][]byte
}

// Keyring validates the configuration and loads the keys.
func (c EncryptionConfig) Keyring() (*Keyring, error) {
	if len(c.Keys) == 0 {
		return nil, errors.New("mail.encryption.keys is required")
	}
	k := &Keyring{keys: map[string]cipher.AEAD{}, raw: map[string][]byte{}}
	for _, kc := range c.Keys {
		if !keyIDPattern.MatchString(kc.ID) {
			return nil, errors.New("mail.encryption.keys[].id must be 1-64 characters of letters, digits, dot, underscore, hyphen")
		}
		if _, dup := k.keys[kc.ID]; dup {
			return nil, fmt.Errorf("mail.encryption.keys: duplicate key id %q", kc.ID)
		}
		if (kc.Key == "") == (kc.KeyFile == "") {
			return nil, fmt.Errorf("mail.encryption.keys[%s]: set exactly one of key or key_file", kc.ID)
		}
		raw := kc.Key
		if kc.KeyFile != "" {
			b, err := os.ReadFile(kc.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("mail.encryption.keys[%s].key_file: %w", kc.ID, err)
			}
			raw = string(b)
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil || len(key) != keyBytes {
			return nil, fmt.Errorf("mail.encryption.keys[%s]: the key must be 32 bytes in standard base64", kc.ID)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		k.keys[kc.ID], k.raw[kc.ID] = aead, key
	}
	if _, ok := k.keys[c.ActiveKeyID]; !ok {
		return nil, errors.New("mail.encryption.active_key_id must name one of the configured keys")
	}
	k.active = c.ActiveKeyID
	return k, nil
}

// ActiveKeyID is the key new jobs are encrypted with.
func (k *Keyring) ActiveKeyID() string { return k.active }

// Has reports whether the key can decrypt.
func (k *Keyring) Has(id string) bool { _, ok := k.keys[id]; return ok }

// IDs lists the configured key IDs, sorted.
func (k *Keyring) IDs() []string {
	out := make([]string, 0, len(k.keys))
	for id := range k.keys {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Overlaps reports whether any configured key equals the given secret bytes.
// Configuration uses it to keep the encryption keys independent of the HMAC key.
func (k *Keyring) Overlaps(secret []byte) bool {
	for _, raw := range k.raw {
		if len(raw) == len(secret) && string(raw) == string(secret) {
			return true
		}
	}
	return false
}

// associatedData binds a ciphertext to one job and template: moving a
// ciphertext to another row, or re-labelling the template, fails to decrypt.
func associatedData(jobID, template string) []byte {
	return []byte(aadPrefix + jobID + "\x00" + template)
}

// seal encrypts plaintext with the active key: version byte, random nonce, sealed text.
func (k *Keyring) seal(jobID, template string, plaintext []byte) (ciphertext []byte, keyID string, err error) {
	aead := k.keys[k.active]
	out := make([]byte, 1+nonceBytes, 1+nonceBytes+len(plaintext)+aead.Overhead())
	out[0] = formatV1
	if _, err = rand.Read(out[1 : 1+nonceBytes]); err != nil {
		return nil, "", err
	}
	out = aead.Seal(out, out[1:1+nonceBytes], plaintext, associatedData(jobID, template))
	return out, k.active, nil
}

var (
	errUnknownKey = errors.New("outbox: unknown encryption key")
	errDecrypt    = errors.New("outbox: cannot decrypt job")
)

// open decrypts a job payload. An unknown key or a failed authentication (wrong
// key, altered ciphertext, wrong job or template) is an error, never a guess.
func (k *Keyring) open(keyID, jobID, template string, ciphertext []byte) ([]byte, error) {
	aead, ok := k.keys[keyID]
	if !ok {
		return nil, errUnknownKey
	}
	if len(ciphertext) < minSealedLen || ciphertext[0] != formatV1 {
		return nil, errDecrypt
	}
	nonce := ciphertext[1 : 1+nonceBytes]
	plain, err := aead.Open(nil, nonce, ciphertext[1+nonceBytes:], associatedData(jobID, template))
	if err != nil {
		return nil, errDecrypt
	}
	return plain, nil
}
