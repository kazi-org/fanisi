package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// recordCompositionReview appends a separate review event. It never mutates the
// immutable AttemptResult (so result content_sha256 stays stable). Under the
// journal lock, latest state is derived from events so terminal accept/reject
// cannot be reversed by a second review. Agent kind cannot accept. Human accept
// rechecks candidate/protected/read-only/HEAD/index and forbidden scope paths.
func recordCompositionReview(journalDir string, r CompositionReview) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = compositionSchemaVersion
	}
	if r.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("review schema_version must be %d", compositionSchemaVersion)
	}
	if r.Decision != "accept" && r.Decision != "reject" {
		return errors.New("review decision must be accept or reject")
	}
	if strings.TrimSpace(r.Reviewer) == "" || (r.Kind != "human" && r.Kind != "agent") {
		return errors.New("review requires reviewer and human|agent kind")
	}
	if strings.TrimSpace(r.Notes) == "" || len(r.Notes) > 16000 {
		return errors.New("review requires substantive bounded notes")
	}
	if !simpleID(r.AttemptID) {
		return errors.New("attempt_id must be a simple identifier")
	}
	if strings.TrimSpace(r.ResultHash) == "" {
		return errors.New("result_sha256 is required")
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	} else {
		r.At = r.At.UTC()
	}

	j, err := openJournal(journalDir)
	if err != nil {
		return err
	}
	unlock, err := j.lock()
	if err != nil {
		return err
	}
	defer unlock()

	req, result, err := j.loadAttempt(r.AttemptID)
	if err != nil {
		return err
	}
	if result.AttemptID == "" {
		return errors.New("attempt has no result to review")
	}
	gotHash, err := hashAttemptResult(result)
	if err != nil {
		return err
	}
	if result.ContentSHA256 != "" && result.ContentSHA256 != gotHash {
		return errors.New("stored attempt result hash mismatch")
	}
	if r.ResultHash != gotHash && (result.ContentSHA256 == "" || r.ResultHash != result.ContentSHA256) {
		return errors.New("review result_sha256 does not match immutable attempt result")
	}

	observed, err := composeObservedAttemptState(j, r.AttemptID, result)
	if err != nil {
		return err
	}
	switch observed {
	case AttemptAccepted, AttemptRejected:
		return fmt.Errorf("attempt already reviewed (%s); repeated or conflicting reviews are rejected", observed)
	}

	toState := AttemptRejected
	if r.Decision == "accept" {
		if r.Kind != "human" {
			return errors.New("agent review cannot accept; human operator review required")
		}
		if observed != AttemptVerifiedPendingReview {
			return fmt.Errorf("acceptance requires verified_pending_review, got %s", observed)
		}
		if err := composeRecheckReviewEvidence(context.Background(), j.Dir, req, result); err != nil {
			return err
		}
		toState = AttemptAccepted
	} else {
		if observed != AttemptVerifiedPendingReview && observed != AttemptBlockedUncertain {
			return fmt.Errorf("reject requires verified_pending_review or blocked_uncertain, got %s", observed)
		}
	}

	reviewPath := filepath.Join(composeAttemptDir(j.Dir, r.AttemptID), fmt.Sprintf("review-%020d.json", r.At.UnixNano()))
	if err := composeWriteNewJSON(reviewPath, r); err != nil {
		return err
	}
	return j.appendEvent(JournalEvent{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     r.AttemptID,
		FenceToken:    req.FenceToken,
		FromState:     observed,
		ToState:       toState,
		Actor:         r.Reviewer,
		PayloadSHA:    r.ResultHash,
		Note:          fmt.Sprintf("composition review %s kind=%s", r.Decision, r.Kind),
	})
}

func composeRecheckReviewEvidence(ctx context.Context, journalDir string, req ImplementationRequest, result AttemptResult) error {
	readOnly := composeReadOnlyPaths(req.ReadPaths, req.WritePaths)
	allowed := composeUniquePaths(append(append(append([]string{}, req.ReadPaths...), req.WritePaths...), req.ProtectedPaths...))
	source, err := composeSnapshotRelFiles(req.Workspace, readOnly, allowed)
	if err != nil {
		return err
	}
	if changed(result.SourceHashes, source) {
		return errors.New("read-only source hashes changed since verification")
	}
	protected, err := composeSnapshotRelFiles(req.Workspace, req.ProtectedPaths, allowed)
	if err != nil {
		return err
	}
	if changed(result.ProtectedHashes, protected) {
		return errors.New("protected hashes changed since verification")
	}
	writes, err := composeSnapshotRelFiles(req.Workspace, req.WritePaths, allowed)
	if err != nil {
		return err
	}
	if changed(result.FinalWriteHashes, writes) {
		return errors.New("candidate write hashes changed since verification")
	}
	var pre composeAttemptSnapshots
	if err := readStrictJSON(filepath.Join(composeAttemptDir(journalDir, req.AttemptID), "snapshots", "pre.json"), &pre); err != nil {
		return err
	}
	gitSnap, err := composeCaptureGit(ctx, req.Workspace)
	if err != nil {
		return err
	}
	if gitSnap.HEAD != pre.Git.HEAD {
		return errors.New("worktree HEAD changed since verification")
	}
	if gitSnap.IndexTree != pre.Git.IndexTree {
		return errors.New("git index changed since verification")
	}
	if err := composeDetectForbiddenWrites(ctx, req.Workspace, req.WritePaths); err != nil {
		return fmt.Errorf("before review acceptance: %w", err)
	}
	return nil
}
