package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchFindsLateSymbolsAndPagesWithoutChangingVerification(t *testing.T) {
	dir := t.TempDir()
	data := strings.Repeat("irrelevant line\n", 160) + "set -uo pipefail\n" + strings.Repeat("needle in source\n", 24)
	if err := os.WriteFile(filepath.Join(dir, "source.sh"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Workspace: dir, Output: dir, ReadPaths: []string{"source.sh"}}
	h := Harness{cfg: cfg, verified: map[string]string{"source.sh": digest([]byte(data))}}
	call := Call{}
	call.Function.Name = "search"
	call.Function.Arguments = `{"path":"source.sh","query":"set -u"}`
	result, pass, err := h.execute(context.Background(), call)
	if err != nil || pass {
		t.Fatalf("search failed or declared acceptance: %v", err)
	}
	got := result.(map[string]any)
	hits := got["matches"].([]SearchHit)
	if len(hits) != 1 || hits[0].Line != 161 || hits[0].Text != "set -uo pipefail" {
		t.Fatalf("wrong source location: %+v", hits)
	}
	if h.verified == nil {
		t.Fatal("read-only search invalidated verification")
	}
	result, err = searchFile(context.Background(), cfg, "source.sh", "needle", 1)
	if err != nil {
		t.Fatal(err)
	}
	first := result.(map[string]any)
	if len(first["matches"].([]SearchHit)) != 20 || first["truncated"] != true {
		t.Fatalf("missing result bound: %+v", first)
	}
	result, err = searchFile(context.Background(), cfg, "source.sh", "needle", first["next_start"].(int))
	if err != nil {
		t.Fatal(err)
	}
	last := result.(map[string]any)
	if len(last["matches"].([]SearchHit)) != 4 || last["truncated"] != false {
		t.Fatalf("paging lost or duplicated matches: %+v", last)
	}
	after, err := os.ReadFile(filepath.Join(dir, "source.sh"))
	if err != nil || string(after) != data {
		t.Fatal("search changed source")
	}
}

func TestSearchBoundsTextAndRejectsScopeEscapes(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	cfg := Config{Workspace: dir, ReadPaths: []string{"source.txt", "link.txt"}}
	data := strings.Repeat("界", 200) + "literal.*needle" + strings.Repeat("界", 200)
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := searchFile(context.Background(), cfg, "source.txt", "literal.*needle", 1)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	hit := got["matches"].([]SearchHit)[0]
	if !utf8.ValidString(hit.Text) || !strings.Contains(hit.Text, "literal.*needle") || len(hit.Text) > 300 {
		t.Fatalf("invalid bounded excerpt: %q", hit.Text)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret.txt", filepath.Join(outside, "secret.txt"), "link.txt", "not-scoped.txt"} {
		if _, err := searchFile(context.Background(), cfg, path, "needle", 1); err == nil {
			t.Fatalf("searched outside scope: %s", path)
		}
	}
	for _, q := range []string{"", strings.Repeat("x", 201), "two\nlines"} {
		if _, err := searchFile(context.Background(), cfg, "source.txt", q, 1); err == nil {
			t.Fatal("accepted invalid query")
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1000 {
		t.Fatalf("unbounded result: %d %v", len(encoded), err)
	}
}
