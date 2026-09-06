package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskPathsAreRelativeToConfig(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	base := filepath.Dir(cfg.Workspace)
	cfg.Workspace = "repo"
	cfg.Output = "runs/one"
	cfg.PromptFile = "brief.md"
	cfg.KeyFile = "key.env"
	path := filepath.Join(base, "task.json")
	if err := writeJSON(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != filepath.Join(base, "repo") || got.PromptFile != filepath.Join(base, "brief.md") {
		t.Fatal("config resolved against working directory")
	}
}

func TestConfigRejectsUnknownFieldsAndEscapingScope(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.WritePaths = []string{"../escape"}
	if err := validateConfig(cfg); err == nil {
		t.Fatal("scope escape accepted")
	}
	path := filepath.Join(filepath.Dir(cfg.Workspace), "task.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"max_tokenz":100}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field not caught: %v", err)
	}
}

func TestPacketProtectsBriefAndAccountsOmittedContext(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	data := strings.Repeat("x", 10000)
	if err := os.WriteFile(filepath.Join(cfg.Workspace, "value.txt"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Context = []ContextSlice{{Path: "value.txt", Start: 1, Lines: 1}}
	cfg.PacketBytes = 1000
	packet, sections, err := buildPacket(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packet), "configured verifier must pass") || sections[0].Included || sections[0].SHA256 != digest([]byte(data)) {
		t.Fatal("mandatory context lost or omission unreported")
	}
	again, _, err := buildPacket(context.Background(), cfg)
	if err != nil || string(again) != string(packet) {
		t.Fatal("packet is nondeterministic")
	}
	cfg.PacketBytes = 10
	if _, _, err := buildPacket(context.Background(), cfg); err == nil {
		t.Fatal("oversized mandatory contract silently truncated")
	}
}

func TestDryRunNeedsNoKeyAndWritesNothing(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.KeyFile = "nonexistent.env"
	path := filepath.Join(filepath.Dir(cfg.Workspace), "task.json")
	if err := writeJSON(path, cfg); err != nil {
		t.Fatal(err)
	}
	if err := mainContext(context.Background(), []string{"run", "--config", path, "--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.Output); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote run artifacts")
	}
	if _, err := os.Stat(filepath.Join(cfg.Workspace, ".fanisi-lock")); !os.IsNotExist(err) {
		t.Fatal("dry-run acquired workspace")
	}
}
