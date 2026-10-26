package world

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

// maxPayloadBytes bounds a decompressed stored document: far above the 2.5 MB of
// text plus framing a valid document can have, so only a damaged row hits it.
const maxPayloadBytes = 16 << 20

// Encode serialises a canonical document for storage: a gzip-compressed
// tripmap.v1.WorldDocument.
func Encode(doc *tripmapv1.WorldDocument) ([]byte, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err = zw.Write(raw); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Decode reads a stored document. It does not validate: stored documents were
// validated when they were written.
func Decode(payload []byte) (*tripmapv1.WorldDocument, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("world payload: %w", err)
	}
	defer func() { _ = zr.Close() }()
	raw, err := io.ReadAll(io.LimitReader(zr, maxPayloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("world payload: %w", err)
	}
	if len(raw) > maxPayloadBytes {
		return nil, fmt.Errorf("world payload: larger than %d bytes", maxPayloadBytes)
	}
	doc := &tripmapv1.WorldDocument{}
	if err = proto.Unmarshal(raw, doc); err != nil {
		return nil, fmt.Errorf("world payload: %w", err)
	}
	return doc, nil
}

// Digest is the request digest of ReplaceWorld: the hex SHA-256 over base_revision
// and the canonical document. The encoding is fixed here (a domain tag, the base
// revision, then every field in canonical order with explicit lengths), does not
// depend on protobuf serialisation, and is computed only by the server: the same
// request_id with the same document but another base_revision has another digest.
func Digest(baseRevision int64, canonical *tripmapv1.WorldDocument) string {
	h := sha256.New()
	var num [binary.MaxVarintLen64]byte
	putUint := func(v uint64) { h.Write(num[:binary.PutUvarint(num[:], v)]) }
	str := func(s string) {
		putUint(uint64(len(s)))
		_, _ = io.WriteString(h, s)
	}
	sint := func(v int64) { h.Write(num[:binary.PutVarint(num[:], v)]) }

	_, _ = io.WriteString(h, "world-replace/v1\x00")
	sint(baseRevision)
	str(canonical.GetKind())
	sint(int64(canonical.GetVersion()))
	putUint(uint64(len(canonical.GetCountries())))
	for _, c := range canonical.GetCountries() {
		str(c.GetCode())
		str(c.GetColor())
	}
	putUint(uint64(len(canonical.GetPlaces())))
	for _, p := range canonical.GetPlaces() {
		str(p.GetId())
		str(p.GetName())
		str(p.GetCountry())
		sint(int64(p.GetLatE6()))
		sint(int64(p.GetLngE6()))
		putUint(uint64(len(p.GetTrips())))
		for _, t := range p.GetTrips() {
			str(t.GetId())
			str(t.GetDate())
			str(t.GetNote())
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
