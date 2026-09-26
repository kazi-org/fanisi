package main

import (
	"fmt"
	"strings"
)

const (
	productKindReal         = "real"
	productKindFixture      = "fixture"
	productKindWorktree     = "worktree"
	productKindHypothetical = "hypothetical"
)

// validateEvidenceForVerdict checks structural A/B/C eligibility against the
// trusted evidence registry. Decision- or model-supplied EvidenceRef bodies are
// not authority: only IDs are taken from refs, and ProductKinds / IDs / maturity
// claims are read from known. Semantic honesty remains operator review.
func validateEvidenceForVerdict(v Verdict, refs []EvidenceRef, known map[string]EvidenceRef) error {
	switch v {
	case VerdictProposeAMSL:
		if len(refs) == 0 {
			return fmt.Errorf("propose_amsl requires evidence references")
		}
		var trusted []EvidenceRef
		for _, ref := range refs {
			id := strings.TrimSpace(ref.ID)
			if id == "" {
				return fmt.Errorf("evidence reference missing id")
			}
			got, ok := known[id]
			if !ok {
				return fmt.Errorf("unknown evidence id %q", id)
			}
			trusted = append(trusted, got)
		}
		for _, ev := range trusted {
			if err := validateTrustedEvidenceStructure(ev); err != nil {
				return err
			}
		}
		return nil
	case VerdictReuseReference, VerdictAdaptLocal, VerdictProductLocal, VerdictDefer:
		for _, ref := range refs {
			id := strings.TrimSpace(ref.ID)
			if id == "" {
				return fmt.Errorf("evidence reference missing id")
			}
			if _, ok := known[id]; !ok {
				return fmt.Errorf("unknown evidence id %q", id)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown verdict %q", v)
	}
}

func validateTrustedEvidenceStructure(ev EvidenceRef) error {
	if strings.TrimSpace(ev.ID) == "" {
		return fmt.Errorf("trusted evidence missing id")
	}
	switch ev.Kind {
	case EvidenceA, EvidenceB, EvidenceC:
	default:
		return fmt.Errorf("evidence %q: propose_amsl requires kind A, B, or C", ev.ID)
	}
	if len(ev.SourceRefs) == 0 {
		return fmt.Errorf("evidence %q: missing source_refs", ev.ID)
	}
	realProducts, err := realProductIDs(ev)
	if err != nil {
		return err
	}
	switch ev.Kind {
	case EvidenceA:
		if len(realProducts) < 2 {
			return fmt.Errorf("evidence %q kind A requires >=2 distinct real product_ids", ev.ID)
		}
		impls := distinctNonEmpty(ev.ImplementationIDs)
		if len(impls) < 2 {
			return fmt.Errorf("evidence %q kind A requires >=2 distinct implementation_ids", ev.ID)
		}
	case EvidenceB:
		if len(realProducts) < 2 {
			return fmt.Errorf("evidence %q kind B requires >=2 distinct real product_ids", ev.ID)
		}
		impls := distinctNonEmpty(ev.ImplementationIDs)
		if len(impls) != 1 {
			return fmt.Errorf("evidence %q kind B requires exactly one implementation_id", ev.ID)
		}
	case EvidenceC:
		if len(realProducts) < 2 {
			return fmt.Errorf("evidence %q kind C requires >=2 distinct real product_ids", ev.ID)
		}
		mature := distinctNonEmpty(ev.MatureImplementationIDs)
		if len(mature) < 1 {
			return fmt.Errorf("evidence %q kind C requires mature_implementation_ids", ev.ID)
		}
		impls := distinctNonEmpty(ev.ImplementationIDs)
		if len(impls) < 1 {
			return fmt.Errorf("evidence %q kind C requires implementation_ids", ev.ID)
		}
		for _, m := range mature {
			found := false
			for _, id := range impls {
				if id == m {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("evidence %q kind C mature implementation %q not listed in implementation_ids", ev.ID, m)
			}
		}
	}
	return nil
}

// realProductIDs returns distinct product IDs explicitly marked real in the
// trusted ProductKinds map. Fixture/worktree/hypothetical do not count. Missing
// kinds never infer realness from ID spelling.
func realProductIDs(ev EvidenceRef) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, pid := range ev.ProductIDs {
		if strings.TrimSpace(pid) == "" {
			return nil, fmt.Errorf("evidence %q: empty product_id", ev.ID)
		}
		kind, ok := ev.ProductKinds[pid]
		if !ok {
			return nil, fmt.Errorf("evidence %q: product %q missing product_kinds entry (realness is never inferred from id spelling)", ev.ID, pid)
		}
		switch kind {
		case productKindReal:
			if _, dup := seen[pid]; dup {
				continue
			}
			seen[pid] = struct{}{}
			out = append(out, pid)
		case productKindFixture, productKindWorktree, productKindHypothetical:
			// Explicit non-real classifications do not count toward A/B/C.
			continue
		default:
			return nil, fmt.Errorf("evidence %q: product %q has unknown product_kinds %q", ev.ID, pid, kind)
		}
	}
	return out, nil
}

func distinctNonEmpty(ids []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
