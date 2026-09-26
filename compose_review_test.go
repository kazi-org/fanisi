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
