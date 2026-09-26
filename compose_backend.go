package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	composeImportResultMax       = 64 * 1024
	composeCommandStdoutMax      = 64 * 1024
	composeCommandStderrMax      = 8 * 1024
	composeCommandDefaultTimeout = 30 * time.Second
	composeCommandMinimalPATH    = "/usr/bin:/bin:/usr/sbin:/sbin"
)

type composeImportBackend struct {
	path string
}

type composeCommandBackend struct {
	argv    []string
	timeout time.Duration
}

// NewImportBackend returns a DecisionBackend that reads one FiniteChoiceResult
// from path via strict JSON. Provenance Backend is set to import; supplied
// model/provider metadata is cleared.
func NewImportBackend(path string) DecisionBackend {
	return &composeImportBackend{path: path}
}

// NewCommandBackend returns a DecisionBackend that runs argv (not a shell) with
// the request JSON on stdin and parses one FiniteChoiceResult from stdout.
func NewCommandBackend(argv []string) DecisionBackend {
	cp := append([]string(nil), argv...)
	return &composeCommandBackend{argv: cp, timeout: composeCommandDefaultTimeout}
}

func (b *composeImportBackend) Choose(ctx context.Context, req FiniteChoiceRequest) (FiniteChoiceResult, error) {
	if b.path == "" {
		return FiniteChoiceResult{}, errors.New("import backend path is empty")
	}
	if err := validateFiniteChoiceRequest(req); err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FiniteChoiceResult{}, err
	}
	data, err := composeReadBoundedRegularFile(b.path, composeImportResultMax)
	if err != nil {
		return FiniteChoiceResult{}, err
	}
	raw, err := composeDecodeFiniteChoiceResult(data, "import result")
	if err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := composeRejectBackendSpoof(raw.Backend, BackendImport); err != nil {
		return FiniteChoiceResult{}, err
	}
	result := composeAuthorizeFiniteChoiceResult(raw, BackendImport)
	if err := validateFiniteChoiceResult(req, result); err != nil {
		return FiniteChoiceResult{}, err
	}
	return result, nil
}

// composeReadBoundedRegularFile Lstats then opens only regular files, rejecting
// FIFOs and other special files before any blocking open/read. At most max
// bytes are read; oversized content is rejected without truncation.
func composeReadBoundedRegularFile(path string, max int) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("byte limit must be positive")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s: symlinks are not allowed", path)
	}
	if !mode.IsRegular() {
		return nil, fmt.Errorf("%s: must be a regular file", path)
	}
	if info.Size() > int64(max) {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, max)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: must be a regular file", path)
	}
	if st.Size() > int64(max) {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, max)
	}
	// Read at most max+1 to detect growth after Stat without loading unbounded data.
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, max)
	}
	return data, nil
}

func (b *composeCommandBackend) Choose(ctx context.Context, req FiniteChoiceRequest) (FiniteChoiceResult, error) {
	if len(b.argv) == 0 {
		return FiniteChoiceResult{}, errors.New("command backend argv is empty")
	}
	if err := validateFiniteChoiceRequest(req); err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FiniteChoiceResult{}, err
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return FiniteChoiceResult{}, fmt.Errorf("encode finite choice request: %w", err)
	}
	if len(reqJSON) > composeFiniteChoiceMaxBytes {
		return FiniteChoiceResult{}, fmt.Errorf("finite choice request exceeds %d bytes", composeFiniteChoiceMaxBytes)
	}

	timeout := b.timeout
	if timeout <= 0 {
		timeout = composeCommandDefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, b.argv[0], b.argv[1:]...)
	cmd.Stdin = bytes.NewReader(reqJSON)
	cmd.Env = composeCommandMinimalEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second

	stdout := &composeCapBuffer{max: composeCommandStdoutMax}
	stderr := &composeCapBuffer{max: composeCommandStderrMax}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	composeKillProcessGroup(cmd)
	if stdout.overflow {
		return FiniteChoiceResult{}, fmt.Errorf("decision command stdout exceeds %d bytes", composeCommandStdoutMax)
	}
	if stderr.overflow {
		return FiniteChoiceResult{}, fmt.Errorf("decision command stderr exceeds %d bytes", composeCommandStderrMax)
	}
	if runCtx.Err() != nil {
		return FiniteChoiceResult{}, fmt.Errorf("decision command timeout or canceled: %w", runCtx.Err())
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return FiniteChoiceResult{}, fmt.Errorf("decision command provider error: exit %d", exitErr.ExitCode())
		}
		return FiniteChoiceResult{}, fmt.Errorf("decision command failed: %w", runErr)
	}

	raw, err := composeDecodeFiniteChoiceResult(stdout.Bytes(), "decision command stdout")
	if err != nil {
		return FiniteChoiceResult{}, err
	}
	if err := composeRejectBackendSpoof(raw.Backend, BackendCommand); err != nil {
		return FiniteChoiceResult{}, err
	}
	result := composeAuthorizeFiniteChoiceResult(raw, BackendCommand)
	if err := validateFiniteChoiceResult(req, result); err != nil {
		return FiniteChoiceResult{}, err
	}
	return result, nil
}

func composeAuthorizeFiniteChoiceResult(raw FiniteChoiceResult, actual DecisionBackendKind) FiniteChoiceResult {
	raw.Backend = actual
	raw.Model = ""
	raw.Provider = ""
	return raw
}

func composeDecodeFiniteChoiceResult(data []byte, source string) (FiniteChoiceResult, error) {
	var result FiniteChoiceResult
	if err := composeDecodeStrictJSON(data, &result, source); err != nil {
		return FiniteChoiceResult{}, err
	}
	return result, nil
}

func composeCommandMinimalEnv() []string {
	tmpdir := os.TempDir()
	if tmpdir == "" {
		tmpdir = "/tmp"
	}
	return []string{
		"PATH=" + composeCommandMinimalPATH,
		"TMPDIR=" + tmpdir,
	}
}

func composeKillProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// composeCapBuffer captures at most max bytes and flags overflow.
type composeCapBuffer struct {
	max      int
	buf      bytes.Buffer
	overflow bool
}

func (b *composeCapBuffer) Write(p []byte) (int, error) {
	if b.overflow {
		return len(p), nil
	}
	remain := b.max - b.buf.Len()
	if remain <= 0 {
		b.overflow = true
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = b.buf.Write(p[:remain])
		b.overflow = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *composeCapBuffer) Bytes() []byte {
	return b.buf.Bytes()
}
