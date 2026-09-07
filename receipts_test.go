package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestReconciliationDeduplicatesAndRejectsIncompleteReceipts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		omitCost     bool
		wrongID      bool
		wantComplete bool
	}{{"complete", false, false, true}, {"missing_cost", true, false, false}, {"wrong_generation", false, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Multiple assistant blocks from one streamed generation are one charge.
			trace := "{\"type\":\"assistant\",\"message\":{\"id\":\"gen-1\"}}\n{\"type\":\"assistant\",\"message\":{\"id\":\"gen-1\"}}\n{\"type\":\"result\",\"is_error\":false}\n"
			if err := os.WriteFile(filepath.Join(dir, "claude-stream.jsonl"), []byte(trace), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Error("missing auth")
				}
				data := map[string]any{"id": "gen-1", "model": model, "native_tokens_prompt": 100, "native_tokens_completion": 20, "native_tokens_cached": 80, "native_tokens_reasoning": 10, "total_cost": 0.001}
				if tc.omitCost {
					delete(data, "total_cost")
				}
				if tc.wrongID {
					data["id"] = "other"
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: redirectTransport{target: target, base: http.DefaultTransport}}
			err = reconcileWithClient(context.Background(), dir, "fixture-key", client)
			if (err == nil) != tc.wantComplete {
				t.Fatalf("completion error=%v", err)
			}
			var ledger ReceiptLedger
			if err := readJSON(filepath.Join(dir, "provider-ledger.json"), &ledger); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("generation fetched %d times", calls)
			}
			if ledger.Complete != tc.wantComplete {
				t.Fatal("wrong receipt coverage")
			}
			if tc.wantComplete {
				if ledger.Cost == nil || *ledger.Cost != 0.001 || ledger.Tokens == nil || *ledger.Tokens != 120 {
					t.Fatalf("double-counted cached/reasoning tokens: %+v", ledger)
				}
			} else if ledger.Cost != nil || ledger.Tokens != nil {
				t.Fatal("missing receipt treated as zero cost")
			}
		})
	}
}
