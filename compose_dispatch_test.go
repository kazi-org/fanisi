package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func composeTestBundleAndImpl(t *testing.T, root string, mutate func(*CompositionRequest, *ImplementationRequest, map[string]string)) (CompositionBundle, ImplementationRequest) {
	t.Helper()
	ws := filepath.Join(root, "product")
	out := filepath.Join(root, "output")
	journal := filepath.Join(root, "journal")
	_ = journal
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"candidate.txt":       "old",
		"readonly.txt":        "frozen-source",
		"protected/accept.md": "acceptance-input",
		"verify.sh":           "#!/bin/sh\ntest \"$(cat candidate.txt)\" = new\n",
	}
	okDelegate := filepath.Join(root, "ok.sh")
	composeTestWriteScript(t, okDelegate, "#!/bin/sh\nprintf 'new' > candidate.txt\n")
	head := composeTestInitGit(t, ws, files)

	pin := strings.Repeat("a", 64)
	req := CompositionRequest{
		SchemaVersion:   compositionSchemaVersion,
		RequestID:       "req1",
		ParentID:        "parent1",
		ProductRevision: head,
		CatalogPin:      pin,
		Requirements:    []string{"Edit candidate.txt to new under verification"},
		Behaviors:       []BehaviorNeed{{BehaviorID: "edit-candidate", Required: true, Contract: "contracts/edit.md"}},
		Workspace:       ws,
		ReadPaths:       []string{"readonly.txt", "candidate.txt"},
		WritePaths:      []string{"candidate.txt"},
		ProtectedPaths:  []string{"protected/accept.md", "verify.sh"},
		AuthorityRef:    "operator:local-test",
		DeadlineRFC3339: composeTestFutureDeadline(),
		Budget: CompositionBudget{
			MaxAttempts: 3, MaxSeconds: 30, MaxEstimatedUSD: 1.0, ReservedUSD: 0.1,
			CurrencyNote: "admission estimate; provider receipts separate",
		},
	}
	if mutate != nil {
		mutate(&req, nil, files)
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	dec := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec1",
		RequestHash:   reqHash,
		BehaviorID:    "edit-candidate",
		Verdict:       VerdictProductLocal,
		Owner:         "operator",
		Provenance:    DecisionProvenance{Source: "operator", Author: "tester"},
	}
	decHash, err := hashComponentDecision(dec)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man1",
		RequestHash:    reqHash,
		DecisionHashes: []string{decHash},
		Bindings: []Binding{{
			BehaviorID:   "edit-candidate",
			DecisionHash: decHash,
			Kind:         "local_planned",
			TargetRef:    "candidate.txt",
		}},
	}
	manHash, err := hashApplicationManifest(man)
	if err != nil {
		t.Fatal(err)
	}
	b := CompositionBundle{
		Request:   req,
		Catalog:   CatalogIndex{SchemaVersion: 1, Pin: pin, SourceLabel: "test", Records: nil},
		Evidence:  map[string]EvidenceRef{},
		Decisions: []ComponentDecision{dec},
		Manifest:  man,
	}
	impl := ImplementationRequest{
		SchemaVersion:   compositionSchemaVersion,
		AttemptID:       "att1",
		ParentID:        req.ParentID,
		ManifestHash:    manHash,
		RequestHash:     reqHash,
		FenceToken:      "fence-1",
		Owner:           "operator",
		ReadPaths:       append([]string{}, req.ReadPaths...),
		WritePaths:      append([]string{}, req.WritePaths...),
		ProtectedPaths:  append([]string{}, req.ProtectedPaths...),
		VerifyCommand:   []string{filepath.Join(ws, "verify.sh")},
		DelegateArgv:    []string{okDelegate},
		Workspace:       ws,
		OutputDir:       out,
		ReservedUSD:     0.1,
		MaxSeconds:      20,
		DeadlineRFC3339: composeTestFutureDeadline(),
		IdempotencyKey:  req.ParentID + "|" + reqHash + "|intent-edit",
	}
	if mutate != nil {
		mutate(nil, &impl, files)
	}
	return b, impl
}

func TestComposeDispatchVerifiedPendingReview(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result.State != AttemptVerifiedPendingReview {
		t.Fatalf("state=%s err=%q", result.State, result.Error)
	}
	if result.Usage.KnownCostUSD != nil || result.Usage.UsageComplete {
		t.Fatalf("usage must be unknown: %+v", result.Usage)
	}
	body, err := os.ReadFile(filepath.Join(impl.Workspace, "candidate.txt"))
	if err != nil || string(body) != "new" {
		t.Fatalf("candidate not updated: %q err=%v", body, err)
	}
}

