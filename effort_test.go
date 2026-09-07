package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEffortAttribution(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "import.json")
	e := Effort{SchemaVersion: 1, ID: "prep", Study: "synthetic", Role: "preparation", Source: "snapshot", From: time.Unix(10, 0).UTC(), To: time.Unix(20, 0).UTC(), Coverage: "partial", Allocation: "study"}
	put := func() {
		t.Helper()
		if err := writeJSON(file, e); err != nil {
			t.Fatal(err)
		}
	}
	put()
	if err := importEffort(root, file); err != nil {
		t.Fatal(err)
	}
	if err := importEffort(root, file); err != nil {
		t.Fatal(err)
	}
	records, err := loadEfforts(root)
	if err != nil || len(records) != 1 || records[0].Cost != nil {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	e.ID = "overlap"
	put()
	if err := importEffort(root, file); err == nil {
		t.Fatal("overlap accepted")
	}
	e.ID = "prep"
	e.Source = "different"
	put()
	if err := importEffort(root, file); err == nil {
		t.Fatal("conflicting ID accepted")
	}
	e.Tokens = &coordinatorTokens{Input: 10, Cached: 5, Output: 3, Reasoning: 2, Total: 15}
	if err := validateEffort(e); err == nil {
		t.Fatal("reasoning double count accepted")
	}
	e.Tokens.Total = 13
	if err := validateEffort(e); err != nil {
		t.Fatal(err)
	}
	e.Tokens.Cached = 11
	if err := validateEffort(e); err == nil {
		t.Fatal("invalid cached subset accepted")
	}
}

func TestEffortCLIReportReload(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "attempt")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "attempt.json"), Attempt{SchemaVersion: 1, TaskID: "task", Arm: "fanisi"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "effort.json")
	e := Effort{SchemaVersion: 1, ID: "review", Study: "study", Task: "task", Attempt: "attempt", Role: "reviewer", Source: "review-source", From: time.Unix(1, 0).UTC(), To: time.Unix(20, 0).UTC(), Coverage: "complete", Allocation: "exclusive"}
	seconds := 12.0
	e.Seconds = &seconds
	if err := writeJSON(file, e); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := mainContext(context.Background(), []string{"import-effort", root, file}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := report(root, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Effort   map[string]EffortTotal `json:"effort"`
		Delivery DeliveryReport         `json:"delivery"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	r := got.Effort["reviewer"]
	if r.Records != 1 || r.ActiveSeconds != 12 || r.Cost != nil || got.Delivery.TotalCost != nil {
		t.Fatalf("report: %s", out.Bytes())
	}
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mainContext(context.Background(), []string{"import-effort", root, file}); err == nil {
		t.Fatal("malformed JSON accepted")
	}
}
