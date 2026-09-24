package policy

import (
	"errors"
	"testing"
)

func TestTrustLevelsMatchDocumentedOrder(t *testing.T) {
	// Pins spec §6.2 ranks 1–8. Inserting or renumbering a level must break
	// here first, before any comparison logic can silently reorder trust.
	for _, tt := range []struct {
		level TrustLevel
		want  uint8
	}{
		{TrustSystemPolicy, 1}, {TrustCurrentRequest, 2}, {TrustSessionConfirmed, 3},
		{TrustConfirmedFact, 4}, {TrustApprovedProcedure, 5}, {TrustEpisodicMemory, 6},
		{TrustExternalKnowledge, 7}, {TrustModelProposal, 8},
	} {
		if uint8(tt.level) != tt.want {
			t.Fatalf("trust level %d: got rank %d, want %d", tt.level, uint8(tt.level), tt.want)
		}
		if !tt.level.Valid() {
			t.Fatalf("trust level %d must be valid", tt.level)
		}
	}
	if TrustUnspecified.Valid() {
		t.Fatal("unspecified trust level must be invalid")
	}
}

func TestPickResolvesFullOrdering(t *testing.T) {
	// Complete ordering check over all 64 valid pairs: the higher-trust (lower
	// ranked) side wins, and equal ranks are ambiguous, never silently picked.
	for a := TrustSystemPolicy; a <= TrustModelProposal; a++ {
		for b := TrustSystemPolicy; b <= TrustModelProposal; b++ {
			got, err := Pick(a, b)
			switch {
			case a == b:
				if !errors.Is(err, ErrAmbiguousTrust) || got != TrustUnspecified {
					t.Fatalf("Pick(%d,%d): want ambiguity, got %d, %v", a, b, got, err)
				}
			case a < b:
				if err != nil || got != a {
					t.Fatalf("Pick(%d,%d): want %d, got %d, %v", a, b, a, got, err)
				}
			default:
				if err != nil || got != b {
					t.Fatalf("Pick(%d,%d): want %d, got %d, %v", a, b, b, got, err)
				}
			}
		}
	}
}

func TestDocumentedPrecedence(t *testing.T) {
	// System policy and authorization outrank the current request (spec §5.1
	// immutable principle 1); the current request outranks historical
	// confirmed facts but never system policy (plan §8.4); model proposals
	// lose to every documented level (spec §6.2 rank 8).
	if got, err := Pick(TrustSystemPolicy, TrustCurrentRequest); err != nil || got != TrustSystemPolicy {
		t.Fatalf("system policy must outrank current request: got %d, %v", got, err)
	}
	if got, err := Pick(TrustCurrentRequest, TrustConfirmedFact); err != nil || got != TrustCurrentRequest {
		t.Fatalf("current request must outrank historical facts: got %d, %v", got, err)
	}
	if got, err := Pick(TrustCurrentRequest, TrustSystemPolicy); err != nil || got != TrustSystemPolicy {
		t.Fatalf("current request must never override system policy: got %d, %v", got, err)
	}
	for _, higher := range []TrustLevel{TrustSystemPolicy, TrustCurrentRequest, TrustSessionConfirmed, TrustConfirmedFact, TrustApprovedProcedure, TrustEpisodicMemory, TrustExternalKnowledge} {
		if got, err := Pick(TrustModelProposal, higher); err != nil || got != higher {
			t.Fatalf("proposal must lose to %d: got %d, %v", higher, got, err)
		}
	}
}

func TestUnknownTrustLevelsFailClosed(t *testing.T) {
	for _, bad := range []TrustLevel{TrustUnspecified, 9, 255} {
		if bad.Valid() {
			t.Fatalf("trust level %d must be invalid", bad)
		}
		for _, ok := range []TrustLevel{TrustSystemPolicy, TrustCurrentRequest, TrustModelProposal} {
			if _, err := Pick(bad, ok); !errors.Is(err, ErrInvalidTrustLevel) {
				t.Fatalf("Pick(%d,%d): want ErrInvalidTrustLevel, got %v", bad, ok, err)
			}
			if _, err := Pick(ok, bad); !errors.Is(err, ErrInvalidTrustLevel) {
				t.Fatalf("Pick(%d,%d): want ErrInvalidTrustLevel, got %v", ok, bad, err)
			}
		}
		// Invalid input rejects before ambiguity is even considered.
		if _, err := Pick(bad, bad); !errors.Is(err, ErrInvalidTrustLevel) {
			t.Fatalf("Pick(%d,%d): invalid input must dominate ambiguity, got %v", bad, bad, err)
		}
		if _, err := BarredFromMemoryPack(bad); !errors.Is(err, ErrInvalidTrustLevel) {
			t.Fatalf("BarredFromMemoryPack(%d): want error, got %v", bad, err)
		}
	}
}

func TestProposalExcludedFromMemoryPack(t *testing.T) {
	bars, err := BarredFromMemoryPack(TrustModelProposal)
	if err != nil || !bars {
		t.Fatalf("model proposal must be barred from MemoryPack: %v, %v", bars, err)
	}
	bars, err = BarredFromMemoryPack(TrustSystemPolicy)
	if err != nil || !bars {
		t.Fatalf("system policy belongs outside MemoryPack: %v, %v", bars, err)
	}
	for l := TrustCurrentRequest; l < TrustModelProposal; l++ {
		if bars, err := BarredFromMemoryPack(l); err != nil || bars {
			t.Fatalf("level %d is not categorically barred: %v, %v", l, bars, err)
		}
		if got, err := Pick(TrustModelProposal, l); err != nil || got != l {
			t.Fatalf("proposal must lose to %d: got %d, %v", l, got, err)
		}
		if got, err := Pick(l, TrustModelProposal); err != nil || got != l {
			t.Fatalf("proposal must lose to %d: got %d, %v", l, got, err)
		}
	}
	if _, err := Pick(TrustModelProposal, TrustModelProposal); !errors.Is(err, ErrAmbiguousTrust) {
		t.Fatalf("proposal vs proposal must not resolve to a winner: %v", err)
	}
}

func TestPickNeverConfirmsProposal(t *testing.T) {
	// No implicit confirmation: a precedence win for the opponent never
	// promotes the proposal. The losing model proposal stays categorically
	// barred; a system-policy winner is separately kept outside MemoryPack,
	// and every other winner still needs independent status/authorization checks.
	winner, err := Pick(TrustModelProposal, TrustConfirmedFact)
	if err != nil || winner != TrustConfirmedFact {
		t.Fatalf("pick against a confirmed fact: got %d, %v", winner, err)
	}
	if bars, err := BarredFromMemoryPack(TrustModelProposal); err != nil || !bars {
		t.Fatalf("losing proposal must remain barred: %v, %v", bars, err)
	}
	for a := TrustSystemPolicy; a <= TrustModelProposal; a++ {
		for b := TrustSystemPolicy; b <= TrustModelProposal; b++ {
			got, err := Pick(a, b)
			if err != nil {
				continue
			}
			if got == TrustModelProposal {
				t.Fatalf("Pick(%d,%d) returned an unconfirmed proposal as winner", a, b)
			}
			bars, err := BarredFromMemoryPack(got)
			if err != nil || bars != (got == TrustSystemPolicy) {
				t.Fatalf("winner %d: system policy alone is trust-barred: %v, %v", got, bars, err)
			}
		}
	}
}