func TestComposeDispatchDuplicateIdempotency(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	first, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if second.ContentSHA256 != first.ContentSHA256 || second.AttemptID != first.AttemptID {
		t.Fatal("same payload must observationally replay without new execution")
	}
	impl2 := impl
	impl2.AttemptID = "att2"
	impl2.FenceToken = "fence-2"
	impl2.Owner = "other-owner"
	_, err = dispatchComposition(context.Background(), b, impl2, journal)
	if err == nil || !strings.Contains(err.Error(), "idempotency") {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestComposeDispatchIdempotentReplayAfterDeadlineExpiry(t *testing.T) {
	root := t.TempDir()
	shortDL := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if req != nil {
			req.DeadlineRFC3339 = shortDL
		}
		if implReq != nil {
			implReq.DeadlineRFC3339 = shortDL
		}
	})
	// Mutate callback runs before hashes for request, but impl hashes are set after.
	// Re-link hashes because request deadline changed.
	reqHash, err := hashCompositionRequest(b.Request)
	if err != nil {
		t.Fatal(err)
	}
	b.Decisions[0].RequestHash = reqHash
	decHash, err := hashComponentDecision(b.Decisions[0])
	if err != nil {
		t.Fatal(err)
	}
	b.Manifest.RequestHash = reqHash
	b.Manifest.DecisionHashes = []string{decHash}
	b.Manifest.Bindings[0].DecisionHash = decHash
	manHash, err := hashApplicationManifest(b.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	impl.RequestHash = reqHash
	impl.ManifestHash = manHash
	impl.DeadlineRFC3339 = shortDL
	impl.IdempotencyKey = b.Request.ParentID + "|" + reqHash + "|intent-edit"

	journal := filepath.Join(root, "journal")
	first, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if first.State != AttemptVerifiedPendingReview {
		t.Fatalf("state=%s err=%q", first.State, first.Error)
	}
	time.Sleep(3 * time.Second)
	if err := os.WriteFile(filepath.Join(impl.Workspace, "candidate.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatalf("expired deadline must still observationally replay: %v", err)
	}
	if second.AttemptID != first.AttemptID || second.ContentSHA256 != first.ContentSHA256 {
		t.Fatalf("replay after expiry changed result: first=%+v second=%+v", first, second)
	}
	body, err := os.ReadFile(filepath.Join(impl.Workspace, "candidate.txt"))
	if err != nil || string(body) != "dirty" {
		t.Fatalf("replay must not re-run delegate after expiry; body=%q", body)
	}
}

func TestComposeDispatchPoisonedVerifierRejectedBeforeExec(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "verifier-ran")
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		poison := filepath.Join(root, "poison.sh")
		composeTestWriteScript(t, poison, "#!/bin/sh\nprintf 'new' > candidate.txt\nprintf 'hacked' > verify.sh\n")
		implReq.DelegateArgv = []string{poison}
		wrapped := filepath.Join(root, "wrap-verify.sh")
		composeTestWriteScript(t, wrapped, "#!/bin/sh\ntouch '"+marker+"'\nexit 0\n")
		implReq.VerifyCommand = []string{wrapped}
	})
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal, got %s err=%v result.err=%q", result.State, err, result.Error)
	}
	if !strings.Contains(result.Error, "protected") {
		t.Fatalf("want protected rejection, got %q", result.Error)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("poisoned acceptance path must not execute verifier")
	}
}

func TestComposeDispatchOutsideWriteRejected(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		escape := filepath.Join(root, "escape.sh")
		composeTestWriteScript(t, escape, "#!/bin/sh\nprintf 'new' > candidate.txt\nprintf 'nope' > readonly.txt\n")
		implReq.DelegateArgv = []string{escape}
	})
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal, got %s (%v) (%q)", result.State, err, result.Error)
	}
	if !strings.Contains(result.Error, "read-only") && !strings.Contains(result.Error, "forbidden") {
		t.Fatalf("want read-only/forbidden rejection, got %q", result.Error)
	}
}

func TestComposeDispatchSymlinkOutputRejected(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	link := filepath.Join(root, "out-link")
	if err := os.Symlink(impl.OutputDir, link); err != nil {
		t.Fatal(err)
	}
	impl.OutputDir = link
	_, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink rejection, got %v", err)
	}
}

