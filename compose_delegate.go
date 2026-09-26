package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runDelegate executes a trusted local operator argv under a new process group.
//
// Limits (documented): children that call setsid(2)/setpgid to detach leave the
// process group and are not reachable via Kill(-pgid). Ignored files and
// out-of-repository side effects need external sandboxing; this is trusted local
// operator orchestration, not an OS sandbox.
//
// Environment is PATH/TMPDIR plus only explicit EnvNames. Usage is always unknown
// (nil / UsageComplete=false); exit status alone never invents provider cost.
// MaxSeconds <= 0 means rely solely on ctx deadline (used when dispatch already
// bound the attempt context).
func runDelegate(ctx context.Context, cfg DelegateConfig) (exitCode int, usage UsageKnown, err error) {
	usage = UsageKnown{KnownCostUSD: nil, KnownTokens: nil, UsageComplete: false}
	if len(cfg.Argv) == 0 {
		return -1, usage, errors.New("delegate argv is empty")
	}
	if strings.TrimSpace(cfg.Workspace) == "" {
		return -1, usage, errors.New("delegate workspace is required")
	}
	ws, err := composeCanonicalRoot(cfg.Workspace)
	if err != nil {
		return -1, usage, fmt.Errorf("delegate workspace: %w", err)
	}
	cfg.Workspace = ws
	if err := composeRejectSymlinkPath(cfg.Workspace); err != nil {
		return -1, usage, fmt.Errorf("delegate workspace: %w", err)
	}
	if cfg.OutputDir != "" {
		outDir, err := composeCanonicalRoot(cfg.OutputDir)
		if err != nil {
			return -1, usage, fmt.Errorf("delegate output_dir: %w", err)
		}
		cfg.OutputDir = outDir
		if err := composeRejectSymlinkPath(cfg.OutputDir); err != nil {
			return -1, usage, fmt.Errorf("delegate output_dir: %w", err)
		}
		if err := os.MkdirAll(cfg.OutputDir, 0700); err != nil {
			return -1, usage, err
		}
	}
	argv, err := composeResolveAbsoluteArgv(cfg.Argv, cfg.Workspace)
	if err != nil {
		return -1, usage, err
	}
	env, err := composeDelegateEnv(cfg.EnvNames)
	if err != nil {
		return -1, usage, err
	}
	logPath := filepath.Join(cfg.OutputDir, "delegate.log")
	if cfg.OutputDir == "" {
		tmp, err := os.MkdirTemp("", "fanisi-delegate-*")
		if err != nil {
			return -1, usage, err
		}
		defer os.RemoveAll(tmp)
		logPath = filepath.Join(tmp, "delegate.log")
	}
	var maxDur time.Duration
	if cfg.MaxSeconds > 0 {
		maxDur = time.Duration(cfg.MaxSeconds) * time.Second
	}
	code, runErr := composeRunProcessGroup(ctx, cfg.Workspace, argv, env, logPath, maxDur)
	return code, usage, runErr
}

// runIndependentVerifier runs verify_command after the caller has already
// confirmed protected/forbidden inputs are unpoisoned. It snapshots write and
// protected paths, executes the verifier under process-group cancellation, then
// re-reads hashes. The caller sets State and compares against pre-dispatch snapshots.
//
// Verifier environment is PATH/TMPDIR plus controlled HOME/GOCACHE/GOTMPDIR/GOMODCACHE
// under a private cache root (not the operator home). This public entrypoint does
// not inherit ImplementationRequest.EnvNames; dispatch applies those via
// composeRunIndependentVerifier when explicitly configured.
func runIndependentVerifier(ctx context.Context, workspace string, verify []string, writePaths, protected []string) (AttemptResult, error) {
	tmp, err := os.MkdirTemp("", "fanisi-compose-verify-*")
	if err != nil {
		return AttemptResult{}, err
	}
	defer os.RemoveAll(tmp)
	return composeRunIndependentVerifier(ctx, workspace, verify, writePaths, protected, nil, tmp)
}

