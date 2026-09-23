// Package projection offers a necessary, not sufficient, pure event precheck.
// A production consumer must also consult canonical PG, hold an inbox lease,
// perform a fenced conditional write, recheck after writes, and persist receipts.
package projection

// Stamp holds opaque metadata only; no document body or raw identifiers.
type Stamp struct {
	Epoch       uint64
	Seq         uint64
	Revision    uint64
	ContentHash [32]byte
}

type Verdict uint8

const (
	Quarantine Verdict = iota + 1 // malformed, contradictory or incomplete state
	Stale                         // never apply an old/foreign epoch or version
	NoOp                          // same event version and hash
	Eligible                      // may proceed to independent canonical/fence checks
)

// Precheck is only an early rejection filter. Eligible must NOT be treated as
// authority to index: canonical version, ownership, deletion fence, post-write
// compensation and durable receipt checks remain mandatory.
func Precheck(event, indexed Stamp, canonicalEpoch uint64, deleted bool) Verdict {
	if deleted || event.Epoch != canonicalEpoch {
		return Stale
	}
	if event.Seq == 0 || event.Revision == 0 || event.ContentHash == [32]byte{} {
		return Quarantine
	}
	if indexed == (Stamp{}) {
		return Eligible
	}
	if indexed.Seq == 0 || indexed.Revision == 0 || indexed.ContentHash == [32]byte{} {
		return Quarantine
	}
	if indexed.Epoch != event.Epoch {
		return Quarantine
	} // deletion/rebuild reconciliation required first
	if event.Seq < indexed.Seq || event.Revision < indexed.Revision {
		return Stale
	}
	if event.Seq == indexed.Seq && event.Revision == indexed.Revision {
		if event.ContentHash == indexed.ContentHash {
			return NoOp
		}
		return Quarantine
	}
	if event.Seq == indexed.Seq || event.Revision == indexed.Revision {
		return Stale
	}
	return Eligible
}