func TestComposeDispatchIndexChangeRejected(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		script := filepath.Join(root, "index.sh")
		composeTestWriteScript(t, script, "#!/bin/sh\nprintf 'new' > candidate.txt\ngit add candidate.txt\n")
		implReq.DelegateArgv = []string{script}
	})
	result, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal, got %s (%v) (%q)", result.State, err, result.Error)
	}
	if !strings.Contains(result.Error, "index") && !strings.Contains(result.Error, "HEAD") {
		t.Fatalf("want index/HEAD rejection, got %q", result.Error)
	}
}

func TestComposeDispatchCancelWithChild(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		hang := filepath.Join(root, "hang.sh")
		composeTestWriteScript(t, hang, "#!/bin/sh\nsleep 60\n")
		implReq.DelegateArgv = []string{hang}
		implReq.MaxSeconds = 30
	})
	journal := filepath.Join(root, "journal")
	errCh := make(chan error, 1)
	var result AttemptResult
	go func() {
		var err error
		result, err = dispatchComposition(context.Background(), b, impl, journal)
		errCh <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for attempt request.json")
		}
		if _, err := os.Stat(filepath.Join(journal, "attempts", impl.AttemptID, "request.json")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := requestCompositionCancel(journal, impl.AttemptID, impl.FenceToken); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if result.State != AttemptBlockedUncertain {
			t.Fatalf("want blocked_uncertain, got %s err=%v result.err=%q", result.State, err, result.Error)
		}
		if !result.UnresolvedEffects {
			t.Fatal("cancel must mark unresolved_effects")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not finish after cancel")
	}
}

func TestComposeDispatchStaleCancelFence(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	if _, err := dispatchComposition(context.Background(), b, impl, journal); err != nil {
		t.Fatal(err)
	}
	err := requestCompositionCancel(journal, impl.AttemptID, "wrong-fence")
	if err == nil || !strings.Contains(err.Error(), "fence") {
		t.Fatalf("want stale fence error, got %v", err)
	}
}

func TestComposeDispatchParentBudgetExhaustion(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if req != nil {
			req.Budget.MaxAttempts = 1
			req.Budget.MaxEstimatedUSD = 0.1
			req.Budget.ReservedUSD = 0.1
		}
		if implReq != nil {
			implReq.ReservedUSD = 0.1
		}
	})
	journal := filepath.Join(root, "journal")
	if _, err := dispatchComposition(context.Background(), b, impl, journal); err != nil {
		t.Fatal(err)
	}
	impl2 := impl
	impl2.AttemptID = "att2"
	impl2.FenceToken = "fence-2"
	impl2.IdempotencyKey = impl.IdempotencyKey + "-2"
	_, err := dispatchComposition(context.Background(), b, impl2, journal)
	if err == nil || (!strings.Contains(err.Error(), "max_attempts") && !strings.Contains(err.Error(), "exceeds")) {
		t.Fatalf("want budget exhaustion, got %v", err)
	}
}

func TestComposeDispatchDelegateFailure(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		fail := filepath.Join(root, "fail.sh")
		composeTestWriteScript(t, fail, "#!/bin/sh\nexit 7\n")
		implReq.DelegateArgv = []string{fail}
	})
	result, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal, got %s (%v)", result.State, err)
	}
}

func TestComposeDispatchCancelDuringVerifier(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		hangVerify := filepath.Join(root, "hang-verify.sh")
		composeTestWriteScript(t, hangVerify, "#!/bin/sh\nsleep 60\n")
		implReq.VerifyCommand = []string{hangVerify}
		implReq.MaxSeconds = 30
	})
	journal := filepath.Join(root, "journal")
	errCh := make(chan error, 1)
	var result AttemptResult
	go func() {
		var err error
		result, err = dispatchComposition(context.Background(), b, impl, journal)
		errCh <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for running attempt")
		}
		if _, err := os.Stat(filepath.Join(journal, "attempts", impl.AttemptID, "request.json")); err == nil {
			// Wait until delegate finishes and verifier starts (candidate written).
			if body, err := os.ReadFile(filepath.Join(impl.Workspace, "candidate.txt")); err == nil && string(body) == "new" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := requestCompositionCancel(journal, impl.AttemptID, impl.FenceToken); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if result.State != AttemptBlockedUncertain {
			t.Fatalf("want blocked_uncertain from verifier cancel, got %s err=%v result.err=%q", result.State, err, result.Error)
		}
		if !result.UnresolvedEffects {
			t.Fatal("verifier cancel must mark unresolved_effects")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch did not finish after verifier cancel")
	}
}

