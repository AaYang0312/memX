package fact

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestProposalTransitionsFailClosed(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	if _, err := NextProposal(Pending, Confirmed, 3, 3, now, expires); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("unapproved confirmation accepted: %v", err)
	}
	if next, err := NextProposal(Pending, Rejected, 3, 3, now, expires); err != nil || next != 4 {
		t.Fatalf("reject: revision=%d err=%v", next, err)
	}
	if next, err := NextProposal(Pending, Expired, 3, 3, expires, expires); err != nil || next != 4 {
		t.Fatalf("expire: revision=%d err=%v", next, err)
	}
	for _, tt := range []struct {
		current, target  ProposalStatus
		actual, expected uint64
		at               time.Time
		expiry           time.Time
	}{
		{Pending, Rejected, 3, 2, now, expires},
		{Rejected, Pending, 3, 3, now, expires},
		{Confirmed, Rejected, 3, 3, now, expires},
		{Expired, Confirmed, 3, 3, now, expires},
		{Pending, Confirmed, 3, 3, expires, expires},
		{Pending, Expired, 3, 3, now, expires.Add(time.Second)},
		{Pending, Rejected, 3, 3, expires, expires},
		{Pending, Rejected, math.MaxUint64, math.MaxUint64, now, expires},
	} {
		if _, err := NextProposal(tt.current, tt.target, tt.actual, tt.expected, tt.at, tt.expiry); err == nil {
			t.Errorf("accepted transition %v -> %v", tt.current, tt.target)
		}
	}
	if string(PendingConflict) != "pending_conflict" {
		t.Fatal("pending_conflict must remain a reason code")
	}
}

func TestFactTerminalTransitionsAndReason(t *testing.T) {
	for _, tt := range []struct {
		target FactStatus
		reason RevocationReason
	}{
		{Superseded, ""}, {Revoked, RevokedExplicit}, {Revoked, RevokedExpired}, {Revoked, RevokedSourceInvalidated}, {Revoked, RevokedPolicyInvalidated}, {Deleted, ""},
	} {
		if next, err := NextFact(ConfirmedFact, tt.target, 6, 6, tt.reason); err != nil || next != 7 {
			t.Errorf("transition to %v: next=%d err=%v", tt.target, next, err)
		}
	}
	for _, tt := range []struct {
		current, target  FactStatus
		actual, expected uint64
		reason           RevocationReason
	}{
		{Revoked, ConfirmedFact, 6, 6, ""}, {Superseded, Revoked, 6, 6, RevokedExplicit},
		{Deleted, ConfirmedFact, 6, 6, ""}, {ConfirmedFact, ConfirmedFact, 6, 6, ""},
		{ConfirmedFact, Revoked, 6, 5, RevokedExplicit}, {ConfirmedFact, Revoked, 6, 6, ""},
		{ConfirmedFact, Superseded, 6, 6, RevokedExpired}, {ConfirmedFact, Deleted, 6, 6, RevokedExpired},
		{ConfirmedFact, Revoked, 6, 6, "unapproved"},
		{ConfirmedFact, Revoked, math.MaxUint64, math.MaxUint64, RevokedExpired},
	} {
		if _, err := NextFact(tt.current, tt.target, tt.actual, tt.expected, tt.reason); err == nil {
			t.Errorf("accepted transition %v -> %v", tt.current, tt.target)
		}
	}
}
