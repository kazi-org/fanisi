package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestComposeImportBackend(t *testing.T) {
	dir := t.TempDir()
	req := composeTestFiniteChoiceRequest()
	req.EvidenceIDs = []string{"e1"}
	req.Evidence = map[string]string{"e1": "two products"}

	t.Run("ok sets provenance", func(t *testing.T) {
		path := filepath.Join(dir, "ok.json")
		conf := 0.9
		writeComposeTestJSON(t, path, FiniteChoiceResult{
			SchemaVersion: compositionSchemaVersion,
			SelectedID:    "reuse",
			Abstain:       false,
			ReasonIDs:     []string{"e1"},
			Backend:       BackendImport,
			Model:         "spoof-model",
			Provider:      "spoof-provider",
			Confidence:    &conf,
		})
		got, err := NewImportBackend(path).Choose(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Backend != BackendImport {
			t.Fatalf("backend=%q", got.Backend)
		}
		if got.Model != "" || got.Provider != "" {
			t.Fatalf("trusted metadata leaked: model=%q provider=%q", got.Model, got.Provider)
		}
		if got.SelectedID != "reuse" || got.Confidence == nil || *got.Confidence != conf {
			t.Fatalf("unexpected result: %+v", got)
		}
	})

	t.Run("rejects jev spoof", func(t *testing.T) {
		path := filepath.Join(dir, "jev.json")
		writeComposeTestJSON(t, path, FiniteChoiceResult{
			SchemaVersion: compositionSchemaVersion,
			SelectedID:    "reuse",
			Backend:       BackendJevShadow,
		})
		_, err := NewImportBackend(path).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "spoofing") {
			t.Fatalf("got %v, want spoofing", err)
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		path := filepath.Join(dir, "extra.json")
		if err := os.WriteFile(path, []byte(`{"schema_version":1,"selected_id":"reuse","abstain":false,"extra":true}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewImportBackend(path).Choose(context.Background(), req)
		if err == nil {
			t.Fatal("expected unknown field rejection")
		}
	})

	t.Run("rejects trailing values", func(t *testing.T) {
		path := filepath.Join(dir, "trail.json")
		if err := os.WriteFile(path, []byte(`{"schema_version":1,"selected_id":"reuse","abstain":false}{}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewImportBackend(path).Choose(context.Background(), req)
		if err == nil {
			t.Fatal("expected trailing JSON rejection")
		}
	})

	t.Run("rejects oversized regular file", func(t *testing.T) {
		path := filepath.Join(dir, "big.json")
		// Larger than the import cap; content need not be valid JSON.
		payload := append([]byte(`{"pad":"`), bytes.Repeat([]byte("x"), composeImportResultMax)...)
		payload = append(payload, []byte(`"}`)...)
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewImportBackend(path).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("got %v, want exceeds", err)
		}
	})

	t.Run("rejects fifo without blocking", func(t *testing.T) {
		path := filepath.Join(dir, "result.fifo")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := NewImportBackend(path).Choose(context.Background(), req)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("got %v, want regular file rejection", err)
			}
		case <-time.After(time.Second):
			t.Fatal("import Choose blocked on FIFO")
		}
	})

	t.Run("rejects directory", func(t *testing.T) {
		path := filepath.Join(dir, "as-dir")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := NewImportBackend(path).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("got %v, want regular file rejection", err)
		}
	})
}