func TestComposeDispatchRejectsMaxSecondsAboveParent(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if req != nil {
			req.Budget.MaxSeconds = 10
		}
		if implReq != nil {
			implReq.MaxSeconds = 60
		}
	})
	_, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if err == nil || !strings.Contains(err.Error(), "max_seconds") {
		t.Fatalf("want max_seconds rejection, got %v", err)
	}
}

func TestComposeDispatchWorkspaceLeaseBlocksConcurrent(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		hang := filepath.Join(root, "hang.sh")
		composeTestWriteScript(t, hang, "#!/bin/sh\nsleep 60\n")
		implReq.DelegateArgv = []string{hang}
		implReq.MaxSeconds = 30
	})
	journal1 := filepath.Join(root, "journal1")
	errCh := make(chan error, 1)
	go func() {
		_, err := dispatchComposition(context.Background(), b, impl, journal1)
		errCh <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first lease")
		}
		if _, err := os.Stat(filepath.Join(impl.Workspace, ".fanisi-lock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	impl2 := impl
	impl2.AttemptID = "att2"
	impl2.FenceToken = "fence-2"
	impl2.IdempotencyKey = impl.IdempotencyKey + "-2"
	impl2.OutputDir = filepath.Join(root, "output2")
	_, err := dispatchComposition(context.Background(), b, impl2, filepath.Join(root, "journal2"))
	if err == nil {
		t.Fatal("want workspace lease conflict")
	}
	msg := err.Error()
	if !strings.Contains(msg, "lock") && !strings.Contains(msg, "lease") && !strings.Contains(msg, "unavailable") {
		t.Fatalf("want workspace lease conflict, got %v", err)
	}
	_ = requestCompositionCancel(journal1, impl.AttemptID, impl.FenceToken)
	select {
	case <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("first dispatch did not finish")
	}
}

func TestComposeDispatchVerifierMutatesUndeclaredTrackedFile(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(_ *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if implReq == nil {
			return
		}
		evil := filepath.Join(root, "evil-verify.sh")
		composeTestWriteScript(t, evil, "#!/bin/sh\nprintf 'x' > other.txt\nexit 0\n")
		implReq.VerifyCommand = []string{evil}
	})
	// other.txt is tracked at base but not in write_paths.
	if err := os.WriteFile(filepath.Join(impl.Workspace, "other.txt"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	// Rebuild git clean base with other.txt tracked.
	cmd := exec.Command("git", "add", "other.txt")
	cmd.Dir = impl.Workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "commit", "-m", "other")
	cmd.Dir = impl.Workspace
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fanisi", "GIT_AUTHOR_EMAIL=fanisi@example.com",
		"GIT_COMMITTER_NAME=fanisi", "GIT_COMMITTER_EMAIL=fanisi@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	head, err := gitOutput(context.Background(), impl.Workspace, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	b.Request.ProductRevision = strings.TrimSpace(string(head))
	reqHash, err := hashCompositionRequest(b.Request)
	if err != nil {
		t.Fatal(err)
	}
	b.Decisions[0].RequestHash = reqHash
	decHash, err := hashComponentDecision(b.Decisions[0])
	if err != nil {
		t.Fatal(err)
	}
	b.Manifest.RequestHash = reqHash
	b.Manifest.DecisionHashes = []string{decHash}
	b.Manifest.Bindings[0].DecisionHash = decHash
	manHash, err := hashApplicationManifest(b.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	impl.RequestHash = reqHash
	impl.ManifestHash = manHash
	impl.IdempotencyKey = b.Request.ParentID + "|" + reqHash + "|intent-edit"

	result, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal for undeclared mutation, got %s (%v) (%q)", result.State, err, result.Error)
	}
	if !strings.Contains(result.Error, "forbidden") && !strings.Contains(result.Error, "other.txt") {
		t.Fatalf("want forbidden other.txt, got %q", result.Error)
	}
}

func TestComposeVerifierEnvProvidesControlledCaches(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	env, err := composeVerifierEnv(nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, need := range []string{"HOME=", "GOCACHE=", "GOTMPDIR=", "GOMODCACHE=", "PATH="} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing %s in %#v", need, env)
		}
	}
	if strings.Contains(joined, os.Getenv("HOME")+"\n") && os.Getenv("HOME") != "" && !strings.Contains(joined, "HOME="+filepath.Join(cache, "home")) {
		t.Fatal("verifier must not copy operator HOME")
	}
	home := filepath.Join(cache, "home")
	if _, err := os.Stat(home); err != nil {
		t.Fatal(err)
	}
}