// composeRunIndependentVerifier is the dispatch path: optional envNames are the
// same operator-authored ImplementationRequest.EnvNames used for the delegate
// (PATH/TMPDIR plus only those names; values are never logged or written to
// journal records). cacheRoot receives controlled HOME/GOCACHE/GOTMPDIR/GOMODCACHE.
func composeRunIndependentVerifier(ctx context.Context, workspace string, verify, writePaths, protected, envNames []string, cacheRoot string) (AttemptResult, error) {
	var partial AttemptResult
	partial.SchemaVersion = compositionSchemaVersion
	partial.Usage = UsageKnown{KnownCostUSD: nil, KnownTokens: nil, UsageComplete: false}
	if strings.TrimSpace(workspace) == "" {
		return partial, errors.New("verifier workspace is required")
	}
	if len(verify) == 0 {
		return partial, errors.New("verify_command is required")
	}
	if strings.TrimSpace(cacheRoot) == "" {
		return partial, errors.New("verifier cache root is required")
	}
	argv, err := composeResolveAbsoluteArgv(verify, workspace)
	if err != nil {
		return partial, err
	}
	allowed := composeUniquePaths(append(append([]string{}, writePaths...), protected...))
	writeHashes, err := composeSnapshotRelFiles(workspace, writePaths, allowed)
	if err != nil {
		return partial, err
	}
	protHashes, err := composeSnapshotRelFiles(workspace, protected, allowed)
	if err != nil {
		return partial, err
	}
	partial.FinalWriteHashes = writeHashes
	partial.ProtectedHashes = protHashes

	logPath := filepath.Join(cacheRoot, "verifier.log")
	env, err := composeVerifierEnv(envNames, cacheRoot)
	if err != nil {
		return partial, err
	}
	// Duration bound comes from attempt ctx; do not hardcode a nested timeout.
	code, runErr := composeRunProcessGroup(ctx, workspace, argv, env, logPath, 0)
	partial.VerifierExit = code
	if b, rerr := os.ReadFile(logPath); rerr == nil {
		partial.VerifierLogSHA = digest(b)
	}

	afterWrite, err := composeSnapshotRelFiles(workspace, writePaths, allowed)
	if err != nil {
		return partial, errors.Join(runErr, err)
	}
	afterProt, err := composeSnapshotRelFiles(workspace, protected, allowed)
	if err != nil {
		return partial, errors.Join(runErr, err)
	}
	partial.FinalWriteHashes = afterWrite
	if changed(protHashes, afterProt) {
		partial.ProtectedHashes = afterProt
		return partial, errors.New("protected inputs changed during verifier execution")
	}
	partial.ProtectedHashes = afterProt
	if runErr != nil && code != 124 {
		return partial, runErr
	}
	return partial, runErr
}

