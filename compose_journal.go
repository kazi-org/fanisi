package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	composeJournalLockRetryBudget = 2 * time.Second
	composeJournalLockRetrySleep  = 20 * time.Millisecond
)

const (
	composeMaxEventBytes      = 64 << 10
	composeMaxRecordBytes     = 256 << 10
	composeMaxLogBytes        = 1 << 20
	composeEventsDirName      = "events"
	composeAttemptsDirName    = "attempts"
	composeParentsDirName     = "parents"
	composeIdempotencyDirName = "idempotency"
)

// openJournal prepares a journal directory layout. It does not hold the lock.
func openJournal(dir string) (*Journal, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("journal directory is required")
	}
	abs, err := composeCanonicalRoot(dir)
	if err != nil {
		return nil, err
	}
	if err := composeRejectSymlinkPath(abs); err != nil {
		return nil, fmt.Errorf("journal path: %w", err)
	}
	for _, sub := range []string{composeEventsDirName, composeAttemptsDirName, composeParentsDirName, composeIdempotencyDirName} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0700); err != nil {
			return nil, err
		}
	}
	return &Journal{Dir: abs}, nil
}

// lock acquires a short-transaction journal lock via claimWorkspace.
// Callers must unlock before running long-lived subprocesses so status/cancel remain usable.
// Contention from another short transaction is retried within a bounded budget;
// stale locks are never removed.
func (j *Journal) lock() (unlock func(), err error) {
	if j == nil || j.Dir == "" {
		return nil, errors.New("journal is not open")
	}
	deadline := time.Now().Add(composeJournalLockRetryBudget)
	var last error
	for {
		unlock, err := claimWorkspace(j.Dir)
		if err == nil {
			return unlock, nil
		}
		last = err
		if time.Now().After(deadline) {
			return nil, last
		}
		time.Sleep(composeJournalLockRetrySleep)
	}
}

// composeCanonicalRoot resolves platform symlink prefixes (e.g. macOS /var ->
// /private/var) via EvalSymlinks on the path or its nearest existing ancestor,
// then rejoins any not-yet-created suffix. The leaf path itself may not be a
// symlink (malicious output_dir links are rejected before flattening). Callers
// then reject remaining symlink components on the canonical path.
func composeCanonicalRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink paths are not allowed")
		}
	} else if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		return "", err
	}
	cur := abs
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if cur == abs {
				return resolved, nil
			}
			rel, err := filepath.Rel(cur, abs)
			if err != nil {
				return "", err
			}
			return filepath.Join(resolved, rel), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
			parent := filepath.Dir(cur)
			if parent == cur {
				return "", fmt.Errorf("canonicalize %s: %w", abs, err)
			}
			cur = parent
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		cur = parent
	}
}

func (j *Journal) appendEvent(e JournalEvent) error {
	if j == nil || j.Dir == "" {
		return errors.New("journal is not open")
	}
	if e.SchemaVersion == 0 {
		e.SchemaVersion = compositionSchemaVersion
	}
	if e.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("journal event schema_version must be %d", compositionSchemaVersion)
	}
	if strings.TrimSpace(e.AttemptID) == "" || strings.TrimSpace(e.FenceToken) == "" || strings.TrimSpace(e.Actor) == "" {
		return errors.New("journal event requires attempt_id, fence_token, and actor")
	}
	if e.ToState == "" {
		return errors.New("journal event requires to_state")
	}
	if e.FromState != "" && !transitionAllowed(e.FromState, e.ToState) {
		return fmt.Errorf("transition %s -> %s is not allowed", e.FromState, e.ToState)
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	} else {
		e.At = e.At.UTC()
	}
	if e.Seq == 0 {
		next, err := composeNextEventSeq(j.Dir)
		if err != nil {
			return err
		}
		e.Seq = next
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if len(b) > composeMaxEventBytes {
		return fmt.Errorf("journal event exceeds %d bytes", composeMaxEventBytes)
	}
	path := filepath.Join(j.Dir, composeEventsDirName, fmt.Sprintf("%06d.json", e.Seq))
	return composeWriteNewFsync(path, b)
}

