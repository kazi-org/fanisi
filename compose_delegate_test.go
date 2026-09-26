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

func TestComposeDelegateSuccessUnknownUsage(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "ok.sh")
	composeTestWriteScript(t, script, "#!/bin/sh\necho ok > \"$1\"\n")
	marker := filepath.Join(ws, "marker.txt")
	code, usage, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{script, marker},
		Workspace:  ws,
		OutputDir:  out,
		MaxSeconds: 10,
	})
	if err != nil || code != 0 {
		t.Fatalf("delegate: code=%d err=%v", code, err)
	}
	if usage.KnownCostUSD != nil || usage.KnownTokens != nil || usage.UsageComplete {
		t.Fatalf("usage must stay unknown/nil/false: %+v", usage)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
}

func TestComposeDelegateTimeout(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "hang.sh")
	composeTestWriteScript(t, script, "#!/bin/sh\nsleep 30\n")
	code, usage, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{script},
		Workspace:  ws,
		OutputDir:  out,
		MaxSeconds: 1,
	})
	if code != 124 {
		t.Fatalf("want exit 124 on timeout, got %d err=%v", code, err)
	}
	if usage.UsageComplete || usage.KnownCostUSD != nil {
		t.Fatalf("timeout usage must remain unknown: %+v", usage)
	}
}

func TestComposeDelegateOptInCredentialEnv(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "env.sh")
	composeTestWriteScript(t, script, "#!/bin/sh\nprintf '%s' \"$OPENROUTER_API_KEY\" > \"$1\"\n")
	marker := filepath.Join(ws, "key.txt")
	const sentinel = "sentinel-api-key-value-not-for-records"

	// Absent by default: sentinel must not leak from the process environment.
	t.Setenv("OPENROUTER_API_KEY", sentinel)
	code, _, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{script, marker},
		Workspace:  ws,
		OutputDir:  out,
		MaxSeconds: 5,
	})
	if err != nil || code != 0 {
		t.Fatalf("default env: code=%d err=%v", code, err)
	}
	body, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "" {
		t.Fatalf("credential must be absent without env_names opt-in, got %q", body)
	}

	// Present only on explicit opt-in.
	code, _, err = runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{script, marker},
		Workspace:  ws,
		OutputDir:  filepath.Join(root, "out2"),
		MaxSeconds: 5,
		EnvNames:   []string{"OPENROUTER_API_KEY"},
	})
	if err != nil || code != 0 {
		t.Fatalf("opt-in env: code=%d err=%v", code, err)
	}
	body, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != sentinel {
		t.Fatalf("opt-in credential not forwarded, got %q", body)
	}

	// Records store names only — never credential values.
	impl := ImplementationRequest{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     "att-env",
		ParentID:      "parent",
		EnvNames:      []string{"OPENROUTER_API_KEY"},
	}
	raw, err := json.Marshal(impl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatal("implementation JSON must not serialize credential values")
	}
	if !strings.Contains(string(raw), "OPENROUTER_API_KEY") {
		t.Fatal("env_names must record the opt-in name")
	}
}

func TestComposeDelegateKillsProcessGroupChildren(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "children.sh")
	// Spawn a sleeper in the same process group; primary exits immediately.
	// composeRunProcessGroup must SIGKILL the group after primary exit.
	composeTestWriteScript(t, script, "#!/bin/sh\n(sleep 60)&\nexit 0\n")
	start := time.Now()
	code, _, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{script},
		Workspace:  ws,
		OutputDir:  out,
		MaxSeconds: 5,
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("delegate appeared to wait on lingering children")
	}
}