// composeResolveAbsoluteArgv resolves only argv[0] to an absolute executable
// path. Remaining argv elements are preserved byte-for-byte (literals, URLs,
// flags like --output=dir/file, and shell/Python snippets must not be rewritten).
// Relative argv[0] is resolved against workspace when that path exists (any
// file type); otherwise LookPath is used. The child process cwd is workspace.
//
// Workspace-scoped executables (relative hits or absolute paths inside the
// workspace) reject every symlink component—including parents—so protected
// scripts cannot be swapped via links. Operator-selected external executables
// (absolute outside workspace or PATH tools) are trusted and EvalSymlinks'd to
// the real regular file before spawn.
func composeResolveAbsoluteArgv(argv []string, workspace string) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty argv")
	}
	for _, a := range argv {
		if strings.ContainsAny(a, "\x00") {
			return nil, errors.New("argv must not contain NUL")
		}
	}
	ws := ""
	if strings.TrimSpace(workspace) != "" {
		canon, err := composeCanonicalRoot(workspace)
		if err != nil {
			return nil, fmt.Errorf("resolve executable workspace: %w", err)
		}
		ws = canon
	}
	first := argv[0]
	if !filepath.IsAbs(first) {
		resolved := ""
		if ws != "" {
			candidate := filepath.Join(ws, first)
			if _, err := os.Lstat(candidate); err == nil {
				abs, err := filepath.Abs(candidate)
				if err != nil {
					return nil, err
				}
				resolved = abs
			}
		}
		if resolved == "" {
			looked, err := exec.LookPath(first)
			if err != nil {
				return nil, fmt.Errorf("resolve executable %q: %w", first, err)
			}
			abs, err := filepath.Abs(looked)
			if err != nil {
				return nil, err
			}
			resolved = abs
		}
		first = resolved
	} else {
		abs, err := filepath.Abs(first)
		if err != nil {
			return nil, err
		}
		first = abs
	}

	mapped, inside := "", false
	if ws != "" {
		mapped, inside = composeMapAbsInsideWorkspace(first, ws)
	}
	if inside {
		first = mapped
		if err := composeRejectSymlinkPath(first); err != nil {
			return nil, fmt.Errorf("delegate/verifier executable: %w", err)
		}
		info, err := os.Lstat(first)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("delegate/verifier executable must be a regular file")
		}
	} else {
		real, err := filepath.EvalSymlinks(first)
		if err != nil {
			return nil, fmt.Errorf("resolve executable %q: %w", first, err)
		}
		info, err := os.Lstat(real)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("delegate/verifier executable must be a regular file")
		}
		first = real
	}
	out := append([]string{first}, argv[1:]...)
	return out, nil
}

// composeMapAbsInsideWorkspace maps absPath into the canonical workspace when
// it is workspace-scoped, without resolving symlink components below the
// workspace root. Lexically-inside paths are retained. Platform-alias paths
// (macOS /var → /private/var) walk ancestors until EvalSymlinks(ancestor)
// equals the canonical workspace root, then join that root with the lexical
// relative suffix so composeRejectSymlinkPath sees canonical-prefix +
// untouched suffix (leaf/parent link escapes still reject).
func composeMapAbsInsideWorkspace(absPath, workspace string) (mapped string, inside bool) {
	absPath = filepath.Clean(absPath)
	workspace = filepath.Clean(workspace)
	sep := string(filepath.Separator)
	if absPath == workspace || strings.HasPrefix(absPath, workspace+sep) {
		return absPath, true
	}
	cur := absPath
	for {
		info, err := os.Lstat(cur)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			next := filepath.Dir(cur)
			if next == cur {
				return "", false
			}
			cur = next
			continue
		}
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil && filepath.Clean(resolved) == workspace {
			rel, err := filepath.Rel(cur, absPath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+sep) {
				return "", false
			}
			return filepath.Join(workspace, rel), true
		}
		next := filepath.Dir(cur)
		if next == cur {
			return "", false
		}
		cur = next
	}
}

// composeDelegateEnv builds PATH/TMPDIR plus only explicitly named operator
// EnvNames. Credential-bearing names are allowed when listed (opt-in); they are
// never inherited implicitly and must never be logged or written into journal
// records (env_names stores names only). Native runCommand credential stripping
// is unchanged and separate.
func composeDelegateEnv(extraNames []string) ([]string, error) {
	var env []string
	if v, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+v)
	} else {
		env = append(env, "PATH=/usr/bin:/bin")
	}
	if v, ok := os.LookupEnv("TMPDIR"); ok {
		env = append(env, "TMPDIR="+v)
	}
	seen := map[string]bool{"PATH": true, "TMPDIR": true}
	for _, name := range extraNames {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "=") {
			return nil, errors.New("env_names entries must be nonempty names without '='")
		}
		if seen[name] {
			continue
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("requested env %q is not set", name)
		}
		env = append(env, name+"="+v)
		seen[name] = true
	}
	return env, nil
}

