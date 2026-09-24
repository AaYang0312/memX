package conversation

import (
	"errors"
	"math"
	"testing"
)

func TestAppendSeqPrecheck(t *testing.T) {
	for _, tt := range []struct {
		name               string
		status             Status
		head, expectedHead uint64
		wantSeq            uint64
		wantErr            error
	}{
		{"first append from empty head", Active, 0, 0, 1, nil},
		{"later append continues head", Active, 5, 5, 6, nil},
		{"stale expected head", Active, 5, 4, 0, ErrSeqMismatch},
		{"future expected head", Active, 5, 6, 0, ErrSeqMismatch},
		{"head at max uint64 must not wrap", Active, math.MaxUint64, math.MaxUint64, 0, ErrSeqExhausted},
		{"max head with stale expectation", Active, math.MaxUint64, 0, 0, ErrSeqMismatch},
		{"deleted conversation stays closed", Deleted, 3, 3, 0, ErrConversationDeleted},
		{"archived conversation fails closed", Archived, 3, 3, 0, ErrConversationArchived},
		{"zero status fails closed", Status(0), 3, 3, 0, ErrInvalidStatus},
		{"unknown status fails closed", Status(4), 3, 3, 0, ErrInvalidStatus},
		{"max unknown status fails closed", Status(math.MaxUint8), 3, 3, 0, ErrInvalidStatus},
	} {
		t.Run(tt.name, func(t *testing.T) {
			seq, err := AppendSeq(tt.status, tt.head, tt.expectedHead)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if seq != tt.wantSeq {
				t.Fatalf("got seq %d, want %d", seq, tt.wantSeq)
			}
		})
	}
}

func TestNextSummaryCheckpointPrecheck(t *testing.T) {
	for _, tt := range []struct {
		name                                  string
		status                                Status
		currentCheckpoint, expectedCheckpoint uint64
		from, to, head                        uint64
		wantCheckpoint                        uint64
		wantErr                               error
	}{
		{"first checkpoint covers from 1", Active, 0, 0, 1, 3, 5, 3, nil},
		{"extend coverage by one turn", Active, 3, 3, 4, 4, 4, 4, nil},
		{"extend coverage below head", Active, 3, 3, 4, 6, 9, 6, nil},
		{"stale checkpoint CAS", Active, 3, 2, 4, 5, 9, 0, ErrCheckpointStale},
		{"future checkpoint CAS", Active, 3, 4, 4, 5, 9, 0, ErrCheckpointStale},
		{"overlap with covered prefix", Active, 3, 3, 3, 5, 9, 0, ErrCheckpointOverlap},
		{"overlap below covered prefix", Active, 3, 3, 1, 5, 9, 0, ErrCheckpointOverlap},
		{"gap after covered prefix", Active, 3, 3, 5, 6, 9, 0, ErrCheckpointGap},
		{"range exceeds head", Active, 3, 3, 4, 7, 6, 0, ErrCheckpointBeyondHead},
		{"empty conversation has no head", Active, 0, 0, 1, 1, 0, 0, ErrCheckpointBeyondHead},
		{"zero from seq is invalid", Active, 0, 0, 0, 3, 5, 0, ErrCheckpointInvalid},
		{"inverted range is invalid", Active, 3, 3, 5, 4, 9, 0, ErrCheckpointInvalid},
		{"max checkpoint cannot advance", Active, math.MaxUint64, math.MaxUint64, 0, 0, 0, 0, ErrCheckpointExhausted},
		{"deleted conversation stays closed", Deleted, 3, 3, 4, 5, 9, 0, ErrConversationDeleted},
		{"archived conversation fails closed", Archived, 3, 3, 4, 5, 9, 0, ErrConversationArchived},
		{"unknown status fails closed", Status(9), 3, 3, 4, 5, 9, 0, ErrInvalidStatus},
	} {
		t.Run(tt.name, func(t *testing.T) {
			checkpoint, err := NextSummaryCheckpoint(tt.status, tt.currentCheckpoint, tt.expectedCheckpoint, tt.from, tt.to, tt.head)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if checkpoint != tt.wantCheckpoint {
				t.Fatalf("got checkpoint %d, want %d", checkpoint, tt.wantCheckpoint)
			}
		})
	}
}

func TestNextDeletionEpochPrecheck(t *testing.T) {
	for _, tt := range []struct {
		name         string
		currentEpoch uint64
		wantEpoch    uint64
		wantErr      error
	}{
		{"first deletion leaves envelope epoch 0", 0, 1, nil},
		{"later deletion increments observed epoch", 7, 8, nil},
		{"max epoch must not wrap old events into currency", math.MaxUint64, 0, ErrEpochExhausted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			epoch, err := NextDeletionEpoch(tt.currentEpoch)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if epoch != tt.wantEpoch {
				t.Fatalf("got epoch %d, want %d", epoch, tt.wantEpoch)
			}
		})
	}
}
