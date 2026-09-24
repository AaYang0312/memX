// Package conversation holds the personal-stage Task 2 seam of pure,
// fail-closed precondition checks for the canonical conversation sequence,
// summary checkpoint and deletion epoch (design spec §8.1, §8.2, §8.2.1,
// §12.1, §14.2, §17.4; ADR 0001 §5.1, §8).
//
// Every return value is only a precondition. A passing check is NEVER
// authorization, never a committed append, never a summary commit and never
// a deletion receipt. Ownership checks, the idempotency ledger, the
// deletion-fence lookup, and the PostgreSQL transaction that CASes
// memory_conversations and inserts the turn, summary or fence together with
// its outbox event remain separate mandatory steps. No method touches
// PostgreSQL, Kafka, Redis or the outbox.
package conversation

import (
	"errors"
	"math"
)

// Status mirrors memory_conversations.status (design spec §8.1).
// Zero and unlisted values carry no approved semantics and fail closed.
type Status uint8

const (
	Active Status = iota + 1
	Archived
	Deleted
)

var (
	// ErrInvalidStatus: zero or unlisted status values have no approved
	// semantics, so every precondition fails closed.
	ErrInvalidStatus = errors.New("unknown conversation status")
	// Deleted subjects are unreadable and unwritable (design spec §17.4);
	// late events must not resurrect them.
	ErrConversationDeleted = errors.New("conversation is deleted")
	// Appends and checkpoint advances on archived conversations have no
	// approved semantics, so they stay closed instead of guessed.
	ErrConversationArchived = errors.New("conversation is archived")
	ErrSeqMismatch          = errors.New("expected conversation seq does not match current head")
	ErrSeqExhausted         = errors.New("conversation seq space exhausted")
	ErrCheckpointStale      = errors.New("expected summary checkpoint does not match current checkpoint")
	ErrCheckpointInvalid    = errors.New("invalid summary turn range")
	ErrCheckpointOverlap    = errors.New("summary range overlaps the covered prefix")
	ErrCheckpointGap        = errors.New("summary range leaves a gap after the checkpoint")
	ErrCheckpointBeyondHead = errors.New("summary range exceeds the conversation head")
	ErrCheckpointExhausted  = errors.New("summary checkpoint space exhausted")
	ErrEpochExhausted       = errors.New("deletion epoch space exhausted")
)

// AppendSeq derives the next turn seq under an expected-head CAS
// (design spec §12.1, §14.2). The conversation starts with head 0, so the
// first committed append gets seq 1 and every later seq is head+1 with no
// gap and no reuse of a deleted turn's seq (design spec §8.2). A stale
// expected value, a maxed-out head (wrapping to 0 would collide with the
// initial state) and deleted or archived conversations are rejected. The
// result is not a commit: only the canonical CAS inside the PG transaction
// advances the head.
func AppendSeq(status Status, head, expectedHead uint64) (uint64, error) {
	if err := writable(status); err != nil {
		return 0, err
	}
	if expectedHead != head {
		return 0, ErrSeqMismatch
	}
	if head == math.MaxUint64 {
		return 0, ErrSeqExhausted
	}
	return head + 1, nil
}

// NextSummaryCheckpoint validates one summary coverage range for the async
// summary worker (design spec §8.2.1). The range must start exactly at
// currentCheckpoint+1 (no gap before it, no overlap with the covered
// prefix), must end at or below the current conversation head, and must
// advance summary_checkpoint_seq under CAS. It returns the new checkpoint,
// which is the end of the covered range. Nothing is granted: only the PG
// transaction that writes the summary row, CASes the checkpoint and inserts
// the memory.summary.committed.v1 outbox event may commit it.
func NextSummaryCheckpoint(status Status, currentCheckpoint, expectedCheckpoint, from, to, head uint64) (uint64, error) {
	if err := writable(status); err != nil {
		return 0, err
	}
	if expectedCheckpoint != currentCheckpoint {
		return 0, ErrCheckpointStale
	}
	if currentCheckpoint == math.MaxUint64 {
		return 0, ErrCheckpointExhausted
	}
	if from == 0 || to < from {
		return 0, ErrCheckpointInvalid
	}
	switch next := currentCheckpoint + 1; {
	case from < next:
		return 0, ErrCheckpointOverlap
	case from > next:
		return 0, ErrCheckpointGap
	}
	if to > head {
		return 0, ErrCheckpointBeyondHead
	}
	return to, nil
}

// NextDeletionEpoch derives the epoch a deletion command must persist when
// it extends the persistent tenant+subject fence (design spec §17.4, ADR
// 0001 §8). The event envelope's epoch 0 denotes "no deletion yet", so the
// first deletion moves 0 to 1; the epoch is monotonic and must never wrap,
// because a wrapped epoch would let old events look current and resurrect
// deleted data. The returned epoch is a precondition for the deletion
// transaction, not a fence, not a tombstone and not a completion receipt.
func NextDeletionEpoch(currentEpoch uint64) (uint64, error) {
	if currentEpoch == math.MaxUint64 {
		return 0, ErrEpochExhausted
	}
	return currentEpoch + 1, nil
}

// writable fails closed for every status without approved write semantics.
func writable(status Status) error {
	switch status {
	case Active:
		return nil
	case Archived:
		return ErrConversationArchived
	case Deleted:
		return ErrConversationDeleted
	default:
		return ErrInvalidStatus
	}
}
