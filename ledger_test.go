package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReportCountsFailedAttemptsAndSeparatesReviewKinds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i, tc := range []struct {
		status   string
		review   string
		cost     float64
		complete bool
	}{{"failed", "", 0.01, true}, {"verified_pending_review", "agent", 0.02, true}, {"failed", "", 0.005, false}} {
		dir := filepath.Join(root, string(rune('a'+i)))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		prep := 20.0
		a := Attempt{SchemaVersion: 1, TaskID: "one-task", Arm: "fanisi", Status: tc.status, PatchSHA: "patch", ScopeOK: tc.review != "", VerificationPassed: tc.review != "", ExecutionSeconds: 10, PreparationSeconds: &prep}
		if err := writeJSON(filepath.Join(dir, "attempt.json"), a); err != nil {
			t.Fatal(err)
		}
		ledger := ReceiptLedger{SchemaVersion: 1, KnownCost: tc.cost, KnownTokens: 100, Complete: tc.complete}
		if err := writeJSON(filepath.Join(dir, "provider-ledger.json"), ledger); err != nil {
			t.Fatal(err)
		}
		reviewSeconds := 30.0
		if tc.review != "" {
			if err := writeJSON(filepath.Join(dir, "review-1.json"), Review{SchemaVersion: 1, At: time.Now(), Decision: "accept", Kind: tc.review, Seconds: &reviewSeconds, PatchSHA: "patch"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var out bytes.Buffer
	if err := report(root, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Arms []ArmReport `json:"arms"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	a := got.Arms[0]
	if a.Attempts != 3 || a.Tasks != 1 || a.Accepted != 1 || a.AgentAccepted != 1 || a.HumanAccepted != 0 || a.KnownTokens != 300 || a.ExecutionSeconds != 30 || a.PreparationSeconds != 20 || a.ReviewSeconds != 30 {
		t.Fatalf("incorrect attempt aggregation: %+v", a)
	}
	if a.CostPerAccepted != nil || a.Unresolved != 1 {
		t.Fatal("incomplete receipt coverage produced a cost-per-accepted claim")
	}
	if a.KnownCost < 0.03499 || a.KnownCost > 0.03501 {
		t.Fatalf("failed attempt cost disappeared: %f", a.KnownCost)
	}
}
