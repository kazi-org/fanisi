package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type composeGitSnapshot struct {
	HEAD         string `json:"head"`
	IndexTree    string `json:"index_tree"`
	Porcelain    string `json:"porcelain"`
	StatusSHA256 string `json:"status_sha256"`
}

type composeAttemptSnapshots struct {
	SourceHashes    map[string]string  `json:"source_hashes"`
	ProtectedHashes map[string]string  `json:"protected_hashes"`
	WriteHashes     map[string]string  `json:"write_hashes"`
	Git             composeGitSnapshot `json:"git"`
	VerifyCommand   []string           `json:"verify_command"`
	ProtectedInputs []string           `json:"protected_inputs"`
}

// dispatchComposition validates the full bundle, reserves budget, persists fence
// state, runs the local delegate, rejects poisoned protected changes before the
// verifier, and records a durable AttemptResult. No automatic retry or resume.
//
// Same-payload idempotent replay is observational and happens after durable
// lookup (and payload hash verification) even when deadlines have expired or the
// workspace is dirty; execution-only future-deadline and clean-git gates run only
// for new attempts.
func dispatchComposition(ctx context.Context, b CompositionBundle, impl ImplementationRequest, journalDir string) (AttemptResult, error) {
	var empty AttemptResult
	if err := validateCompositionBundle(b); err != nil {
		return empty, fmt.Errorf("composition bundle: %w", err)
	}
	if err := composeValidateImplAgainstBundle(b, impl); err != nil {
		return empty, err
	}
	ws, err := composeCanonicalRoot(impl.Workspace)
	if err != nil {
		return empty, fmt.Errorf("workspace: %w", err)
	}
	impl.Workspace = ws
	outDir, err := composeCanonicalRoot(impl.OutputDir)
	if err != nil {
		return empty, fmt.Errorf("output_dir: %w", err)
	}
	impl.OutputDir = outDir

	j, err := openJournal(journalDir)
	if err != nil {
		return empty, err
	}
	if err := composeRejectSymlinkPath(impl.OutputDir); err != nil {
		return empty, fmt.Errorf("output_dir: %w", err)
	}
	if err := composeEnsureOutsideProductWrites(impl.Workspace, impl.WritePaths, impl.OutputDir); err != nil {
		return empty, fmt.Errorf("output_dir: %w", err)
	}
	if err := composeEnsureOutsideProductWrites(impl.Workspace, impl.WritePaths, j.Dir); err != nil {
		return empty, fmt.Errorf("journal: %w", err)
	}

	payloadSHA, err := hashImplementationRequest(impl)
	if err != nil {
		return empty, err
	}

	unlock, err := j.lock()
	if err != nil {
		return empty, err
	}
	idemPath := filepath.Join(j.Dir, composeIdempotencyDirName, composeSafeFileToken(impl.IdempotencyKey)+".json")
	if _, err := os.Lstat(idemPath); err == nil {
		var rec composeIdempotencyRecord
		if err := readStrictJSON(idemPath, &rec); err != nil {
			unlock()
			return empty, err
		}
		if rec.PayloadSHA != payloadSHA {
			unlock()
			return empty, errors.New("idempotency key conflict: different payload")
		}
		observed, loadErr := composeLoadObservedResult(j, rec.AttemptID)
		unlock()
		if loadErr != nil {
			return empty, loadErr
		}
		return observed, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		unlock()
		return empty, err
	}

	// Execution-only gates after durable replay lookup.
	if err := composeRequireFutureDeadlines(b.Request.DeadlineRFC3339, impl.DeadlineRFC3339); err != nil {
		unlock()
		return empty, err
	}
	maxSec, err := composeEffectiveMaxSeconds(impl.MaxSeconds, b.Request.Budget.MaxSeconds)
	if err != nil {
		unlock()
		return empty, err
	}
	attemptCtx, cancelAttempt, err := composeAttemptContext(ctx, b.Request.DeadlineRFC3339, impl.DeadlineRFC3339, maxSec)
	if err != nil {
		unlock()
		return empty, err
	}
	defer cancelAttempt()

	if err := j.reserveBudget(impl.ParentID, b.Request.Budget, impl.ReservedUSD); err != nil {
		unlock()
		return empty, err
	}
	if err := composePersistReservation(j.Dir, impl.ParentID, impl.AttemptID, impl.ReservedUSD); err != nil {
		unlock()
		return empty, err
	}

	attemptDir := composeAttemptDir(j.Dir, impl.AttemptID)
	if err := os.MkdirAll(filepath.Join(attemptDir, "snapshots"), 0700); err != nil {
		unlock()
		return empty, err
	}
	if err := composeWriteNewJSON(filepath.Join(attemptDir, "request.json"), impl); err != nil {
		unlock()
		return empty, err
	}
	if err := composeWriteNewJSON(filepath.Join(attemptDir, "allocation.json"), map[string]any{
		"schema_version": compositionSchemaVersion,
		"attempt_id":     impl.AttemptID,
		"parent_id":      impl.ParentID,
		"fence_token":    impl.FenceToken,
		"owner":          impl.Owner,
		"reserved_usd":   impl.ReservedUSD,
		"payload_sha256": payloadSHA,
		"at":             time.Now().UTC(),
	}); err != nil {
		unlock()
		return empty, err
	}
	if err := composeWriteNewJSON(idemPath, composeIdempotencyRecord{
		SchemaVersion: compositionSchemaVersion,
		Key:           impl.IdempotencyKey,
		AttemptID:     impl.AttemptID,
		PayloadSHA:    payloadSHA,
	}); err != nil {
		unlock()
		return empty, err
	}
	if err := j.appendEvent(JournalEvent{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     impl.AttemptID,
		FenceToken:    impl.FenceToken,
		FromState:     AttemptValidated,
		ToState:       AttemptDispatched,
		Actor:         impl.Owner,
		PayloadSHA:    payloadSHA,
		Note:          "dispatch persisted before spawn",
	}); err != nil {
		unlock()
		return empty, err
	}
	if err := j.appendEvent(JournalEvent{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     impl.AttemptID,
		FenceToken:    impl.FenceToken,
		FromState:     AttemptDispatched,
		ToState:       AttemptRunning,
		Actor:         "fanisi-delegate",
		Note:          "spawn beginning",
	}); err != nil {
		unlock()
		return empty, err
	}
	unlock()

	// Exclusive workspace execution lease (claimWorkspace). Journal stays unlocked
	// so cancel/status can proceed. Lease is not a stored PID kill target.
	releaseWS, err := claimWorkspace(impl.Workspace)
	if err != nil {
		return composeFinalizeAttempt(j, impl, nil, nil, nil, -1, "", AttemptBlockedUncertain, true, "workspace execution lease unavailable: "+err.Error(), UsageKnown{})
	}
	defer releaseWS()

	gitSnap, err := composeCaptureGit(attemptCtx, impl.Workspace)
	if err != nil {
		state, unresolved := composeClassifyCtxFailure(attemptCtx, err)
		return composeFinalizeAttempt(j, impl, nil, nil, nil, -1, "", state, unresolved, err.Error(), UsageKnown{})
	}
	if gitSnap.HEAD != b.Request.ProductRevision {
		return composeFinalizeAttempt(j, impl, nil, nil, nil, -1, "", AttemptFailedTerminal, false, fmt.Sprintf("product_revision %q does not match clean Git HEAD %q", b.Request.ProductRevision, gitSnap.HEAD), UsageKnown{})
	}
	if err := composeRequireCleanGit(gitSnap); err != nil {
		return composeFinalizeAttempt(j, impl, nil, nil, nil, -1, "", AttemptFailedTerminal, false, err.Error(), UsageKnown{})
	}

	readOnly := composeReadOnlyPaths(impl.ReadPaths, impl.WritePaths)
	scopeAllowed := composeUniquePaths(append(append(append([]string{}, impl.ReadPaths...), impl.WritePaths...), impl.ProtectedPaths...))
	sourceHashes, err := composeSnapshotRelFiles(impl.Workspace, readOnly, scopeAllowed)
	if err != nil {
		return composeFinalizeAttempt(j, impl, nil, nil, nil, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}
	protectedHashes, err := composeSnapshotRelFiles(impl.Workspace, impl.ProtectedPaths, scopeAllowed)
	if err != nil {
		return composeFinalizeAttempt(j, impl, sourceHashes, nil, nil, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}
	writeHashes, err := composeSnapshotRelFiles(impl.Workspace, impl.WritePaths, scopeAllowed)
	if err != nil {
		return composeFinalizeAttempt(j, impl, sourceHashes, protectedHashes, nil, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}
	snaps := composeAttemptSnapshots{
		SourceHashes:    sourceHashes,
		ProtectedHashes: protectedHashes,
		WriteHashes:     writeHashes,
		Git:             gitSnap,
		VerifyCommand:   append([]string{}, impl.VerifyCommand...),
		ProtectedInputs: append([]string{}, impl.ProtectedPaths...),
	}
	unlock, err = j.lock()
	if err != nil {
		return composeFinalizeAttempt(j, impl, sourceHashes, protectedHashes, writeHashes, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}
	if err := composeWriteNewJSON(filepath.Join(attemptDir, "snapshots", "pre.json"), snaps); err != nil {
		unlock()
		return composeFinalizeAttempt(j, impl, sourceHashes, protectedHashes, writeHashes, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}
	unlock()

	if err := os.MkdirAll(impl.OutputDir, 0700); err != nil {
		return composeFinalizeAttempt(j, impl, sourceHashes, protectedHashes, nil, -1, "", AttemptBlockedUncertain, true, err.Error(), UsageKnown{})
	}

	// Watcher stays alive through verification/finalization on the same attemptCtx.
	watchDone := make(chan struct{})
	go composeWatchCancel(attemptCtx, attemptDir, impl.FenceToken, cancelAttempt, watchDone)
	defer close(watchDone)

	usage := UsageKnown{}
	exitCode := -1

	exitCode, usage, delErr := runDelegate(attemptCtx, DelegateConfig{
		Argv:       impl.DelegateArgv,
		Workspace:  impl.Workspace,
		OutputDir:  impl.OutputDir,
		MaxSeconds: 0, // attemptCtx already carries duration + deadline bounds
		EnvNames:   impl.EnvNames,
	})

	canceled := composeAttemptCanceled(attemptCtx, attemptDir, impl.FenceToken, exitCode)

	if err := composeCheckIntegrity(attemptCtx, impl, gitSnap, sourceHashes, protectedHashes, readOnly, scopeAllowed, "before verifier"); err != nil {
		state := AttemptFailedTerminal
		unresolved := false
		if canceled || composeCtxInterrupted(attemptCtx, err) {
			state = AttemptBlockedUncertain
			unresolved = true
		}
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		return composeFinalizeAttempt(j, impl, postSource, postProt, nil, exitCode, "", state, unresolved, err.Error(), usage)
	}

	if canceled || (delErr != nil && exitCode == 124) {
		msg := "delegate canceled or timed out"
		if delErr != nil {
			msg = delErr.Error()
		}
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		return composeFinalizeAttempt(j, impl, postSource, postProt, nil, exitCode, "", AttemptBlockedUncertain, true, msg, usage)
	}
	if delErr != nil {
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		return composeFinalizeAttempt(j, impl, postSource, postProt, nil, exitCode, "", AttemptBlockedUncertain, true, delErr.Error(), usage)
	}
	if exitCode != 0 {
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		return composeFinalizeAttempt(j, impl, postSource, postProt, nil, exitCode, "", AttemptFailedTerminal, false, fmt.Sprintf("delegate exit %d", exitCode), usage)
	}

	verifyCache := filepath.Join(impl.OutputDir, "verifier-cache")
	if err := os.MkdirAll(verifyCache, 0700); err != nil {
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		return composeFinalizeAttempt(j, impl, postSource, postProt, nil, exitCode, "", AttemptBlockedUncertain, true, err.Error(), usage)
	}
	partial, verErr := composeRunIndependentVerifier(attemptCtx, impl.Workspace, impl.VerifyCommand, impl.WritePaths, impl.ProtectedPaths, impl.EnvNames, verifyCache)
	finalWrites := partial.FinalWriteHashes
	finalProt := partial.ProtectedHashes

	canceled = composeAttemptCanceled(attemptCtx, attemptDir, impl.FenceToken, partial.VerifierExit)
	if canceled || (verErr != nil && partial.VerifierExit == 124) {
		msg := "verifier canceled or timed out"
		if verErr != nil {
			msg = verErr.Error()
		}
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		if finalProt == nil {
			finalProt = postProt
		}
		return composeFinalizeAttempt(j, impl, postSource, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptBlockedUncertain, true, msg, usage)
	}

	if err := composeCheckIntegrity(attemptCtx, impl, gitSnap, sourceHashes, protectedHashes, readOnly, scopeAllowed, "after verifier"); err != nil {
		state := AttemptFailedTerminal
		unresolved := false
		if composeAttemptCanceled(attemptCtx, attemptDir, impl.FenceToken, partial.VerifierExit) || composeCtxInterrupted(attemptCtx, err) {
			state = AttemptBlockedUncertain
			unresolved = true
		}
		postSource, postProt, _ := composeCurrentHashes(impl, readOnly, scopeAllowed)
		if finalProt == nil {
			finalProt = postProt
		}
		return composeFinalizeAttempt(j, impl, postSource, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, state, unresolved, err.Error(), usage)
	}
	postSource2, postProt2, err := composeCurrentHashes(impl, readOnly, scopeAllowed)
	if err != nil {
		return composeFinalizeAttempt(j, impl, postSource2, postProt2, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptBlockedUncertain, true, err.Error(), usage)
	}
	if finalProt == nil {
		finalProt = postProt2
	}
	if verErr != nil {
		return composeFinalizeAttempt(j, impl, postSource2, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptFailedTerminal, false, verErr.Error(), usage)
	}
	if partial.VerifierExit != 0 {
		return composeFinalizeAttempt(j, impl, postSource2, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptFailedTerminal, false, fmt.Sprintf("verifier exit %d", partial.VerifierExit), usage)
	}
	// Deadline may expire during post-verifier git/hash work: recheck before success.
	if composeAttemptCanceled(attemptCtx, attemptDir, impl.FenceToken, partial.VerifierExit) {
		return composeFinalizeAttempt(j, impl, postSource2, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptBlockedUncertain, true, "attempt canceled or deadline expired before finalization", usage)
	}
	return composeFinalizeAttempt(j, impl, postSource2, finalProt, finalWrites, partial.VerifierExit, partial.VerifierLogSHA, AttemptVerifiedPendingReview, false, "", usage)
}

func requestCompositionCancel(journalDir, attemptID, fence string) error {
	if !simpleID(attemptID) {
		return errors.New("attempt_id must be a simple identifier")
	}
	if strings.TrimSpace(fence) == "" {
		return errors.New("fence token is required")
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
	req, result, err := j.loadAttempt(attemptID)
	if err != nil {
		return err
	}
	if req.FenceToken != fence {
		return errors.New("stale fence token for cancel")
	}
	observed, err := composeObservedAttemptState(j, attemptID, result)
	if err != nil {
		return err
	}
	switch observed {
	case AttemptFailedTerminal, AttemptBlockedUncertain, AttemptVerifiedPendingReview, AttemptAccepted, AttemptRejected:
		return fmt.Errorf("attempt already terminal (%s)", observed)
	}
	path := filepath.Join(composeAttemptDir(j.Dir, attemptID), "cancel-request.json")
	return composeWriteNewJSON(path, map[string]any{
		"schema_version": compositionSchemaVersion,
		"attempt_id":     attemptID,
		"fence_token":    fence,
		"at":             time.Now().UTC(),
		"actor":          "local-operator",
	})
}

func compositionStatus(journalDir, attemptID string) ([]AttemptResult, error) {
	j, err := openJournal(journalDir)
	if err != nil {
		return nil, err
	}
	unlock, err := j.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if attemptID != "" {
		if !simpleID(attemptID) {
			return nil, errors.New("attempt_id must be a simple identifier")
		}
		result, err := composeLoadObservedResult(j, attemptID)
		if err != nil {
			return nil, err
		}
		return []AttemptResult{result}, nil
	}
	entries, err := os.ReadDir(filepath.Join(j.Dir, composeAttemptsDirName))
	if err != nil {
		return nil, err
	}
	var out []AttemptResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		result, err := composeLoadObservedResult(j, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

// composeLoadObservedResult returns the immutable result with event-derived state
// overlaid (result.json is never rewritten for review).
func composeLoadObservedResult(j *Journal, attemptID string) (AttemptResult, error) {
	req, result, err := j.loadAttempt(attemptID)
	if err != nil {
		return AttemptResult{}, err
	}
	base := AttemptRunning
	if result.AttemptID != "" {
		base = result.State
	} else {
		result = AttemptResult{
			SchemaVersion: compositionSchemaVersion,
			AttemptID:     req.AttemptID,
			FenceToken:    req.FenceToken,
			State:         AttemptRunning,
			Usage:         UsageKnown{},
		}
		base = AttemptRunning
	}
	observed, err := composeObservedAttemptState(j, attemptID, AttemptResult{AttemptID: attemptID, State: base})
	if err != nil {
		return result, err
	}
	if result.AttemptID == "" {
		result.AttemptID = req.AttemptID
		result.FenceToken = req.FenceToken
		result.SchemaVersion = compositionSchemaVersion
	}
	result.State = observed
	return result, nil
}

// composeObservedAttemptState derives the latest attempt state from journal events
// under the caller's journal lock. The immutable result.State is only the base
// when no later event exists.
func composeObservedAttemptState(j *Journal, attemptID string, result AttemptResult) (AttemptState, error) {
	state := AttemptRunning
	if result.AttemptID != "" {
		state = result.State
	}
	events, err := composeListEventFiles(j.Dir)
	if err != nil {
		return state, err
	}
	for _, path := range events {
		var e JournalEvent
		if err := readJSON(path, &e); err != nil {
			return state, err
		}
		if e.AttemptID != attemptID || e.ToState == "" {
			continue
		}
		state = e.ToState
	}
	return state, nil
}

func composeFinalizeAttempt(j *Journal, impl ImplementationRequest, source, protected, writes map[string]string, verifierExit int, verifierLogSHA string, state AttemptState, unresolved bool, errMsg string, usage UsageKnown) (AttemptResult, error) {
	result := AttemptResult{
		SchemaVersion:     compositionSchemaVersion,
		AttemptID:         impl.AttemptID,
		FenceToken:        impl.FenceToken,
		SourceHashes:      source,
		ProtectedHashes:   protected,
		FinalWriteHashes:  writes,
		VerifierExit:      verifierExit,
		VerifierLogSHA:    verifierLogSHA,
		State:             state,
		Usage:             usage,
		UnresolvedEffects: unresolved,
		Error:             errMsg,
	}
	hash, err := hashAttemptResult(result)
	if err != nil {
		return result, err
	}
	result.ContentSHA256 = hash

	unlock, err := j.lock()
	if err != nil {
		return result, err
	}
	defer unlock()

	req, existing, err := j.loadAttempt(impl.AttemptID)
	if err != nil {
		return result, err
	}
	if req.FenceToken != impl.FenceToken {
		return result, errors.New("stale completion fence")
	}
	if existing.AttemptID != "" {
		return existing, errors.New("attempt result already recorded")
	}
	path := filepath.Join(composeAttemptDir(j.Dir, impl.AttemptID), "result.json")
	if err := composeWriteNewJSON(path, result); err != nil {
		return result, err
	}
	from := AttemptRunning
	if err := j.appendEvent(JournalEvent{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     impl.AttemptID,
		FenceToken:    impl.FenceToken,
		FromState:     from,
		ToState:       state,
		Actor:         "fanisi-delegate",
		PayloadSHA:    hash,
		Note:          errMsg,
	}); err != nil {
		return result, err
	}
	if state == AttemptVerifiedPendingReview {
		return result, nil
	}
	if errMsg == "" {
		errMsg = string(state)
	}
	return result, errors.New(errMsg)
}

func composeValidateImplAgainstBundle(b CompositionBundle, impl ImplementationRequest) error {
	if impl.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("implementation schema_version must be %d", compositionSchemaVersion)
	}
	if !simpleID(impl.AttemptID) || !simpleID(impl.ParentID) {
		return errors.New("attempt_id and parent_id must be simple identifiers")
	}
	if strings.TrimSpace(impl.FenceToken) == "" || strings.TrimSpace(impl.Owner) == "" {
		return errors.New("fence_token and owner are required")
	}
	if strings.TrimSpace(impl.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	if len(impl.DelegateArgv) == 0 || len(impl.VerifyCommand) == 0 {
		return errors.New("delegate_argv and verify_command are required")
	}
	if impl.Workspace == "" || impl.OutputDir == "" {
		return errors.New("workspace and output_dir are required")
	}
	if impl.ParentID != b.Request.ParentID {
		return errors.New("implementation parent_id must match request parent_id")
	}
	reqHash, err := hashCompositionRequest(b.Request)
	if err != nil {
		return err
	}
	if impl.RequestHash != reqHash {
		return errors.New("implementation request_hash does not match composition request")
	}
	manHash, err := hashApplicationManifest(b.Manifest)
	if err != nil {
		return err
	}
	if impl.ManifestHash != manHash {
		return errors.New("implementation manifest_hash does not match application manifest")
	}
	if !composeSameStringSet(impl.WritePaths, b.Request.WritePaths) {
		return errors.New("implementation write_paths must match request write_paths")
	}
	if !composeSameStringSet(impl.ProtectedPaths, b.Request.ProtectedPaths) {
		return errors.New("implementation protected_paths must match request protected_paths")
	}
	reqReads := b.Request.ReadPaths
	if len(reqReads) == 0 {
		reqReads = nil
	}
	if !composeSameStringSet(impl.ReadPaths, reqReads) {
		return errors.New("implementation read_paths must match request read_paths")
	}
	if impl.Workspace != b.Request.Workspace {
		return errors.New("implementation workspace must match request workspace")
	}
	if _, err := time.Parse(time.RFC3339, impl.DeadlineRFC3339); err != nil {
		return fmt.Errorf("implementation deadline: %w", err)
	}
	if impl.ReservedUSD < 0 {
		return errors.New("reserved_usd must be non-negative")
	}
	if _, err := composeEffectiveMaxSeconds(impl.MaxSeconds, b.Request.Budget.MaxSeconds); err != nil {
		return err
	}
	if err := composeValidateExactScope(impl.ReadPaths, impl.WritePaths, impl.ProtectedPaths); err != nil {
		return err
	}
	return nil
}

// composeRequireFutureDeadlines is an execution-only gate applied after durable
// idempotent replay lookup so observational replay still works after expiry.
func composeRequireFutureDeadlines(requestDeadline, implDeadline string) error {
	now := time.Now().UTC()
	reqDL, err := time.Parse(time.RFC3339, requestDeadline)
	if err != nil {
		return fmt.Errorf("request deadline: %w", err)
	}
	if !reqDL.After(now) {
		return errors.New("request deadline must be in the future for new dispatch")
	}
	implDL, err := time.Parse(time.RFC3339, implDeadline)
	if err != nil {
		return fmt.Errorf("implementation deadline: %w", err)
	}
	if !implDL.After(now) {
		return errors.New("implementation deadline must be in the future")
	}
	return nil
}

func composeCtxInterrupted(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func composeClassifyCtxFailure(ctx context.Context, err error) (AttemptState, bool) {
	if composeCtxInterrupted(ctx, err) {
		return AttemptBlockedUncertain, true
	}
	return AttemptBlockedUncertain, true
}

func composeEffectiveMaxSeconds(implMax, parentMax int) (int, error) {
	if parentMax < 1 {
		return 0, errors.New("parent budget.max_seconds must be >= 1")
	}
	if implMax < 0 {
		return 0, errors.New("implementation max_seconds must be non-negative")
	}
	if implMax == 0 {
		return parentMax, nil
	}
	if implMax > parentMax {
		return 0, fmt.Errorf("implementation max_seconds %d exceeds parent budget.max_seconds %d", implMax, parentMax)
	}
	return implMax, nil
}

func composeAttemptContext(parent context.Context, requestDeadline, implDeadline string, maxSeconds int) (context.Context, context.CancelFunc, error) {
	if maxSeconds < 1 {
		return nil, nil, errors.New("attempt max_seconds must be >= 1")
	}
	reqDL, err := time.Parse(time.RFC3339, requestDeadline)
	if err != nil {
		return nil, nil, fmt.Errorf("request deadline: %w", err)
	}
	implDL, err := time.Parse(time.RFC3339, implDeadline)
	if err != nil {
		return nil, nil, fmt.Errorf("implementation deadline: %w", err)
	}
	deadline := time.Now().UTC().Add(time.Duration(maxSeconds) * time.Second)
	if reqDL.Before(deadline) {
		deadline = reqDL
	}
	if implDL.Before(deadline) {
		deadline = implDL
	}
	if d, ok := parent.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !deadline.After(time.Now().UTC()) {
		return nil, nil, errors.New("attempt deadline already passed")
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	return ctx, cancel, nil
}

func composeValidateExactScope(readPaths, writePaths, protected []string) error {
	all := append(append(append([]string{}, readPaths...), writePaths...), protected...)
	for _, p := range all {
		if err := validRelativePath(p); err != nil {
			return err
		}
	}
	for _, w := range writePaths {
		for _, p := range protected {
			if w == p || composePathNested(w, p) || composePathNested(p, w) {
				return fmt.Errorf("write path %q overlaps protected path %q", w, p)
			}
		}
	}
	return nil
}

func composePathNested(a, b string) bool {
	ap := filepath.Clean(a)
	bp := filepath.Clean(b)
	if ap == bp {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(ap, bp+sep) || strings.HasPrefix(bp, ap+sep)
}

func composeSameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := map[string]int{}
	for _, x := range a {
		ma[x]++
	}
	for _, x := range b {
		if ma[x] == 0 {
			return false
		}
		ma[x]--
	}
	return true
}

func composeEnsureOutsideProductWrites(workspace string, writePaths []string, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	for _, w := range writePaths {
		wp := filepath.Join(absWS, w)
		if absPath == wp || strings.HasPrefix(absPath, wp+string(filepath.Separator)) {
			return fmt.Errorf("%q collides with product write path %q", path, w)
		}
	}
	return nil
}

func composeCaptureGit(ctx context.Context, workspace string) (composeGitSnapshot, error) {
	var snap composeGitSnapshot
	head, err := gitOutput(ctx, workspace, "rev-parse", "HEAD")
	if err != nil {
		return snap, err
	}
	snap.HEAD = strings.TrimSpace(string(head))
	tree, err := gitOutput(ctx, workspace, "write-tree")
	if err != nil {
		return snap, err
	}
	snap.IndexTree = strings.TrimSpace(string(tree))
	status, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return snap, err
	}
	snap.Porcelain = string(status)
	snap.StatusSHA256 = digest(status)
	return snap, nil
}

func composeRequireCleanGit(snap composeGitSnapshot) error {
	for _, line := range strings.Split(snap.Porcelain, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		path := composePorcelainPath(line)
		if composeIgnoredLeasePath(path) {
			continue
		}
		return errors.New("workspace Git tree must be clean before dispatch")
	}
	return nil
}

func composeCheckIntegrity(ctx context.Context, impl ImplementationRequest, baseline composeGitSnapshot, sourceHashes, protectedHashes map[string]string, readOnly, scopeAllowed []string, when string) error {
	postGit, err := composeCaptureGit(ctx, impl.Workspace)
	if err != nil {
		return err
	}
	if postGit.HEAD != baseline.HEAD {
		return fmt.Errorf("Git HEAD changed %s", when)
	}
	if postGit.IndexTree != baseline.IndexTree {
		return fmt.Errorf("git index changed %s", when)
	}
	postSource, err := composeSnapshotRelFiles(impl.Workspace, readOnly, scopeAllowed)
	if err != nil {
		return err
	}
	if changed(sourceHashes, postSource) {
		return fmt.Errorf("read-only source paths changed %s", when)
	}
	postProtected, err := composeSnapshotRelFiles(impl.Workspace, impl.ProtectedPaths, scopeAllowed)
	if err != nil {
		return err
	}
	if changed(protectedHashes, postProtected) {
		if when == "before verifier" {
			return fmt.Errorf("protected inputs changed %s; verifier not executed", when)
		}
		return fmt.Errorf("protected inputs changed %s", when)
	}
	if err := composeDetectForbiddenWrites(ctx, impl.Workspace, impl.WritePaths); err != nil {
		return fmt.Errorf("%s: %w", when, err)
	}
	return nil
}

func composeCurrentHashes(impl ImplementationRequest, readOnly, scopeAllowed []string) (source, protected map[string]string, err error) {
	source, err = composeSnapshotRelFiles(impl.Workspace, readOnly, scopeAllowed)
	if err != nil {
		return nil, nil, err
	}
	protected, err = composeSnapshotRelFiles(impl.Workspace, impl.ProtectedPaths, scopeAllowed)
	return source, protected, err
}

func composeDetectForbiddenWrites(ctx context.Context, workspace string, writePaths []string) error {
	status, err := gitOutput(ctx, workspace, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, w := range writePaths {
		allowed[filepath.Clean(w)] = true
	}
	for _, line := range strings.Split(string(status), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		path := composePorcelainPath(line)
		if path == "." || path == "" || composeIgnoredLeasePath(path) {
			continue
		}
		if !allowed[path] {
			return fmt.Errorf("forbidden path change outside write scope: %s", path)
		}
	}
	return nil
}

func composePorcelainPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	rest := line[3:]
	if i := strings.Index(rest, " -> "); i >= 0 {
		rest = rest[i+4:]
	}
	return filepath.Clean(strings.Trim(rest, `"`))
}

// composeIgnoredLeasePath excludes claimWorkspace's .fanisi-lock from product
// scope porcelain checks while the execution lease is held.
func composeIgnoredLeasePath(path string) bool {
	clean := filepath.Clean(path)
	return clean == ".fanisi-lock" || strings.HasPrefix(clean, ".fanisi-lock"+string(filepath.Separator))
}

func composeAttemptCanceled(ctx context.Context, attemptDir, fence string, exitCode int) bool {
	if composeCancelRequested(attemptDir, fence) {
		return true
	}
	if exitCode == 124 {
		return true
	}
	if ctx.Err() != nil && composeCancelRequested(attemptDir, fence) {
		return true
	}
	if ctx.Err() != nil {
		// Deadline/timeout/cancel on the shared attempt context.
		return true
	}
	return false
}

func composeWatchCancel(ctx context.Context, attemptDir, fence string, cancel context.CancelFunc, done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if composeCancelRequested(attemptDir, fence) {
				cancel()
				return
			}
		}
	}
}

func composeCancelRequested(attemptDir, fence string) bool {
	var rec struct {
		FenceToken string `json:"fence_token"`
	}
	if err := readJSON(filepath.Join(attemptDir, "cancel-request.json"), &rec); err != nil {
		return false
	}
	return rec.FenceToken == fence
}

func composeSafeFileToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return digest([]byte(s))
	}
	if len(out) > 120 {
		return out[:120]
	}
	return out
}
