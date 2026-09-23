// Package ref provides opaque, kind-safe identifiers for the offline spike.
// JSON transport shapes and issuer/ownership policy are not frozen (G1/G4).
package ref

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

var ErrInvalidRef = errors.New("invalid opaque reference")
var ErrEntropy = errors.New("opaque reference generation failed")
var ErrTransport = errors.New("reference transport schema not frozen")

type opaque struct {
	id     [16]byte
	prefix string
}

func newOpaque(prefix string) (opaque, error) {
	var r opaque
	r.prefix = prefix
	if _, err := rand.Read(r.id[:]); err != nil {
		return opaque{}, ErrEntropy
	}
	if r.IsZero() {
		return opaque{}, ErrEntropy
	}
	return r, nil
}

func parseOpaque(text, prefix string) (opaque, error) {
	if !strings.HasPrefix(text, prefix) {
		return opaque{}, ErrInvalidRef
	}
	encoded := strings.TrimPrefix(text, prefix)
	if len(encoded) != base64.RawURLEncoding.EncodedLen(16) {
		return opaque{}, ErrInvalidRef
	}
	bytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(bytes) != 16 || base64.RawURLEncoding.EncodeToString(bytes) != encoded {
		return opaque{}, ErrInvalidRef
	}
	r := opaque{prefix: prefix}
	copy(r.id[:], bytes)
	if r.IsZero() {
		return opaque{}, ErrInvalidRef
	}
	return r, nil
}

func (r opaque) IsZero() bool { return r.id == [16]byte{} || r.prefix == "" }
func (r opaque) String() string {
	if r.IsZero() {
		return ""
	}
	return r.prefix + base64.RawURLEncoding.EncodeToString(r.id[:])
}

// No implicit JSON serialization before the public schema is reviewed.
func (opaque) MarshalJSON() ([]byte, error) { return nil, ErrTransport }

// Different types prevent accidental use of a proposal_ref as a fact_ref.
// Private embeddings prevent callers from constructing a nonzero raw identity.
type TenantRef struct{ opaque }
type SubjectRef struct{ opaque }
type FactRef struct{ opaque }
type ProposalRef struct{ opaque }

func NewTenantRef() (TenantRef, error)     { r, err := newOpaque("tn_"); return TenantRef{r}, err }
func NewSubjectRef() (SubjectRef, error)   { r, err := newOpaque("sb_"); return SubjectRef{r}, err }
func NewFactRef() (FactRef, error)         { r, err := newOpaque("fa_"); return FactRef{r}, err }
func NewProposalRef() (ProposalRef, error) { r, err := newOpaque("pr_"); return ProposalRef{r}, err }

func ParseTenantRef(s string) (TenantRef, error) {
	r, err := parseOpaque(s, "tn_")
	return TenantRef{r}, err
}
func ParseSubjectRef(s string) (SubjectRef, error) {
	r, err := parseOpaque(s, "sb_")
	return SubjectRef{r}, err
}
func ParseFactRef(s string) (FactRef, error) { r, err := parseOpaque(s, "fa_"); return FactRef{r}, err }
func ParseProposalRef(s string) (ProposalRef, error) {
	r, err := parseOpaque(s, "pr_")
	return ProposalRef{r}, err
}
