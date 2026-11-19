// Package refs generates and validates the opaque references Timeline rows expose
// instead of database ids.
//
// The alphabet and length are repeated in the schema's CHECK constraints
// (migrations/timeline/*_record_refs_mutations.sql), so a ref generated here that
// does not match them is rejected on insert, and a ref accepted here that the
// database could not hold is a receipt that can never be replayed. Generator and
// validator therefore live together and are asserted against those constraints.
package refs

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// Length is the number of alphabet characters after the prefix. 128 bits of entropy
// encode to 26 base32 characters, which is what the CHECK constraints require.
const Length = 26

// Alphabet is Crockford-style base32: digits and lowercase letters without i, l, o
// and u, so a ref cannot be misread for another or for a word.
const Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

var encoding = base32.NewEncoding(Alphabet).WithPadding(base32.NoPadding)

// New returns prefix followed by Length random alphabet characters, e.g. "med_01…".
func New(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + encoding.EncodeToString(b[:])[:Length]
}

// Valid reports whether ref is prefix followed by exactly Length alphabet characters.
func Valid(ref, prefix string) bool {
	if len(ref) != len(prefix)+Length || !strings.HasPrefix(ref, prefix) {
		return false
	}
	for _, c := range ref[len(prefix):] {
		if !strings.ContainsRune(Alphabet, c) {
			return false
		}
	}
	return true
}
