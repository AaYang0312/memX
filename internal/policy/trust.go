// Package policy holds pure, offline, fail-closed precedence and sensitivity
// checks for the local prototype: the eight-level trust order (spec §6.2) and
// sensitivity classes and content markers (spec §17.2, data-classification
// §2). No function performs IO, storage, networking, authorization, outbound
// transfer, indexing, projection writes, or confirmation, and none constructs
// a confirmed fact or a MemoryPack. Every result is a NECESSARY but never
// SUFFICIENT precondition: canonical revalidation, authorization, deletion
// fences, TTL and projection receipts still apply independently, and the
// G4/G5/G6/G-M gates remain unapproved. This is not a Task 2 contract, not a
// public JSON contract, and not production approval.
package policy

import "errors"

// TrustLevel is the closed eight-level trust order of spec §6.2, numbered
// from highest (1) to lowest (8); a lower numeric value is more trusted. The
// zero value is deliberately invalid so an unset level fails closed instead
// of silently comparing as an extreme.
type TrustLevel uint8

const (
	TrustUnspecified       TrustLevel = iota // 0: invalid, rejected before any comparison
	TrustSystemPolicy                        // 1: system policy, authorization, server-deterministic facts
	TrustCurrentRequest                      // 2: explicit values stated in the current request
	TrustSessionConfirmed                    // 3: values explicitly confirmed within the current session
	TrustConfirmedFact                       // 4: non-expired confirmed facts in canonical storage
	TrustApprovedProcedure                   // 5: human-approved procedural memory
	TrustEpisodicMemory                      // 6: historical episodic memory
	TrustExternalKnowledge                   // 7: external knowledge and semantic-similarity content
	TrustModelProposal                       // 8: model-inferred proposals; unconfirmed, lowest
)

var (
	// ErrInvalidTrustLevel reports an unset or unknown trust level. Unknown
	// values are never mapped to a nearest level.
	ErrInvalidTrustLevel = errors.New("invalid trust level")
	// ErrAmbiguousTrust reports candidates at the same valid trust level.
	// Equal-trust conflicts are dropped, downgraded or turned into
	// clarification questions (spec §6.2, §10.3); a comparator never silently
	// resolves them, and two conflicting confirmed facts are the fail-closed
	// invariant break of spec §10.3.
	ErrAmbiguousTrust = errors.New("equal trust levels cannot be resolved")
)

// Valid reports whether the level is one of the eight documented levels.
func (l TrustLevel) Valid() bool { return l >= TrustSystemPolicy && l <= TrustModelProposal }

// Pick resolves which of two candidates outranks the other. It is a pure
// precedence outcome for bounded pairwise comparison — never an admission,
// confirmation, authorization or release decision. Rejection-first: any
// invalid level rejects before comparison, and equal valid levels reject as
// ambiguous instead of silently picking a side.
//
// TrustModelProposal can never win. That does not classify every possible
// pending proposal: proposal status must independently exclude all proposals,
// regardless of their source. A Pick result is never a MemoryPack admission;
// canonical status, authorization, sensitivity and TTL checks remain mandatory
// (spec §10.1, §10.3).
func Pick(a, b TrustLevel) (TrustLevel, error) {
	if !a.Valid() || !b.Valid() {
		return TrustUnspecified, ErrInvalidTrustLevel
	}
	if a == b {
		return TrustUnspecified, ErrAmbiguousTrust
	}
	if a < b {
		return a, nil
	}
	return b, nil
}

// BarredFromMemoryPack reports categorical exclusions known from trust level
// alone. Rank 1 is an authority boundary, not a MemoryPack item: system
// policy/authorization are enforced outside the pack (spec §17.3). A server
// deterministic fact may appear only after separate confirmation and canonical
// validation as a rank-4 confirmed fact, never as rank-1 policy content.
// Model proposals never enter the prompt (§19.2). Other unconfirmed proposals
// must also be excluded by independent canonical
// status checks, whatever their trust source. A false result never grants
// admission: authorization, sensitivity, TTL and deletion fences still apply.
func BarredFromMemoryPack(l TrustLevel) (bool, error) {
	if !l.Valid() {
		return false, ErrInvalidTrustLevel
	}
	return l == TrustSystemPolicy || l == TrustModelProposal, nil
}
