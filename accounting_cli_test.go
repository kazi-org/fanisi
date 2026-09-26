package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAccountingCLIProcess(t *testing.T) {
	if os.Getenv("FANISI_ACCOUNTING_CLI_HELPER") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				if err := mainContext(context.Background(), os.Args[i+1:]); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				os.Exit(0)
			}
		}
		os.Exit(2)
	}
	root := t.TempDir()
	attempt := filepath.Join(root, "attempt")
	if err := os.Mkdir(attempt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(attempt, "attempt.json"), Attempt{SchemaVersion: 1, TaskID: "task", Arm: "fanisi", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "effort.json")
	if err := os.WriteFile(file, []byte(`{"schema_version":1,"id":"review","study":"study","task":"task","attempt":"attempt","role":"reviewer","source_fingerprint":"fixture","from":"2026-09-07T00:00:00Z","to":"2026-09-07T00:01:00Z","coverage":"complete","allocation":"exclusive","active_seconds":12}`), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(exe, append([]string{"-test.run=^TestAccountingCLIProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "FANISI_ACCOUNTING_CLI_HELPER=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI: %v: %s", err, out)
		}
		return out
	}
	run("import-effort", root, file)
	run("import-effort", root, file)
	var got struct {
		Effort   map[string]EffortTotal `json:"effort"`
		Delivery DeliveryReport         `json:"delivery"`
	}
	out := run("report", root)
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Effort["reviewer"].Records != 1 || got.Effort["reviewer"].ActiveSeconds != 12 || got.Delivery.Attempts != 1 || got.Delivery.TotalCost != nil {
		t.Fatalf("CLI report: %s", out)
	}
}
