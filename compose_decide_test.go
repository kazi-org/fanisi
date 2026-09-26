package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestComposeDecideNativeModelPinUnchanged(t *testing.T) {
	if model != "z-ai/glm-5.3-flash" {
		t.Fatalf("native model pin changed: %q", model)
	}
}

func TestComposeValidateFiniteChoiceRequest(t *testing.T) {
	valid := composeTestFiniteChoiceRequest()
	if err := validateFiniteChoiceRequest(valid); err != nil {
		t.Fatalf("valid request: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*FiniteChoiceRequest)
		sub  string
	}{
		{"schema", func(r *FiniteChoiceRequest) { r.SchemaVersion = 99 }, "schema_version"},
		{"empty question", func(r *FiniteChoiceRequest) { r.Question = "  " }, "question"},
		{"empty state", func(r *FiniteChoiceRequest) { r.State = "" }, "state"},
		{"bad question id", func(r *FiniteChoiceRequest) { r.QuestionID = "bad id" }, "question_id"},
		{"one choice", func(r *FiniteChoiceRequest) {
			r.Candidates = map[string]string{r.AbstainID: "skip"}
		}, "candidates"},
		{"abstain missing", func(r *FiniteChoiceRequest) {
			delete(r.Candidates, r.AbstainID)
			r.Candidates["other"] = "x"
		}, "abstain_id"},
		{"empty label", func(r *FiniteChoiceRequest) { r.Candidates["reuse"] = " " }, "label"},
		{"missing evidence content", func(r *FiniteChoiceRequest) {
			r.EvidenceIDs = []string{"e1"}
			r.Evidence = map[string]string{}
		}, "supplied content"},
		{"empty evidence content", func(r *FiniteChoiceRequest) {
			r.EvidenceIDs = []string{"e1"}
			r.Evidence = map[string]string{"e1": "  "}
		}, "content must be nonempty"},
		{"dup evidence id", func(r *FiniteChoiceRequest) {
			r.EvidenceIDs = []string{"e1", "e1"}
			r.Evidence = map[string]string{"e1": "ok"}
		}, "duplicated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := composeTestFiniteChoiceRequest()
			tc.mut(&r)
			err := validateFiniteChoiceRequest(r)
			if err == nil || !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("got %v, want substring %q", err, tc.sub)
			}
		})
	}

	t.Run("oversized", func(t *testing.T) {
		r := composeTestFiniteChoiceRequest()
		r.State = strings.Repeat("x", composeFiniteChoiceMaxBytes)
		err := validateFiniteChoiceRequest(r)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("got %v, want exceeds", err)
		}
	})

	t.Run("max choices ok", func(t *testing.T) {
		r := composeTestFiniteChoiceRequest()
		r.Candidates = map[string]string{r.AbstainID: "skip"}
		for i := 0; i < composeFiniteChoiceMaxChoices-1; i++ {
			r.Candidates[simpleTestID("c", i)] = "label"
		}
		if err := validateFiniteChoiceRequest(r); err != nil {
			t.Fatalf("max choices: %v", err)
		}
		r.Candidates[simpleTestID("c", composeFiniteChoiceMaxChoices)] = "extra"
		err := validateFiniteChoiceRequest(r)
		if err == nil || !strings.Contains(err.Error(), "candidates") {
			t.Fatalf("got %v, want candidates bound", err)
		}
	})
}

func TestComposeValidateFiniteChoiceResult(t *testing.T) {
	req := composeTestFiniteChoiceRequest()
	req.EvidenceIDs = []string{"e1"}
	req.Evidence = map[string]string{"e1": "notes"}
	conf := 0.5
	ok := FiniteChoiceResult{
		SchemaVersion: compositionSchemaVersion,
		SelectedID:    "reuse",
		Abstain:       false,
		ReasonIDs:     []string{"e1"},
		Backend:       BackendImport,
		Confidence:    &conf,
	}
	if err := validateFiniteChoiceResult(req, ok); err != nil {
		t.Fatalf("valid result: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*FiniteChoiceResult)
		sub  string
	}{
		{"unknown selected", func(r *FiniteChoiceResult) { r.SelectedID = "nope" }, "not a known candidate"},
		{"abstain inconsistency", func(r *FiniteChoiceResult) {
			r.Abstain = true
			r.SelectedID = "reuse"
		}, "abstain"},
		{"selected abstain without flag", func(r *FiniteChoiceResult) {
			r.Abstain = false
			r.SelectedID = req.AbstainID
		}, "abstain=true"},
		{"unknown reason", func(r *FiniteChoiceResult) { r.ReasonIDs = []string{"missing"} }, "unknown"},
		{"jev shadow", func(r *FiniteChoiceResult) { r.Backend = BackendJevShadow }, "not activated"},
		{"glm", func(r *FiniteChoiceResult) { r.Backend = BackendGLM }, "not activated"},
		{"unsupported backend", func(r *FiniteChoiceResult) { r.Backend = DecisionBackendKind("cursor") }, "unsupported"},
		{"empty backend", func(r *FiniteChoiceResult) { r.Backend = "" }, "must be set"},
		{"nonfinite confidence", func(r *FiniteChoiceResult) {
			nan := math.NaN()
			r.Confidence = &nan
		}, "finite"},
		{"out of range confidence", func(r *FiniteChoiceResult) {
			hi := 1.5
			r.Confidence = &hi
		}, "[0,1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ok
			c := conf
			r.Confidence = &c
			tc.mut(&r)
			err := validateFiniteChoiceResult(req, r)
			if err == nil || !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("got %v, want substring %q", err, tc.sub)
			}
		})
	}
}

func TestComposeDecideDoesNotMutateCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	writeComposeTestJSON(t, path, FiniteChoiceResult{
		SchemaVersion: compositionSchemaVersion,
		SelectedID:    "reuse",
		Abstain:       false,
	})
	catalogPath := filepath.Join(dir, "catalog.json")
	writeComposeTestJSON(t, catalogPath, map[string]any{"untouched": true})
	before, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	backend := NewImportBackend(path)
	req := composeTestFiniteChoiceRequest()
	if _, err := composeDecide(context.Background(), backend, req); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("composeDecide mutated adjacent catalog file")
	}
}

func composeTestFiniteChoiceRequest() FiniteChoiceRequest {
	return FiniteChoiceRequest{
		SchemaVersion: compositionSchemaVersion,
		QuestionID:    "q1",
		Question:      "Which binding fits?",
		State:         "need durable thread store",
		Candidates: map[string]string{
			"reuse":   "reuse AMSL",
			"local":   "product local",
			"abstain": "cannot decide",
		},
		AbstainID:   "abstain",
		EvidenceIDs: nil,
		Evidence:    map[string]string{},
	}
}

func writeComposeTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func simpleTestID(prefix string, i int) string {
	return prefix + strconv.Itoa(i)
}
