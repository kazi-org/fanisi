package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeReviewAgentCannotAccept(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "agent-bot",
		Kind:          "agent",
		ResultHash:    result.ContentSHA256,
		Notes:         "agent claims acceptance",
	})
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("want agent accept rejection, got %v", err)
	}
	statuses, err := compositionStatus(journal, result.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != AttemptVerifiedPendingReview {
		t.Fatalf("state mutated to %s", statuses[0].State)
	}
}

func TestComposeReviewHumanAcceptAndRejectAgentPath(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	hashBefore := result.ContentSHA256
	if err := recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    hashBefore,
		Notes:         "local operator accepts verified candidate",
	}); err != nil {
		t.Fatal(err)
	}
	// Result file must remain immutable (hash stable; no review fields injected).
	_, stored, err := func() (ImplementationRequest, AttemptResult, error) {
		j, err := openJournal(journal)
		if err != nil {
			return ImplementationRequest{}, AttemptResult{}, err
		}
		unlock, err := j.lock()
		if err != nil {
			return ImplementationRequest{}, AttemptResult{}, err
		}
		defer unlock()
		return j.loadAttempt(result.AttemptID)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContentSHA256 != hashBefore || stored.State != AttemptVerifiedPendingReview {
		t.Fatalf("result.json mutated: state=%s hash=%s", stored.State, stored.ContentSHA256)
	}
	statuses, err := compositionStatus(journal, result.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != AttemptAccepted {
		t.Fatalf("status overlay want accepted, got %s", statuses[0].State)
	}
}

func TestComposeReviewRejectAgent(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "reject",
		Reviewer:      "agent-bot",
		Kind:          "agent",
		ResultHash:    result.ContentSHA256,
		Notes:         "agent recommends rejection",
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err := compositionStatus(journal, result.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != AttemptRejected {
		t.Fatalf("want rejected, got %s", statuses[0].State)
	}
}

func TestComposeReviewRepeatedDecisionRejected(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "reject",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "first terminal review",
	}); err != nil {
		t.Fatal(err)
	}
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "conflicting reverse accept must fail",
	})
	if err == nil || !strings.Contains(err.Error(), "already reviewed") {
		t.Fatalf("want repeated/conflicting review rejection, got %v", err)
	}
	_, stored, err := func() (ImplementationRequest, AttemptResult, error) {
		j, err := openJournal(journal)
		if err != nil {
			return ImplementationRequest{}, AttemptResult{}, err
		}
		unlock, err := j.lock()
		if err != nil {
			return ImplementationRequest{}, AttemptResult{}, err
		}
		defer unlock()
		return j.loadAttempt(result.AttemptID)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != AttemptVerifiedPendingReview {
		t.Fatalf("immutable result state must stay verified_pending_review, got %s", stored.State)
	}
}

func TestComposeReviewPostVerifyEditBlocksHumanAccept(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(impl.Workspace, "candidate.txt"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "should fail because candidate drifted",
	})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("want post-verify change rejection, got %v", err)
	}
}

func TestComposeReviewOperatorModifiesUndeclaredTrackedBeforeAccept(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	// Modify a tracked non-write path after pending result (readonly.txt is frozen read-only).
	if err := os.WriteFile(filepath.Join(impl.Workspace, "readonly.txt"), []byte("operator-drift"), 0600); err != nil {
		t.Fatal(err)
	}
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "should fail because undeclared tracked path drifted",
	})
	if err == nil {
		t.Fatal("want acceptance blocked after undeclared modification")
	}
	if !strings.Contains(err.Error(), "changed") && !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("want source/forbidden rejection, got %v", err)
	}
}

func TestComposeReviewStaleResultHash(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, nil)
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "reject",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    strings.Repeat("0", 64),
		Notes:         "wrong hash",
	})
	if err == nil || !strings.Contains(err.Error(), "result_sha256") {
		t.Fatalf("want result hash mismatch, got %v", err)
	}
}

func TestComposeReviewAcceptFrozenWorkspaceVerifierOmittedFromProtectedPaths(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		// Workspace verifier remains VerifyCommand (absolute verify.sh) but is
		// omitted from configured ProtectedPaths so admission must auto-freeze it.
		if req != nil {
			req.ProtectedPaths = []string{"protected/accept.md"}
		}
		if implReq != nil {
			implReq.ProtectedPaths = []string{"protected/accept.md"}
		}
	})
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != AttemptVerifiedPendingReview {
		t.Fatalf("dispatch: %s %q", result.State, result.Error)
	}
	if _, ok := result.ProtectedHashes["verify.sh"]; !ok {
		t.Fatalf("expected auto-frozen verify.sh in ProtectedHashes, got %#v", result.ProtectedHashes)
	}
	if err := recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "accept with frozen workspace verifier omitted from configured protected_paths",
	}); err != nil {
		t.Fatalf("human accept must succeed when frozen protected set is rechecked: %v", err)
	}
	statuses, err := compositionStatus(journal, result.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != AttemptAccepted {
		t.Fatalf("want accepted, got %s", statuses[0].State)
	}
}

func TestComposeDispatchDelegateMutatesFrozenWorkspaceVerifierRejected(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if req != nil {
			req.WritePaths = []string{"candidate.txt", "verify.sh"}
			req.ProtectedPaths = []string{"protected/accept.md"}
		}
		if implReq == nil {
			return
		}
		implReq.WritePaths = []string{"candidate.txt", "verify.sh"}
		implReq.ProtectedPaths = []string{"protected/accept.md"}
		poison := filepath.Join(root, "mutate-verify.sh")
		composeTestWriteScript(t, poison, "#!/bin/sh\nprintf 'new' > candidate.txt\nprintf '#!/bin/sh\\nexit 0\\n' > verify.sh\n")
		implReq.DelegateArgv = []string{poison}
	})
	result, err := dispatchComposition(context.Background(), b, impl, filepath.Join(root, "journal"))
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal when delegate mutates frozen verifier, got %s (%v) (%q)", result.State, err, result.Error)
	}
	if !strings.Contains(result.Error, "protected") {
		t.Fatalf("want protected-input rejection, got %q", result.Error)
	}
	if !strings.Contains(result.Error, "verifier not executed") {
		t.Fatalf("want verifier-not-executed evidence, got %q", result.Error)
	}
	if result.VerifierLogSHA != "" {
		t.Fatalf("verifier must not leave a log after frozen verifier mutation, got %q", result.VerifierLogSHA)
	}
}

func TestComposeReviewPostVerifyFrozenVerifierChangeBlocksAccept(t *testing.T) {
	root := t.TempDir()
	b, impl := composeTestBundleAndImpl(t, root, func(req *CompositionRequest, implReq *ImplementationRequest, _ map[string]string) {
		if req != nil {
			req.ProtectedPaths = []string{"protected/accept.md"}
		}
		if implReq != nil {
			implReq.ProtectedPaths = []string{"protected/accept.md"}
		}
	})
	journal := filepath.Join(root, "journal")
	result, err := dispatchComposition(context.Background(), b, impl, journal)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != AttemptVerifiedPendingReview {
		t.Fatalf("dispatch: %s %q", result.State, result.Error)
	}
	composeTestWriteScript(t, filepath.Join(impl.Workspace, "verify.sh"), "#!/bin/sh\nexit 0\n# tampered after verify\n")
	err = recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     result.AttemptID,
		Decision:      "accept",
		Reviewer:      "david",
		Kind:          "human",
		ResultHash:    result.ContentSHA256,
		Notes:         "should fail because frozen workspace verifier drifted after verification",
	})
	if err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("want post-verify frozen verifier rejection, got %v", err)
	}
}
