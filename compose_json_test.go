package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestComposeStrictJSONRejectsDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dup.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"schema_version":2}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var dest map[string]any
	err := composeReadStrictJSONFile(path, &dest, composeExecutableJSONMax)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate key rejection, got %v", err)
	}
}

func TestComposeStrictJSONRejectsOversized(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.json")
	body := []byte(`{"x":"` + strings.Repeat("a", 200) + `"}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var dest map[string]any
	err := composeReadStrictJSONFile(path, &dest, 64)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want oversized rejection, got %v", err)
	}
}

func TestComposeStrictJSONRejectsFIFOWithoutHang(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fifo.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		var dest map[string]any
		done <- composeReadStrictJSONFile(path, &dest, composeExecutableJSONMax)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("want regular-file rejection, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO open hung; bounded regular-file guard failed")
	}
}

func TestComposeStrictJSONNestedDuplicateKeys(t *testing.T) {
	raw := []byte(`{"a":{"k":1,"k":2}}`)
	var dest map[string]any
	err := composeDecodeStrictJSON(raw, &dest, "nested")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want nested duplicate rejection, got %v", err)
	}
}