func TestComposeCommandBackendInspectsStdin(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "inspect.sh")
	capture := filepath.Join(dir, "stdin.json")
	writeExecutable(t, script, `#!/bin/sh
set -e
cat > "$1"
/usr/bin/python3 - "$1" <<'PY'
import json, sys
req = json.load(open(sys.argv[1]))
assert req["state"] == "need durable thread store", req["state"]
assert req["evidence"]["e1"] == "two products", req["evidence"]
print('{"schema_version":1,"selected_id":"reuse","abstain":false,"reason_ids":["e1"]}')
PY
`)
	req := composeTestFiniteChoiceRequest()
	req.EvidenceIDs = []string{"e1"}
	req.Evidence = map[string]string{"e1": "two products"}
	got, err := NewCommandBackend([]string{script, capture}).Choose(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != BackendCommand || got.SelectedID != "reuse" {
		t.Fatalf("unexpected result: %+v", got)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var saved FiniteChoiceRequest
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.State != req.State || saved.Evidence["e1"] != "two products" {
		t.Fatalf("stdin not complete request: %+v", saved)
	}
}

func TestComposeCommandBackendInvalidRequestBeforeMarker(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := filepath.Join(dir, "mark.sh")
	writeExecutable(t, script, `#!/bin/sh
touch "$1"
echo '{"schema_version":1,"selected_id":"reuse","abstain":false}'
`)
	req := composeTestFiniteChoiceRequest()
	req.Question = ""
	_, err := NewCommandBackend([]string{script, marker}).Choose(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "question") {
		t.Fatalf("got %v, want question validation", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("command executed despite invalid request")
	}
}

func TestComposeCommandBackendProviderErrorVsAbstain(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"abstain","abstain":true}'
exit 2
`)
	req := composeTestFiniteChoiceRequest()
	_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "provider error") {
		t.Fatalf("got %v, want provider error (not abstain success)", err)
	}
}

func TestComposeCommandBackendUnknownChoiceAndReason(t *testing.T) {
	dir := t.TempDir()
	req := composeTestFiniteChoiceRequest()
	req.EvidenceIDs = []string{"e1"}
	req.Evidence = map[string]string{"e1": "x"}

	t.Run("unknown choice", func(t *testing.T) {
		script := filepath.Join(dir, "bad-choice.sh")
		writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"not-a-candidate","abstain":false}'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "not a known candidate") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("unknown reason", func(t *testing.T) {
		script := filepath.Join(dir, "bad-reason.sh")
		writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"reuse","abstain":false,"reason_ids":["missing"]}'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestComposeCommandBackendMalformedAndOversized(t *testing.T) {
	dir := t.TempDir()
	req := composeTestFiniteChoiceRequest()

	t.Run("malformed", func(t *testing.T) {
		script := filepath.Join(dir, "bad-json.sh")
		writeExecutable(t, script, `#!/bin/sh
echo 'not-json'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil {
			t.Fatal("expected decode error")
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		script := filepath.Join(dir, "unknown-field.sh")
		writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"reuse","abstain":false,"surprise":1}'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil {
			t.Fatal("expected unknown field rejection")
		}
	})

	t.Run("trailing value", func(t *testing.T) {
		script := filepath.Join(dir, "trail.sh")
		writeExecutable(t, script, `#!/bin/sh
printf '%s\n' '{"schema_version":1,"selected_id":"reuse","abstain":false}' '{}'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "exactly one JSON value") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("oversized stdout", func(t *testing.T) {
		script := filepath.Join(dir, "big.sh")
		writeExecutable(t, script, `#!/bin/sh
/usr/bin/python3 - <<'PY'
print("x" * (64 * 1024 + 8))
PY
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "stdout exceeds") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("out of range confidence", func(t *testing.T) {
		script := filepath.Join(dir, "hi-conf.sh")
		writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"reuse","abstain":false,"confidence":1.5}'
`)
		_, err := NewCommandBackend([]string{script}).Choose(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "[0,1]") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestComposeCommandBackendTimeoutAndChildCleanup(t *testing.T) {
	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "hang.sh")
	writeExecutable(t, script, `#!/bin/sh
set -e
/bin/sleep 120 &
echo $! > "$1"
wait
`)

	backend := NewCommandBackend([]string{script, childPIDPath}).(*composeCommandBackend)
	backend.timeout = 200 * time.Millisecond

	_, err := backend.Choose(context.Background(), composeTestFiniteChoiceRequest())
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("got %v, want timeout", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		b, readErr := os.ReadFile(childPIDPath)
		if readErr == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(b)))
			if convErr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		// Group kill can race the pid write; absence still means no surviving child file.
		return
	}
	for time.Now().Before(deadline) {
		if killErr := syscall.Kill(childPID, 0); killErr != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child pid %d still alive after timeout cleanup", childPID)
}

func TestComposeCommandBackendRejectsSpoofedBackend(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "spoof.sh")
	writeExecutable(t, script, `#!/bin/sh
echo '{"schema_version":1,"selected_id":"reuse","abstain":false,"backend":"jev_shadow"}'
`)
	_, err := NewCommandBackend([]string{script}).Choose(context.Background(), composeTestFiniteChoiceRequest())
	if err == nil || !strings.Contains(err.Error(), "spoofing") {
		t.Fatalf("got %v, want spoofing", err)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}
