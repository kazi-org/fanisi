package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	a := Attempt{SchemaVersion: 1, Base: base, PatchSHA: digest(patch), ConfigurationSHA: digest(raw), VerificationPassed: true, ScopeOK: true}
	if err := writeJSON(filepath.Join(dir, "attempt.json"), a); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "review-one.json"), Review{Decision: "accept", Kind: "agent", PatchSHA: a.PatchSHA}); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "candidate")
	l := Landing{SchemaVersion: 1, ID: "one", Repository: repo, TargetBase: base, Merge: git("rev-parse", "HEAD"), Review: "review-one.json", Evidence: "synthetic captured merge", Patch: a.PatchSHA}
	if err := verifyLanding(ctx, dir, l); err != nil {
		t.Fatal(err)
	}
	t.Run("changed landed implementation", func(t *testing.T) {
		write("wrong\n")
		git("add", ".")
		git("commit", "-m", "changed")
		bad := l
		bad.Merge = git("rev-parse", "HEAD")
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
		squash.Merge = git("rev-parse", "HEAD")
		if err := verifyLanding(ctx, dir, squash); err != nil {
			t.Fatal(err)
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
	})
}
