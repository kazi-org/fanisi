package main

import (
	"path/filepath"
	"testing"
)

func TestProfileDeduplicatesAndPreservesUnknownUsage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	response := map[string]any{"id": "gen-one", "provider": "Z.AI", "model": model, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120, "cost": 0.001}}
	for _, name := range []string{"call-01-response.json", "call-02-response.json"} {
		if err := writeJSON(filepath.Join(dir, name), response); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(dir, "call-03-response.json"), map[string]any{"error": "failed upstream"}); err != nil {
		t.Fatal(err)
	}
	if err := profile(dir); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := readJSON(filepath.Join(dir, "profile.json"), &got); err != nil {
		t.Fatal(err)
	}
	if got["calls"] != float64(1) || got["usage_complete"] != false || got["cached_counts_complete"] != false || got["reasoning_counts_complete"] != false {
		t.Fatalf("incorrect coverage: %+v", got)
	}
	totals := got["usage_known_subtotals"].(map[string]any)
	if totals["input_tokens_including_cache"] != float64(100) || totals["cost_usd"] != 0.001 {
		t.Fatal("duplicated provider generation counted twice")
	}
}

func TestProfileMissingResponseIsUnresolved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := writeJSON(filepath.Join(dir, "call-01-response.json"), map[string]any{"id": "gen-one", "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2, "cost": 0.001}}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "call-02-request.json"), map[string]any{"messages": []any{}}); err != nil {
		t.Fatal(err)
	}
	if err := profile(dir); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := readJSON(filepath.Join(dir, "profile.json"), &got); err != nil {
		t.Fatal(err)
	}
	if got["usage_complete"] != false || len(got["unresolved_responses"].([]any)) != 1 {
		t.Fatalf("missing HTTP response incorrectly counted as complete: %+v", got)
	}
}
