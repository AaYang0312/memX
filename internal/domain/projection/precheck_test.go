package projection

import (
	"crypto/sha256"
	"testing"
)

func TestCanonicalFenceAndMonotonicProjectionPrecheck(t *testing.T) {
	old := Stamp{Epoch: 2, Seq: 7, Revision: 4, ContentHash: sha256.Sum256([]byte("old"))}
	next := Stamp{Epoch: 2, Seq: 8, Revision: 5, ContentHash: sha256.Sum256([]byte("next"))}
	for _, tt := range []struct {
		name           string
		event, indexed Stamp
		currentEpoch   uint64
		deleted        bool
		want           Verdict
	}{
		{"eligible", next, old, 2, false, Eligible},
		{"old epoch", next, old, 3, false, Stale},
		{"future epoch", next, old, 1, false, Stale},
		{"subject deleted", next, old, 2, true, Stale},
		{"duplicate", old, old, 2, false, NoOp},
		{"same version different content", Stamp{Epoch: 2, Seq: 7, Revision: 4, ContentHash: next.ContentHash}, old, 2, false, Quarantine},
		{"seq regression", Stamp{Epoch: 2, Seq: 6, Revision: 5, ContentHash: next.ContentHash}, old, 2, false, Stale},
		{"revision regression", Stamp{Epoch: 2, Seq: 8, Revision: 4, ContentHash: next.ContentHash}, old, 2, false, Stale},
		{"indexed older epoch needs deletion reconciliation", Stamp{Epoch: 3, Seq: 1, Revision: 1, ContentHash: next.ContentHash}, old, 3, false, Quarantine},
		{"missing hash", Stamp{Epoch: 2, Seq: 8, Revision: 5}, old, 2, false, Quarantine},
		{"missing seq", Stamp{Epoch: 2, Seq: 0, Revision: 5, ContentHash: next.ContentHash}, old, 2, false, Quarantine},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Precheck(tt.event, tt.indexed, tt.currentEpoch, tt.deleted); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
