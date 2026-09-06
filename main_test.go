package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiteralKey(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		bad              bool
	}{
		{"plain", "OPENROUTER_API_KEY=test-value\n", "test-value", false},
		{"quoted", "export OPENROUTER_API_KEY='test-value'\n", "test-value", false},
		{"expansion", "OPENROUTER_API_KEY=$(echo test-value)\n", "", true},
		{"duplicate", "OPENROUTER_API_KEY=one\nOPENROUTER_API_KEY=two", "", true},
		{"missing", "SOMETHING=else", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := loadKey(path)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("unexpected parse result; error=%v", err)
			}
		})
	}
}

func TestScopedEdits(t *testing.T) {
	for _, tc := range []struct {
		name, path, old, new string
		bad                  bool
	}{
		{"unique", "file", "unique", "replaced", false},
		{"ambiguous", "file", "same", "new", true},
		{"stale", "file", "absent", "new", true},
		{"traversal", "../file", "unique", "new", true},
		{"absolute", "/etc/passwd", "unique", "new", true},
		{"empty", "file", "", "new", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			const before = "same same unique"
			if err := os.WriteFile(filepath.Join(root, "file"), []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			h := Harness{cfg: Config{Workspace: root, Output: root, WritePaths: []string{"file"}}}
			b, err := json.Marshal(map[string]any{"path": tc.path, "old": tc.old, "new": tc.new})
			if err != nil {
				t.Fatal(err)
			}
			call := Call{}
			call.Function.Name = "replace"
			call.Function.Arguments = string(b)
			_, _, err = h.execute(context.Background(), call)
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			got, err := os.ReadFile(filepath.Join(root, "file"))
			if err != nil {
				t.Fatal(err)
			}
			want := before
			if !tc.bad {
				want = strings.Replace(before, tc.old, tc.new, 1)
			}
			if string(got) != want {
				t.Fatal("unexpected mutation")
			}
		})
	}
}

func TestSymlinkScopeRejected(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	if _, err := scopedPath(root, "file", []string{"file"}); err == nil {
		t.Fatal("symlink permitted")
	}
}

func TestCheckerCannotVacuouslyPass(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := Harness{cfg: Config{Workspace: root, Output: root, VerifyCommand: []string{"sh", "-c", "printf failing; exit 2"}}}
	call := Call{}
	call.Function.Name = "verify"
	call.Function.Arguments = "{}"
	_, pass, err := h.execute(context.Background(), call)
	if err != nil || pass {
		t.Fatalf("failed checker passed: pass=%v err=%v", pass, err)
	}
}

func TestToolSchemaNoNullRequired(t *testing.T) {
	for _, tool := range toolSchemas() {
		b, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"required":null`) {
			t.Fatal("null required is invalid JSON Schema")
		}
	}
}

func TestOversizedReadReturnsBoundedContinuation(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat(strings.Repeat("x", 200)+"\n", 660)
	if err := os.WriteFile(filepath.Join(root, "file"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	h := Harness{cfg: Config{Workspace: root, Output: root, WritePaths: []string{"file"}}}
	call := Call{}
	call.Function.Name = "read"
	call.Function.Arguments = `{"path":"file","start":1,"lines":660}`
	out, _, err := h.execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if !result["truncated"].(bool) || len(result["text"].(string)) > 12000 || result["next_start"].(int) <= 1 {
		t.Fatalf("bad bounded result")
	}
	end := result["end"].(int)
	if result["text"] != strings.Join(strings.Split(content, "\n")[:end], "\n") {
		t.Fatal("read does not preserve the exact source prefix")
	}
}
