package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestComposeCompatExternalSymlinkExecutableAllowed(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	// Operator-selected external symlink to a harmless system shell (outside workspace).
	link := filepath.Join(root, "ext-sh")
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(ws, "marker.txt")
	literal := "--output=dir/sub/file"
	code, usage, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{link, "-c", "printf ok > marker.txt", literal},
		Workspace:  ws,
		OutputDir:  out,
		MaxSeconds: 5,
	})
	if err != nil || code != 0 {
		t.Fatalf("external symlink delegate: code=%d err=%v", code, err)
	}
	if usage.UsageComplete || usage.KnownCostUSD != nil {
		t.Fatalf("usage must stay unknown: %+v", usage)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "ok" {
		t.Fatalf("marker=%q err=%v", body, err)
	}
	resolved, err := composeResolveAbsoluteArgv([]string{link, "-c", "printf ok", literal}, ws)
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0] == link {
		t.Fatalf("expected EvalSymlinks of external argv0, still %q", resolved[0])
	}
	if info, err := os.Lstat(resolved[0]); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("resolved argv0 must be real regular file: %v", err)
	}
	if resolved[1] != "-c" || resolved[2] != "printf ok" || resolved[3] != literal {
		t.Fatalf("argv[1+] rewritten: %#v", resolved)
	}
}

func TestComposeCompatWorkspaceSymlinkExecutableRejected(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	// Leaf symlink inside workspace (protected-script boundary).
	evil := filepath.Join(ws, "evil.sh")
	if err := os.Symlink("/bin/sh", evil); err != nil {
		t.Fatal(err)
	}
	_, err = composeResolveAbsoluteArgv([]string{"./evil.sh"}, wsCanon)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want workspace leaf symlink rejection, got %v", err)
	}
	_, err = composeResolveAbsoluteArgv([]string{evil}, wsCanon)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want absolute-in-workspace symlink rejection, got %v", err)
	}

	// Symlink parent (not only leaf) must reject.
	bin := filepath.Join(ws, "bin")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	composeTestWriteScript(t, filepath.Join(outside, "tool.sh"), "#!/bin/sh\nexit 0\n")
	if err := os.Symlink(outside, bin); err != nil {
		t.Fatal(err)
	}
	_, err = composeResolveAbsoluteArgv([]string{"./bin/tool.sh"}, wsCanon)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink-parent rejection, got %v", err)
	}
}

func TestComposeCompatRelativeVerifyStillWorks(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	composeTestWriteScript(t, filepath.Join(ws, "verify.sh"), "#!/bin/sh\nexit 0\n")
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := composeResolveAbsoluteArgv([]string{"./verify.sh", "--flag=a/b"}, wsCanon)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resolved[0], string(filepath.Separator)+"verify.sh") {
		t.Fatalf("expected workspace ./verify.sh, got %#v", resolved)
	}
	if resolved[1] != "--flag=a/b" {
		t.Fatalf("argv literal rewritten: %#v", resolved)
	}
	info, err := os.Lstat(resolved[0])
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("verify.sh must stay a regular workspace file: %v", err)
	}
}

func TestComposeCompatBareArgv0IgnoresWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	name := "fanisi-bare-argv0-tool"
	composeTestWriteScript(t, filepath.Join(ws, name), "#!/bin/sh\nexit 0\n")
	pathTool := filepath.Join(bin, name)
	composeTestWriteScript(t, pathTool, "#!/bin/sh\nprintf path-hit\nexit 3\n")
	t.Setenv("PATH", bin)
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := composeResolveAbsoluteArgv([]string{name, "--keep=a/b"}, wsCanon)
	if err != nil {
		t.Fatal(err)
	}
	realPath, err := filepath.EvalSymlinks(pathTool)
	if err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(resolved[0])
	if err != nil {
		t.Fatal(err)
	}
	if realResolved != realPath {
		t.Fatalf("bare argv0 must use PATH tool %q, got %q", realPath, realResolved)
	}
	if resolved[1] != "--keep=a/b" {
		t.Fatalf("argv[1+] rewritten: %#v", resolved)
	}
	// Old behavior preferred workspace: demonstrate that would differ.
	wsHit := filepath.Join(wsCanon, name)
	if realResolved == wsHit {
		t.Fatal("regression: bare argv0 preferred workspace over PATH")
	}
}

