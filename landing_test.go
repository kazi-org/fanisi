package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLandingContentIdentity(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		b, err := gitOutput(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	git("init")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "Fixture")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "code.txt"), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("before\n")
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	write("after\n")
	patch, err := gitOutput(ctx, repo, "diff", "--binary", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidate.patch"), patch, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := struct {
		Evaluation Evaluation
		Task       Config
	}{Evaluation{Repository: repo}, Config{WritePaths: []string{"code.txt"}}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "configuration.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := Attempt{TaskID: "task", Arm: "fanisi", RepairFrom: "initial", StartedAt: time.Unix(1, 0).UTC(), SchemaVersion: 1, Base: base, PatchSHA: digest(patch), ConfigurationSHA: digest(raw), VerificationPassed: true, ScopeOK: true}
	if err := writeJSON(filepath.Join(dir, "attempt.json"), a); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "review-one.json"), Review{SchemaVersion: 1, Reviewer: "external-fixture", Decision: "accept", Kind: "agent", PatchSHA: a.PatchSHA}); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "candidate")
	git("branch", "landed")
	l := Landing{IndependentReview: true, TargetRef: "refs/heads/landed", SchemaVersion: 1, ID: "one", Repository: repo, TargetBase: base, Merge: git("rev-parse", "HEAD"), Review: "review-one.json", Evidence: "synthetic captured merge", Patch: a.PatchSHA}
	if err := verifyLanding(ctx, dir, l); err != nil {
		t.Fatal(err)
	}

	t.Run("synthetic delivery arithmetic", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "landing.json")
		record := l
		record.At = time.Unix(31, 0).UTC()
		if err := writeJSON(file, record); err != nil {
			t.Fatal(err)
		}
		if err := importLanding(ctx, dir, file); err != nil {
			t.Fatal(err)
		}
		cost := 0.25
		input, output, cached, reasoning := 100, 10, 80, 4
		receipt := Receipt{ID: "generation", Provider: "fixture", Cost: &cost, Prompt: &input, Output: &output, Cached: &cached, Reasoning: &reasoning}
		if err := writeJSON(filepath.Join(dir, "provider-ledger.json"), ReceiptLedger{SchemaVersion: 1, Complete: true, Generations: []Receipt{receipt, receipt}}); err != nil {
			t.Fatal(err)
		}
		failed := t.TempDir()
		if err := writeJSON(filepath.Join(failed, "attempt.json"), Attempt{SchemaVersion: 1, TaskID: "task", Arm: "fanisi", Status: "failed"}); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(failed, "provider-ledger.json"), ReceiptLedger{SchemaVersion: 1, KnownCost: 0.5}); err != nil {
			t.Fatal(err)
		}
		got, err := deliveryReport(t.TempDir(), []string{filepath.Join(dir, "attempt.json"), filepath.Join(failed, "attempt.json")})
		if err != nil {
			t.Fatal(err)
		}
		if got.Attempts != 2 || got.Accepted != 1 || got.Assisted != 1 || got.Autonomous != 0 || got.KnownCost != 0.75 || got.Tokens != 110 || got.TotalCost != nil || got.CostPerAutonomous != nil || got.Elapsed["task"] != 30 {
			t.Fatalf("wrong delivery arithmetic: %+v", got)
		}
		if err := os.Remove(filepath.Join(dir, "landing-one.json")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("changed landed implementation", func(t *testing.T) {
		write("wrong\n")
		git("add", ".")
		git("commit", "-m", "changed")
		bad := l
		bad.Merge = git("rev-parse", "HEAD")
		git("branch", "changed-landing", bad.Merge)
		bad.TargetRef = "refs/heads/changed-landing"
		if err := verifyLanding(ctx, dir, bad); err == nil {
			t.Fatal("changed content accepted")
		}
	})
	t.Run("open PR", func(t *testing.T) {
		bad := l
		bad.Merge = ""
		if err := verifyLanding(ctx, dir, bad); err == nil {
			t.Fatal("open PR accepted")
		}
	})

	t.Run("unmerged valid commit", func(t *testing.T) {
		git("checkout", "-b", "unmerged-equivalent", base)
		write("after\n")
		git("add", ".")
		git("commit", "-m", "equivalent but unmerged")
		bad := l
		bad.Merge = git("rev-parse", "HEAD")
		bad.Evidence = "open PR"
		if err := verifyLanding(ctx, dir, bad); err == nil {
			t.Fatal("unmerged commit counted as landed")
		}
	})
	t.Run("wrong repository", func(t *testing.T) {
		bad := l
		bad.Repository = t.TempDir()
		if err := verifyLanding(ctx, dir, bad); err == nil {
			t.Fatal("wrong repo accepted")
		}
	})
	t.Run("equivalent squash", func(t *testing.T) {
		git("checkout", "-b", "squash", base)
		write("after\n")
		git("add", ".")
		git("commit", "-m", "squashed differently")
		squash := l
		squash.TargetRef = "refs/heads/squash"
		squash.Merge = git("rev-parse", "HEAD")
		if err := verifyLanding(ctx, dir, squash); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("intervening scoped base", func(t *testing.T) {
		bad := l
		bad.TargetBase = l.Merge
		if err := verifyLanding(ctx, dir, bad); err == nil {
			t.Fatal("intervening scoped change accepted")
		}
	})
	t.Run("amended reviewed patch", func(t *testing.T) {
		path := filepath.Join(dir, "candidate.patch")
		if err := os.WriteFile(path, []byte("amended"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(path, patch, 0600); err != nil {
				t.Error(err)
			}
		})
		if err := verifyLanding(ctx, dir, l); err == nil {
			t.Fatal("amended patch accepted")
		}
	})
	t.Run("duplicate and revocation", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "landing.json")
		if err := writeJSON(file, l); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := importLanding(ctx, dir, file); err != nil {
				t.Fatal(err)
			}
		}
		revoke := Landing{SchemaVersion: 1, ID: "revoke", Revokes: l.ID, Evidence: "regression"}
		if err := writeJSON(file, revoke); err != nil {
			t.Fatal(err)
		}
		if err := importLanding(ctx, dir, file); err != nil {
			t.Fatal(err)
		}
		got, err := deliveryReport(t.TempDir(), []string{filepath.Join(dir, "attempt.json")})
		if err != nil {
			t.Fatal(err)
		}
		if got.Accepted != 0 || got.VerifiedPending != 1 {
			t.Fatalf("revoked landing counted: %+v", got)
		}
	})
}
