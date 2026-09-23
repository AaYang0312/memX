package ref

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpaqueRefsRoundTripAndKinds(t *testing.T) {
	tenant, err := NewTenantRef()
	if err != nil {
		t.Fatal(err)
	}
	subject, err := NewSubjectRef()
	if err != nil {
		t.Fatal(err)
	}
	fact, err := NewFactRef()
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := NewProposalRef()
	if err != nil {
		t.Fatal(err)
	}
	if tenant.IsZero() || subject.IsZero() || fact.IsZero() || proposal.IsZero() {
		t.Fatal("zero ref generated")
	}
	for _, text := range []string{tenant.String(), subject.String(), fact.String(), proposal.String()} {
		if strings.Contains(text, "@") || strings.Contains(text, "synthetic-user") {
			t.Fatalf("ref contains raw identity: %q", text)
		}
	}
	if parsed, err := ParseTenantRef(tenant.String()); err != nil || parsed != tenant {
		t.Fatal("tenant round trip failed")
	}
	if parsed, err := ParseSubjectRef(subject.String()); err != nil || parsed != subject {
		t.Fatal("subject round trip failed")
	}
	if parsed, err := ParseFactRef(fact.String()); err != nil || parsed != fact {
		t.Fatal("fact round trip failed")
	}
	if parsed, err := ParseProposalRef(proposal.String()); err != nil || parsed != proposal {
		t.Fatal("proposal round trip failed")
	}
	if _, err := ParseFactRef(proposal.String()); err == nil {
		t.Fatal("proposal ref accepted as fact ref")
	}
	if _, err := ParseProposalRef(fact.String()); err == nil {
		t.Fatal("fact ref accepted as proposal ref")
	}
}

func TestRejectRawIDsAndMalformedRefs(t *testing.T) {
	for _, candidate := range []string{"", "user@example.com", "+12025550123", "tenant-1", "tn_", "tn_!!!!!!!!!!!!!!!!!!!!!!", "tn_AAAAAAAAAAAAAAAAAAAAAA", "tn_AAAAAAAAAAAAAAAAAAAAAA=", "TN_AAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := ParseTenantRef(candidate); err == nil {
			t.Errorf("accepted %q", candidate)
		}
	}
	if _, err := json.Marshal(TenantRef{}); err == nil {
		t.Fatal("zero ref must not serialize as {}")
	}
	if _, err := json.Marshal(FactRef{}); err == nil {
		t.Fatal("transport serialization must await schema review")
	}
}
