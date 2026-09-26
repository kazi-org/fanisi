package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadEvidenceFile(t *testing.T, path string) map[string]EvidenceRef {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []EvidenceRef
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]EvidenceRef{}
	for _, ev := range list {
		if _, dup := out[ev.ID]; dup {
			t.Fatalf("duplicate evidence id %q in %s", ev.ID, path)
		}
		out[ev.ID] = ev
	}
	return out
}

func TestComposePolicyEvidenceBValid(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/valid-b.json")
	refs := []EvidenceRef{{ID: "ev-b-thread"}}
	if err := validateEvidenceForVerdict(VerdictProposeAMSL, refs, known); err != nil {
		t.Fatal(err)
	}
}

func TestComposePolicyEvidenceAValid(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/valid-a.json")
	if err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-a-audio"}}, known); err != nil {
		t.Fatal(err)
	}
}

func TestComposePolicyEvidenceCValid(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/valid-c.json")
	if err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-c-session"}}, known); err != nil {
		t.Fatal(err)
	}
}

func TestComposePolicyRejectsFixtureProducts(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/invalid-fixture-products.json")
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-bad-fixture"}}, known)
	if err == nil || !strings.Contains(err.Error(), "real product") {
		t.Fatalf("expected fixture rejection, got %v", err)
	}
}

func TestComposePolicyRejectsWorktreeProducts(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/invalid-worktree-products.json")
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-bad-worktree"}}, known)
	if err == nil || !strings.Contains(err.Error(), "real product") {
		t.Fatalf("expected worktree rejection, got %v", err)
	}
}

func TestComposePolicyRejectsHypotheticalProducts(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/invalid-hypothetical-products.json")
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-bad-hyp"}}, known)
	if err == nil || !strings.Contains(err.Error(), "real product") {
		t.Fatalf("expected hypothetical rejection, got %v", err)
	}
}

func TestComposePolicyNeverInfersRealnessFromIDSpelling(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/invalid-inferred-realness.json")
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-bad-infer-real-name"}}, known)
	if err == nil || !strings.Contains(err.Error(), "never inferred") {
		t.Fatalf("expected missing product_kinds rejection, got %v", err)
	}
}

func TestComposePolicyUsesTrustedRegistryNotModelAssertions(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/valid-b.json")
	// Model-supplied body claims fixture products; trusted registry must win.
	forged := []EvidenceRef{{
		ID:                "ev-b-thread",
		Kind:              EvidenceB,
		ProductIDs:        []string{"fixture.a", "fixture.b"},
		ImplementationIDs: []string{"impl.thread.v1"},
		ProductKinds: map[string]string{
			"fixture.a": "fixture",
			"fixture.b": "fixture",
		},
	}}
	if err := validateEvidenceForVerdict(VerdictProposeAMSL, forged, known); err != nil {
		t.Fatalf("trusted registry should authorize despite forged body: %v", err)
	}

	tampered := known["ev-b-thread"]
	tampered.ProductKinds = map[string]string{
		"product.voice": "fixture",
		"product.notes": "fixture",
	}
	badKnown := map[string]EvidenceRef{"ev-b-thread": tampered}
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "ev-b-thread"}}, badKnown)
	if err == nil {
		t.Fatal("expected tampered trusted registry to fail")
	}
}

func TestComposePolicyUnknownEvidenceID(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/valid-b.json")
	err := validateEvidenceForVerdict(VerdictProposeAMSL, []EvidenceRef{{ID: "missing-ev"}}, known)
	if err == nil || !strings.Contains(err.Error(), "unknown evidence") {
		t.Fatalf("expected unknown evidence id, got %v", err)
	}
}

func TestComposePolicyAllVerdictsEvidenceGate(t *testing.T) {
	known := loadEvidenceFile(t, "testdata/compose/evidence/registry.json")
	cases := []struct {
		verdict Verdict
		refs    []EvidenceRef
		wantErr string
	}{
		{VerdictReuseReference, nil, ""},
		{VerdictAdaptLocal, []EvidenceRef{{ID: "ev-b-thread"}}, ""},
		{VerdictProductLocal, nil, ""},
		{VerdictDefer, []EvidenceRef{{ID: "ev-b-thread"}}, ""},
		{VerdictProposeAMSL, nil, "requires evidence"},
		{VerdictProposeAMSL, []EvidenceRef{{ID: "ev-b-thread"}}, ""},
		{Verdict("nope"), nil, "unknown verdict"},
	}
	for _, tc := range cases {
		err := validateEvidenceForVerdict(tc.verdict, tc.refs, known)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: unexpected err %v", tc.verdict, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: want substring %q got %v", tc.verdict, tc.wantErr, err)
		}
	}
}