func TestComposeCompatPATHGoSymlinkAndControlledGOCACHE(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	realGo, err = filepath.EvalSymlinks(realGo)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	cacheRoot := filepath.Join(root, "cache")
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	linkGo := filepath.Join(bin, "go")
	if err := os.Symlink(realGo, linkGo); err != nil {
		t.Fatal(err)
	}
	// go test prepends canonical GOROOT/bin; force our external symlink first.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"go", "env", "GOCACHE"}
	resolved, err := composeResolveAbsoluteArgv(argv, wsCanon)
	if err != nil {
		t.Fatalf("PATH go symlink must resolve: %v", err)
	}
	if resolved[1] != "env" || resolved[2] != "GOCACHE" {
		t.Fatalf("argv[1+] rewritten: %#v", resolved)
	}
	if resolved[0] == linkGo {
		t.Fatalf("expected EvalSymlinks of PATH go symlink, still %q", resolved[0])
	}
	if info, err := os.Lstat(resolved[0]); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("resolved go must be real regular file: mode err=%v", err)
	}
	partial, err := composeRunIndependentVerifier(context.Background(), wsCanon, argv, nil, nil, nil, cacheRoot)
	if err != nil {
		t.Fatalf("go env GOCACHE verifier: %v", err)
	}
	if partial.VerifierExit != 0 {
		t.Fatalf("want go env exit 0, got %d", partial.VerifierExit)
	}
	logPath := filepath.Join(cacheRoot, "verifier.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantCache := filepath.Join(cacheRoot, "gocache")
	if !strings.Contains(string(body), wantCache) {
		t.Fatalf("controlled GOCACHE %q not observed in verifier log %q", wantCache, body)
	}
}

func TestComposeCompatAbsoluteTempScriptPlatformAlias(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(ws, "ok.sh")
	composeTestWriteScript(t, script, "#!/bin/sh\nexit 0\n")
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir is often /var/... while canonical workspace is /private/var/...
	absScript, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	if absScript == filepath.Join(wsCanon, "ok.sh") {
		t.Skip("host temp path is already canonical; no platform-alias mapping to exercise")
	}
	resolved, err := composeResolveAbsoluteArgv([]string{absScript}, wsCanon)
	if err != nil {
		t.Fatalf("platform-alias absolute workspace script must resolve: %v", err)
	}
	want := filepath.Join(wsCanon, "ok.sh")
	if resolved[0] != want {
		t.Fatalf("mapped argv0=%q want %q", resolved[0], want)
	}
	info, err := os.Lstat(resolved[0])
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("mapped script must be regular file under canonical workspace: %v", err)
	}
}

func TestComposeCompatCLIReviewNotesOversizedAndFIFO(t *testing.T) {
	root := t.TempDir()
	journal := filepath.Join(root, "journal")
	big := filepath.Join(root, "big-notes.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("n", 16001)), 0600); err != nil {
		t.Fatal(err)
	}
	err := composeCLIReview([]string{
		"--journal", journal,
		"--attempt", "att-notes",
		"--decision", "reject",
		"--reviewer", "david",
		"--kind", "human",
		"--notes-file", big,
		"--result-hash", strings.Repeat("a", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want oversized notes rejection, got %v", err)
	}

	fifo := filepath.Join(root, "notes.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- composeCLIReview([]string{
			"--journal", journal,
			"--attempt", "att-fifo",
			"--decision", "reject",
			"--reviewer", "david",
			"--kind", "human",
			"--notes-file", fifo,
			"--result-hash", strings.Repeat("b", 64),
		})
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("want FIFO regular-file refusal, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("compose review notes FIFO open hung; bounded regular-file guard failed")
	}
}
