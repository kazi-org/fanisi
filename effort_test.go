package main

import (
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