// composeVerifierEnv builds PATH/TMPDIR plus controlled HOME/GOCACHE/GOTMPDIR/
// GOMODCACHE under cacheRoot so verifiers such as `go test` do not require the
// operator home or a copied environment. Optional envNames follow the same
// explicit opt-in rules as delegates (ImplementationRequest.EnvNames).
func composeVerifierEnv(extraNames []string, cacheRoot string) ([]string, error) {
	home := filepath.Join(cacheRoot, "home")
	gocache := filepath.Join(cacheRoot, "gocache")
	gotmp := filepath.Join(cacheRoot, "gotmp")
	gomod := filepath.Join(cacheRoot, "gomodcache")
	for _, d := range []string{home, gocache, gotmp, gomod} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return nil, err
		}
	}
	env, err := composeDelegateEnv(extraNames)
	if err != nil {
		return nil, err
	}
	env = append(env,
		"HOME="+home,
		"GOCACHE="+gocache,
		"GOTMPDIR="+gotmp,
		"GOMODCACHE="+gomod,
	)
	return env, nil
}

// composeRunProcessGroup runs argv with Setpgid, bounded logging, and kills the
// owned process group after the primary exit (and on cancel). maxDuration 0 means
// rely only on ctx. Unlike runCommand, there is no hardcoded 180s nested cap and
// no full environment inheritance.
//
// Children that inherit stdio can keep Wait blocked after the primary exits.
// Once the process-group leader is gone, remaining owned children are SIGKILL'd
// so pipes close; WaitDelay expiry with a recorded primary exit code is then
// classified from ProcessState rather than treated as a hard failure.
func composeRunProcessGroup(ctx context.Context, dir string, argv, env []string, logPath string, maxDuration time.Duration) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("empty command")
	}
	if maxDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, maxDuration)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return -1, err
	}
	limited := &composeLimitedWriter{w: logFile, n: composeMaxLogBytes}
	cmd.Stdout = limited
	cmd.Stderr = limited

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return -1, err
	}
	pgid := cmd.Process.Pid
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var runErr error
waitLoop:
	for {
		select {
		case runErr = <-waitErr:
			break waitLoop
		case <-ctx.Done():
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			runErr = <-waitErr
			break waitLoop
		case <-ticker.C:
			// Primary gone while children may still hold stdio: kill the owned group.
			if err := syscall.Kill(pgid, 0); err == syscall.ESRCH {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
				runErr = <-waitErr
				break waitLoop
			}
		}
	}
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	closeErr := logFile.Close()
	if closeErr != nil {
		runErr = errors.Join(runErr, closeErr)
	}
	if ctx.Err() != nil {
		return 124, ctx.Err()
	}
	if errors.Is(runErr, exec.ErrWaitDelay) {
		if cmd.ProcessState != nil {
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				return code, nil
			}
		}
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		return exit.ExitCode(), nil
	}
	if runErr != nil {
		return -1, runErr
	}
	return 0, nil
}

type composeLimitedWriter struct {
	w io.Writer
	n int
	c int
}

func (l *composeLimitedWriter) Write(p []byte) (int, error) {
	if l.c >= l.n {
		return len(p), nil
	}
	remain := l.n - l.c
	if len(p) > remain {
		_, err := l.w.Write(p[:remain])
		l.c = l.n
		return len(p), err
	}
	n, err := l.w.Write(p)
	l.c += n
	return n, err
}

func composeSnapshotRelFiles(workspace string, rels, allowed []string) (map[string]string, error) {
	result := map[string]string{}
	for _, name := range rels {
		path, err := scopedPath(workspace, name, allowed)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			result[name] = "missing"
			continue
		}
		if err != nil {
			return nil, err
		}
		result[name] = digest(b)
	}
	return result, nil
}

func composeUniquePaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func composeReadOnlyPaths(readPaths, writePaths []string) []string {
	writes := map[string]bool{}
	for _, w := range writePaths {
		writes[w] = true
	}
	var out []string
	for _, r := range readPaths {
		if !writes[r] {
			out = append(out, r)
		}
	}
	return out
}