// reserveBudget freezes parent budget on first use, retains every reservation
// (including later failed/unknown attempts), and rejects over-max USD or attempt counts.
func (j *Journal) reserveBudget(parentID string, budget CompositionBudget, usd float64) error {
	if j == nil || j.Dir == "" {
		return errors.New("journal is not open")
	}
	if !simpleID(parentID) {
		return errors.New("parent_id must be a simple identifier")
	}
	if err := composeValidateBudget(budget); err != nil {
		return err
	}
	if usd < 0 || math.IsNaN(usd) || math.IsInf(usd, 0) {
		return errors.New("reservation usd must be finite and non-negative")
	}
	parentDir := filepath.Join(j.Dir, composeParentsDirName, parentID)
	if err := os.MkdirAll(filepath.Join(parentDir, "reservations"), 0700); err != nil {
		return err
	}
	budgetPath := filepath.Join(parentDir, "budget.json")
	if _, err := os.Lstat(budgetPath); errors.Is(err, os.ErrNotExist) {
		frozen := budget
		if err := composeWriteNewJSON(budgetPath, frozen); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		var frozen CompositionBudget
		if err := readStrictJSON(budgetPath, &frozen); err != nil {
			return err
		}
		if !composeBudgetEqual(frozen, budget) {
			return errors.New("parent budget is frozen; later callers cannot change it")
		}
	}
	var frozen CompositionBudget
	if err := readStrictJSON(budgetPath, &frozen); err != nil {
		return err
	}
	reservations, err := composeListReservations(parentDir)
	if err != nil {
		return err
	}
	if len(reservations) >= frozen.MaxAttempts {
		return errors.New("parent max_attempts exceeded")
	}
	sum := usd
	for _, r := range reservations {
		sum += r.USD
	}
	if sum > frozen.MaxEstimatedUSD {
		return fmt.Errorf("reservation sum %.6f exceeds parent max_estimated_usd %.6f", sum, frozen.MaxEstimatedUSD)
	}
	return nil
}

// composePersistReservation writes an immutable reservation record after reserveBudget succeeds.
// attemptID must be unique under the parent. Failed/unknown attempts retain their reservation.
func composePersistReservation(journalDir, parentID, attemptID string, usd float64) error {
	rec := composeReservationRecord{
		SchemaVersion: compositionSchemaVersion,
		ParentID:      parentID,
		AttemptID:     attemptID,
		USD:           usd,
		At:            time.Now().UTC(),
	}
	path := filepath.Join(journalDir, composeParentsDirName, parentID, "reservations", attemptID+".json")
	return composeWriteNewJSON(path, rec)
}

func (j *Journal) loadAttempt(id string) (ImplementationRequest, AttemptResult, error) {
	var req ImplementationRequest
	var result AttemptResult
	if j == nil || j.Dir == "" {
		return req, result, errors.New("journal is not open")
	}
	if !simpleID(id) {
		return req, result, errors.New("attempt_id must be a simple identifier")
	}
	dir := filepath.Join(j.Dir, composeAttemptsDirName, id)
	if err := readStrictJSON(filepath.Join(dir, "request.json"), &req); err != nil {
		return req, result, err
	}
	resultPath := filepath.Join(dir, "result.json")
	if _, err := os.Lstat(resultPath); errors.Is(err, os.ErrNotExist) {
		return req, result, nil
	} else if err != nil {
		return req, result, err
	}
	if err := readStrictJSON(resultPath, &result); err != nil {
		return req, result, err
	}
	return req, result, nil
}

