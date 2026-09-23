// Package fact contains pure, fail-closed transition checks for a local spike.
// No method creates a confirmed fact or performs authorization, CAS, outbox
// writes, deletion, or projection effects. It is not a Task 2 contract.
package fact

import (
	"errors"
	"math"
	"time"
)

type ProposalStatus uint8

const (
	Pending ProposalStatus = iota + 1
	Confirmed
	Rejected
	Expired
)

type ReasonCode string

const PendingConflict ReasonCode = "pending_conflict" // not a proposal status

type FactStatus uint8

const (
	ConfirmedFact FactStatus = iota + 1
	Superseded
	Revoked
	Deleted
)

type RevocationReason string

const (
	RevokedExplicit          RevocationReason = "explicit"
	RevokedExpired           RevocationReason = "expired" // fact status is revoked, never expired
	RevokedSourceInvalidated RevocationReason = "source_invalidated"
	RevokedPolicyInvalidated RevocationReason = "policy_invalidated"
)

var (
	ErrConflict          = errors.New("revision conflict")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrApprovalRequired  = errors.New("confirmation gate not approved")
)

// NextProposal validates an expected-version transition; the confirmation
// path is deliberately closed until G4/G5 and canonical transaction work exist.
func NextProposal(current, target ProposalStatus, actualRevision, expectedRevision uint64, now, expiresAt time.Time) (uint64, error) {
	if actualRevision == 0 || actualRevision != expectedRevision || actualRevision == math.MaxUint64 {
		return 0, ErrConflict
	}
	if current != Pending || now.IsZero() || expiresAt.IsZero() {
		return 0, ErrInvalidTransition
	}
	if target == Confirmed {
		if !now.Before(expiresAt) {
			return 0, ErrInvalidTransition
		}
		return 0, ErrApprovalRequired
	}
	switch target {
	case Rejected:
		if now.Before(expiresAt) {
			return actualRevision + 1, nil
		}
	case Expired:
		if !now.Before(expiresAt) {
			return actualRevision + 1, nil
		}
	}
	return 0, ErrInvalidTransition
}

// NextFact models only terminal transitions of an already confirmed fact.
// It is necessary but NEVER sufficient for a write: PG auth/fence, active-key
// locking, revision audit, outbox, idempotency, and G5 still apply separately.
func NextFact(current, target FactStatus, actualRevision, expectedRevision uint64, reason RevocationReason) (uint64, error) {
	if actualRevision == 0 || actualRevision != expectedRevision || actualRevision == math.MaxUint64 {
		return 0, ErrConflict
	}
	if current != ConfirmedFact {
		return 0, ErrInvalidTransition
	}
	switch target {
	case Superseded, Deleted:
		if reason == "" {
			return actualRevision + 1, nil
		}
	case Revoked:
		if reason == RevokedExplicit || reason == RevokedExpired || reason == RevokedSourceInvalidated || reason == RevokedPolicyInvalidated {
			return actualRevision + 1, nil
		}
	}
	return 0, ErrInvalidTransition
}
