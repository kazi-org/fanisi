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

func TestComposeJournalAppendExclusiveAndSeq(t *testing.T) {
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := j.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := j.appendEvent(JournalEvent{
		AttemptID:  "att1",
		FenceToken: "fence-a",
		FromState:  AttemptValidated,
		ToState:    AttemptDispatched,
		Actor:      "operator",
	}); err != nil {
		t.Fatal(err)
	}
	if err := j.appendEvent(JournalEvent{
		AttemptID:  "att1",
		FenceToken: "fence-a",
		FromState:  AttemptDispatched,
		ToState:    AttemptRunning,
		Actor:      "fanisi-delegate",
	}); err != nil {
		t.Fatal(err)
	}
	files, err := composeListEventFiles(j.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 events, got %d", len(files))
	}
	// Exclusive create: rewriting seq 1 must fail.
	if err := composeWriteNewFsync(files[0], []byte("{}\n")); err == nil {
		t.Fatal("expected exclusive create failure on existing event")
	}
}

func TestComposeJournalLockedBlocksSecondLock(t *testing.T) {
	dir := t.TempDir()
	j, err := openJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := j.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	j2, err := openJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j2.lock(); err == nil {
		t.Fatal("expected second journal lock to fail")
	}
}

func TestComposeJournalParentBudgetFreezeAndExhaustion(t *testing.T) {
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := j.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	budget := CompositionBudget{
		MaxAttempts: 2, MaxSeconds: 30, MaxEstimatedUSD: 1.0, ReservedUSD: 0.4,
		CurrencyNote: "admission estimate; provider receipts separate",
	}
	if err := j.reserveBudget("parent1", budget, 0.4); err != nil {
		t.Fatal(err)
	}
	if err := composePersistReservation(j.Dir, "parent1", "a1", 0.4); err != nil {
		t.Fatal(err)
	}
	changed := budget
	changed.MaxEstimatedUSD = 9
	if err := j.reserveBudget("parent1", changed, 0.1); err == nil {
		t.Fatal("expected frozen budget rejection")
	}
	if err := j.reserveBudget("parent1", budget, 0.4); err != nil {
		t.Fatal(err)
	}
	if err := composePersistReservation(j.Dir, "parent1", "a2", 0.4); err != nil {
		t.Fatal(err)
	}
	if err := j.reserveBudget("parent1", budget, 0.1); err == nil {
		t.Fatal("expected max_attempts exhaustion")
	}
	// Over USD with higher max attempts on a fresh parent.
	budget2 := CompositionBudget{
		MaxAttempts: 5, MaxSeconds: 30, MaxEstimatedUSD: 0.5, ReservedUSD: 0.4,
		CurrencyNote: "admission estimate; provider receipts separate",
	}
	if err := j.reserveBudget("parent2", budget2, 0.4); err != nil {
		t.Fatal(err)
	}
	if err := composePersistReservation(j.Dir, "parent2", "b1", 0.4); err != nil {
		t.Fatal(err)
	}
	if err := j.reserveBudget("parent2", budget2, 0.2); err == nil {
		t.Fatal("expected USD sum exhaustion")
	}
}

func TestComposeTransitionAllowed(t *testing.T) {
	if !transitionAllowed(AttemptRunning, AttemptVerifiedPendingReview) {
		t.Fatal("running -> verified_pending_review")
	}
	if transitionAllowed(AttemptFailedTerminal, AttemptRunning) {
		t.Fatal("failed_terminal must not restart")
	}
	if !transitionAllowed(AttemptVerifiedPendingReview, AttemptAccepted) {
		t.Fatal("pending -> accepted")
	}
	if transitionAllowed(AttemptAccepted, AttemptRejected) {
		t.Fatal("accepted is terminal")
	}
}

func composeTestInitGit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if strings.HasSuffix(name, ".sh") {
			mode = 0700
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fanisi",
			"GIT_AUTHOR_EMAIL=fanisi@example.com",
			"GIT_COMMITTER_NAME=fanisi",
			"GIT_COMMITTER_EMAIL=fanisi@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("add", "-A")
	run("commit", "-m", "base")
	out, err := gitOutput(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func composeTestWriteScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

func composeTestFutureDeadline() string {
	return time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
}