func transitionAllowed(from, to AttemptState) bool {
	if from == to {
		return false
	}
	switch from {
	case AttemptProposed:
		return to == AttemptValidated || to == AttemptFailedTerminal || to == AttemptBlockedUncertain
	case AttemptValidated:
		return to == AttemptDispatched || to == AttemptFailedTerminal || to == AttemptBlockedUncertain
	case AttemptDispatched:
		return to == AttemptRunning || to == AttemptFailedTerminal || to == AttemptBlockedUncertain
	case AttemptRunning:
		return to == AttemptFailedTerminal || to == AttemptBlockedUncertain || to == AttemptVerifiedPendingReview
	case AttemptVerifiedPendingReview:
		return to == AttemptAccepted || to == AttemptRejected
	case AttemptBlockedUncertain:
		return to == AttemptRejected
	case AttemptFailedTerminal, AttemptAccepted, AttemptRejected:
		return false
	default:
		return false
	}
}

type composeReservationRecord struct {
	SchemaVersion int       `json:"schema_version"`
	ParentID      string    `json:"parent_id"`
	AttemptID     string    `json:"attempt_id"`
	USD           float64   `json:"usd"`
	At            time.Time `json:"at"`
}

type composeIdempotencyRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Key           string `json:"key"`
	AttemptID     string `json:"attempt_id"`
	PayloadSHA    string `json:"payload_sha256"`
}

func composeValidateBudget(b CompositionBudget) error {
	if b.MaxAttempts < 1 {
		return errors.New("budget.max_attempts must be >= 1")
	}
	if b.MaxSeconds < 1 {
		return errors.New("budget.max_seconds must be >= 1")
	}
	if b.MaxEstimatedUSD <= 0 || math.IsNaN(b.MaxEstimatedUSD) || math.IsInf(b.MaxEstimatedUSD, 0) {
		return errors.New("budget.max_estimated_usd must be finite and positive")
	}
	if b.ReservedUSD < 0 || math.IsNaN(b.ReservedUSD) || math.IsInf(b.ReservedUSD, 0) {
		return errors.New("budget.reserved_usd must be finite and non-negative")
	}
	if strings.TrimSpace(b.CurrencyNote) == "" {
		return errors.New("budget.currency_note is required")
	}
	return nil
}

func composeBudgetEqual(a, b CompositionBudget) bool {
	return a.MaxAttempts == b.MaxAttempts &&
		a.MaxSeconds == b.MaxSeconds &&
		a.MaxEstimatedUSD == b.MaxEstimatedUSD &&
		a.ReservedUSD == b.ReservedUSD &&
		a.CurrencyNote == b.CurrencyNote
}

func composeListReservations(parentDir string) ([]composeReservationRecord, error) {
	dir := filepath.Join(parentDir, "reservations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []composeReservationRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var rec composeReservationRecord
		if err := readStrictJSON(filepath.Join(dir, e.Name()), &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func composeNextEventSeq(journalDir string) (uint64, error) {
	dir := filepath.Join(journalDir, composeEventsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var max uint64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		n, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid journal event name %q", e.Name())
		}
		if n > max {
			max = n
		}
	}
	return max + 1, nil
}

func composeWriteNewFsync(path string, b []byte) error {
	if len(b) > composeMaxRecordBytes {
		return fmt.Errorf("record exceeds %d bytes", composeMaxRecordBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(path)
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	derr := dir.Sync()
	_ = dir.Close()
	return derr
}

func composeWriteNewJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return composeWriteNewFsync(path, append(b, '\n'))
}

func composeRejectSymlinkPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		return composeRejectSymlinkPath(parent)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink paths are not allowed")
	}
	parent := filepath.Dir(path)
	if parent == path {
		return nil
	}
	return composeRejectSymlinkPath(parent)
}

func composeAttemptDir(journalDir, attemptID string) string {
	return filepath.Join(journalDir, composeAttemptsDirName, attemptID)
}

func composeListEventFiles(journalDir string) ([]string, error) {
	dir := filepath.Join(journalDir, composeEventsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(names)
	return names, nil
}
