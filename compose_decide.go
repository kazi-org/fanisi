package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

const (
	composeFiniteChoiceMaxBytes   = 256 * 1024
	composeFiniteChoiceMinChoices = 2
	composeFiniteChoiceMaxChoices = 255
)

// validateFiniteChoiceRequest checks a finite closed-set decision request.
// Oversized inputs are rejected; requirements are never truncated.
func validateFiniteChoiceRequest(r FiniteChoiceRequest) error {
	if r.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("finite choice request schema_version must be %d", compositionSchemaVersion)
	}
	if strings.TrimSpace(r.Question) == "" {
		return errors.New("finite choice request question must be nonempty")
	}
	if strings.TrimSpace(r.State) == "" {
		return errors.New("finite choice request state must be nonempty")
	}
	if !simpleID(r.QuestionID) {
		return errors.New("finite choice request question_id must be a nonempty simple id")
	}
	if !simpleID(r.AbstainID) {
		return errors.New("finite choice request abstain_id must be a nonempty simple id")
	}
	n := len(r.Candidates)
	if n < composeFiniteChoiceMinChoices || n > composeFiniteChoiceMaxChoices {
		return fmt.Errorf("finite choice request must have %d..%d candidates including abstain", composeFiniteChoiceMinChoices, composeFiniteChoiceMaxChoices)
	}
	if _, ok := r.Candidates[r.AbstainID]; !ok {
		return errors.New("finite choice request abstain_id must be one of the candidates")
	}
	for id, label := range r.Candidates {
		if !simpleID(id) {
			return fmt.Errorf("finite choice candidate id %q must be a nonempty simple id", id)
		}
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("finite choice candidate %q label must be nonempty", id)
		}
	}
	seenEvidence := make(map[string]struct{}, len(r.EvidenceIDs))
	for _, id := range r.EvidenceIDs {
		if !simpleID(id) {
			return fmt.Errorf("finite choice evidence id %q must be a nonempty simple id", id)
		}
		if _, dup := seenEvidence[id]; dup {
			return fmt.Errorf("finite choice evidence id %q is duplicated", id)
		}
		seenEvidence[id] = struct{}{}
		content, ok := r.Evidence[id]
		if !ok {
			return fmt.Errorf("finite choice evidence id %q has no supplied content", id)
		}
		if strings.TrimSpace(content) == "" {
			return fmt.Errorf("finite choice evidence id %q content must be nonempty", id)
		}
	}
	for id, content := range r.Evidence {
		if !simpleID(id) {
			return fmt.Errorf("finite choice evidence map key %q must be a nonempty simple id", id)
		}
		if strings.TrimSpace(content) == "" {
			return fmt.Errorf("finite choice evidence map key %q content must be nonempty", id)
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("finite choice request encode: %w", err)
	}
	if len(raw) > composeFiniteChoiceMaxBytes {
		return fmt.Errorf("finite choice request exceeds %d bytes", composeFiniteChoiceMaxBytes)
	}
	return nil
}

// validateFiniteChoiceResult checks a finite choice against its closed candidate
// and evidence sets. Confidence never grants authority. Unsupported or shadow
// backends are rejected.
func validateFiniteChoiceResult(r FiniteChoiceRequest, result FiniteChoiceResult) error {
	if result.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("finite choice result schema_version must be %d", compositionSchemaVersion)
	}
	switch result.Backend {
	case BackendImport, BackendCommand:
		// v1 executable backends only
	case BackendGLM, BackendJevShadow:
		return fmt.Errorf("finite choice result backend %q is not activated in v1", result.Backend)
	case "":
		return errors.New("finite choice result backend must be set")
	default:
		return fmt.Errorf("finite choice result backend %q is unsupported", result.Backend)
	}
	if _, ok := r.Candidates[result.SelectedID]; !ok {
		return fmt.Errorf("finite choice selected_id %q is not a known candidate", result.SelectedID)
	}
	if result.Abstain {
		if result.SelectedID != r.AbstainID {
			return fmt.Errorf("finite choice abstain requires selected_id %q", r.AbstainID)
		}
	} else if result.SelectedID == r.AbstainID {
		return errors.New("finite choice selected abstain_id without abstain=true")
	}
	seenReason := make(map[string]struct{}, len(result.ReasonIDs))
	knownEvidence := make(map[string]struct{}, len(r.EvidenceIDs))
	for _, id := range r.EvidenceIDs {
		knownEvidence[id] = struct{}{}
	}
	for _, id := range result.ReasonIDs {
		if !simpleID(id) {
			return fmt.Errorf("finite choice reason id %q must be a nonempty simple id", id)
		}
		if _, dup := seenReason[id]; dup {
			return fmt.Errorf("finite choice reason id %q is duplicated", id)
		}
		seenReason[id] = struct{}{}
		if _, ok := knownEvidence[id]; !ok {
			return fmt.Errorf("finite choice reason id %q is unknown", id)
		}
	}
	if result.Confidence != nil {
		c := *result.Confidence
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return errors.New("finite choice confidence must be finite")
		}
		if c < 0 || c > 1 {
			return errors.New("finite choice confidence must be in [0,1]")
		}
	}
	return nil
}

// composeDecide runs a finite-choice decision through a backend after request
// validation. It never writes catalog, decision, manifest, or dispatch artifacts.
func composeDecide(ctx context.Context, backend DecisionBackend, req FiniteChoiceRequest) (FiniteChoiceResult, error) {
	if backend == nil {
		return FiniteChoiceResult{}, errors.New("nil decision backend")
	}
	if err := validateFiniteChoiceRequest(req); err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FiniteChoiceResult{}, err
	}
	result, err := backend.Choose(ctx, req)
	if err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := validateFiniteChoiceResult(req, result); err != nil {
		return FiniteChoiceResult{}, err
	}
	return result, nil
}

// composeRejectBackendSpoof rejects result metadata that claims an authority
// other than the concrete backend that produced the result.
func composeRejectBackendSpoof(claimed DecisionBackendKind, actual DecisionBackendKind) error {
	switch claimed {
	case "":
		return nil
	case actual:
		return nil
	case BackendGLM, BackendJevShadow:
		return fmt.Errorf("decision backend spoofing %q rejected", claimed)
	default:
		return fmt.Errorf("decision backend claimed %q but actual is %q", claimed, actual)
	}
}
