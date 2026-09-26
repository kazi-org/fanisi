package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// composeCLIFixture builds a clean throwaway Git product workspace and valid
// offline composition JSON (request/catalog/evidence/decisions/draft/impl) with
// a future deadline and consistent hashes after path resolution.
func composeCLIFixture(t *testing.T) (root string, head string, pin string) {
	t.Helper()
	root = t.TempDir()
	product := filepath.Join(root, "product")
	artifacts := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(product, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifacts, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"candidate.txt":       "old",
		"readonly.txt":        "frozen-source",
		"protected/accept.md": "acceptance-input",
		"verify.sh":           "#!/bin/sh\ntest \"$(cat candidate.txt)\" = new\n",
	}
	head = composeTestInitGit(t, product, files)
	okDelegate := filepath.Join(artifacts, "ok.sh")
	composeTestWriteScript(t, okDelegate, "#!/bin/sh\nprintf 'new' > candidate.txt\n")

	catalogSrc := "testdata/compose/catalog/index.json"
	catalogDst := filepath.Join(artifacts, "catalog.json")
	raw, err := os.ReadFile(catalogSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogDst, raw, 0600); err != nil {
		t.Fatal(err)
	}
	idx, err := loadCatalog(catalogDst)
	if err != nil {
		t.Fatal(err)
	}
	pin = idx.Pin

	evidence := []EvidenceRef{{
		ID:                "ev-b-thread",
		Kind:              EvidenceB,
		ProductIDs:        []string{"product.voice", "product.notes"},
		ImplementationIDs: []string{"impl.thread.v1"},
		ProductKinds:      map[string]string{"product.voice": "real", "product.notes": "real"},
		SourceRefs:        []string{"src/voice", "src/notes"},
		NotesRef:          "TEST_SCENARIO: synthetic fixture evidence for CLI tests only",
	}}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "evidence.json"), evidence)

	wsCanon, err := composeCanonicalRoot(product)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)
	req := CompositionRequest{
		SchemaVersion:   compositionSchemaVersion,
		RequestID:       "req-cli-1",
		ParentID:        "parent-cli-1",
		ProductRevision: head,
		CatalogPin:      pin,
		Requirements:    []string{"Edit candidate.txt to new under verification"},
		Behaviors:       []BehaviorNeed{{BehaviorID: "edit-candidate", Required: true, Contract: "contracts/edit.md"}},
		Workspace:       wsCanon,
		ReadPaths:       []string{"readonly.txt", "candidate.txt"},
		WritePaths:      []string{"candidate.txt"},
		ProtectedPaths:  []string{"protected/accept.md", "verify.sh"},
		AuthorityRef:    "operator:cli-test",
		DeadlineRFC3339: deadline,
		Budget: CompositionBudget{
			MaxAttempts: 3, MaxSeconds: 60, MaxEstimatedUSD: 1.0, ReservedUSD: 0.1,
			CurrencyNote: "admission estimate; provider receipts separate",
		},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "request.json"), req)

	dec := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-cli-1",
		RequestHash:   reqHash,
		BehaviorID:    "edit-candidate",
		Verdict:       VerdictProductLocal,
		Owner:         "operator",
		Provenance:    DecisionProvenance{Source: "operator", Author: "cli-test"},
	}
	decHash, err := hashComponentDecision(dec)
	if err != nil {
		t.Fatal(err)
	}
	dec.ContentSHA256 = decHash
	if err := os.MkdirAll(filepath.Join(artifacts, "decisions"), 0755); err != nil {
		t.Fatal(err)
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "decisions", "dec-cli-1.json"), dec)

	draft := composeManifestDraft{
		SchemaVersion: compositionSchemaVersion,
		ManifestID:    "man-cli-1",
		Bindings: []Binding{{
			BehaviorID:   "edit-candidate",
			DecisionHash: decHash,
			Kind:         "local_planned",
			TargetRef:    "candidate.txt",
		}},
		Edges: nil,
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "draft.json"), draft)

	outDir := filepath.Join(root, "output")
	impl := ImplementationRequest{
		SchemaVersion:   compositionSchemaVersion,
		AttemptID:       "att-cli-1",
		ParentID:        req.ParentID,
		ManifestHash:    "", // filled after manifest create
		RequestHash:     reqHash,
		FenceToken:      "fence-cli-1",
		Owner:           "operator",
		ReadPaths:       append([]string{}, req.ReadPaths...),
		WritePaths:      append([]string{}, req.WritePaths...),
		ProtectedPaths:  append([]string{}, req.ProtectedPaths...),
		VerifyCommand:   []string{filepath.Join(wsCanon, "verify.sh")},
		DelegateArgv:    []string{okDelegate},
		Workspace:       wsCanon,
		OutputDir:       outDir,
		ReservedUSD:     0.1,
		MaxSeconds:      30,
		DeadlineRFC3339: deadline,
		IdempotencyKey:  req.ParentID + "|" + reqHash + "|intent-edit",
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "impl.json"), impl)

	choiceReq := FiniteChoiceRequest{
		SchemaVersion: compositionSchemaVersion,
		QuestionID:    "q-edit",
		Question:      "Which local edit strategy?",
		State:         "candidate.txt is old; verifier requires new",
		Candidates:    map[string]string{"edit": "write new", "abstain": "skip"},
		AbstainID:     "abstain",
		EvidenceIDs:   []string{"ev1"},
		Evidence:      map[string]string{"ev1": "verifier asserts candidate.txt == new"},
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "finite-request.json"), choiceReq)
	conf := 0.9
	choiceRes := FiniteChoiceResult{
		SchemaVersion: compositionSchemaVersion,
		SelectedID:    "edit",
		Abstain:       false,
		ReasonIDs:     []string{"ev1"},
		Confidence:    &conf,
		Backend:       BackendImport,
	}
	composeCLIWriteJSON(t, filepath.Join(artifacts, "finite-import.json"), choiceRes)

	return root, head, pin
}

func composeCLIWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func composeCLICapture(t *testing.T, ctx context.Context, args ...string) (stdout string, err error) {
	t.Helper()
	old := os.Stdout
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stdout = w
	err = mainContext(ctx, args)
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	return buf.String(), err
}

func TestComposeCLIEndToEndOffline(t *testing.T) {
	root, _, _ := composeCLIFixture(t)
	artifacts := filepath.Join(root, "artifacts")
	journal := filepath.Join(root, "journal")
	ctx := context.Background()

	out, err := composeCLICapture(t, ctx, "compose", "catalog", "--index", filepath.Join(artifacts, "catalog.json"), "--json")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !strings.Contains(out, `"count"`) || !strings.Contains(out, "compose.thread-store") {
		t.Fatalf("catalog output missing AMSL records: %s", out)
	}

	out, err = composeCLICapture(t, ctx, "compose", "decide",
		"--request", filepath.Join(artifacts, "finite-request.json"),
		"--import", filepath.Join(artifacts, "finite-import.json"),
		"--out", filepath.Join(artifacts, "finite-result.json"),
	)
	if err != nil {
		t.Fatalf("decide import: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifacts, "finite-result.json")); err != nil {
		t.Fatal(err)
	}

	err = mainContext(ctx, []string{"compose", "manifest",
		"--request", filepath.Join(artifacts, "request.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--draft", filepath.Join(artifacts, "draft.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--out", filepath.Join(artifacts, "manifest.json"),
	})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var man ApplicationManifest
	if err := readStrictJSON(filepath.Join(artifacts, "manifest.json"), &man); err != nil {
		t.Fatal(err)
	}
	if man.ContentSHA256 == "" || len(man.DecisionHashes) != 1 {
		t.Fatalf("manifest incomplete: %+v", man)
	}

	// Fill impl.manifest_hash now that manifest exists.
	var impl ImplementationRequest
	if err := readStrictJSON(filepath.Join(artifacts, "impl.json"), &impl); err != nil {
		t.Fatal(err)
	}
	impl.ManifestHash = man.ContentSHA256
	// Content hash of manifest omits content_sha256 field for hashing... wait,
	// hashApplicationManifest omits content_sha256, so ManifestHash should be
	// the hash WITHOUT the filled content_sha256 — which equals man.ContentSHA256
	// since that field was set FROM the hash. Good.
	manHash, err := hashApplicationManifest(man)
	if err != nil {
		t.Fatal(err)
	}
	if manHash != man.ContentSHA256 {
		t.Fatalf("manifest content hash unstable: stored=%s got=%s", man.ContentSHA256, manHash)
	}
	impl.ManifestHash = manHash
	composeCLIWriteJSON(t, filepath.Join(artifacts, "impl.json"), impl)

	err = mainContext(ctx, []string{"compose", "validate",
		"--request", filepath.Join(artifacts, "request.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	out, err = composeCLICapture(t, ctx, "compose", "hash",
		"--file", filepath.Join(artifacts, "request.json"),
		"--kind", "request",
	)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if got := strings.TrimSpace(out); len(got) != 64 {
		t.Fatalf("hash digest len=%d %q", len(got), got)
	}

	out, err = composeCLICapture(t, ctx, "compose", "dispatch",
		"--request", filepath.Join(artifacts, "request.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--impl", filepath.Join(artifacts, "impl.json"),
		"--journal", journal,
	)
	if err != nil {
		t.Fatalf("dispatch: %v\n%s", err, out)
	}
	var result AttemptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, out)
	}
	if result.State != AttemptVerifiedPendingReview {
		t.Fatalf("want verified_pending_review, got %s err=%q", result.State, result.Error)
	}
	if result.Usage.KnownCostUSD != nil || result.Usage.UsageComplete {
		t.Fatalf("usage must stay unknown: %+v", result.Usage)
	}

	out, err = composeCLICapture(t, ctx, "compose", "status", "--journal", journal, "--attempt", result.AttemptID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var statuses []AttemptResult
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].State != AttemptVerifiedPendingReview {
		t.Fatalf("status=%+v", statuses)
	}

	notes := filepath.Join(artifacts, "notes.txt")
	if err := os.WriteFile(notes, []byte("agent claims ship-ready"), 0600); err != nil {
		t.Fatal(err)
	}
	err = mainContext(ctx, []string{"compose", "review",
		"--journal", journal,
		"--attempt", result.AttemptID,
		"--decision", "accept",
		"--reviewer", "agent-bot",
		"--kind", "agent",
		"--notes-file", notes,
		"--result-hash", result.ContentSHA256,
	})
	if err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("want agent accept rejection, got %v", err)
	}

	if err := os.WriteFile(notes, []byte("local operator accepts verified candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	err = mainContext(ctx, []string{"compose", "review",
		"--journal", journal,
		"--attempt", result.AttemptID,
		"--decision", "accept",
		"--reviewer", "david",
		"--kind", "human",
		"--notes-file", notes,
		"--result-hash", result.ContentSHA256,
	})
	if err != nil {
		t.Fatalf("human accept: %v", err)
	}
	out, err = composeCLICapture(t, ctx, "compose", "status", "--journal", journal, "--attempt", result.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != AttemptAccepted {
		t.Fatalf("want accepted, got %s", statuses[0].State)
	}

	// Duplicate dispatch must not re-execute.
	before, _ := os.ReadFile(filepath.Join(root, "product", "candidate.txt"))
	out, err = composeCLICapture(t, ctx, "compose", "dispatch",
		"--request", filepath.Join(artifacts, "request.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--impl", filepath.Join(artifacts, "impl.json"),
		"--journal", journal,
	)
	if err != nil {
		t.Fatalf("duplicate dispatch: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "product", "candidate.txt"))
	if string(before) != string(after) {
		t.Fatal("duplicate dispatch mutated workspace")
	}
	var replay AttemptResult
	if err := json.Unmarshal([]byte(out), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.AttemptID != result.AttemptID {
		t.Fatalf("replay attempt_id=%s want %s", replay.AttemptID, result.AttemptID)
	}
}

func TestComposeCLIStaleHashReviewRejected(t *testing.T) {
	root, _, _ := composeCLIFixture(t)
	artifacts := filepath.Join(root, "artifacts")
	journal := filepath.Join(root, "journal")
	ctx := context.Background()
	composeCLIPrepareManifestAndImpl(t, artifacts)

	out, err := composeCLICapture(t, ctx, "compose", "dispatch",
		"--request", filepath.Join(artifacts, "request.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--impl", filepath.Join(artifacts, "impl.json"),
		"--journal", journal,
	)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var result AttemptResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(artifacts, "notes.txt")
	if err := os.WriteFile(notes, []byte("stale hash attempt"), 0600); err != nil {
		t.Fatal(err)
	}
	err = mainContext(ctx, []string{"compose", "review",
		"--journal", journal,
		"--attempt", result.AttemptID,
		"--decision", "accept",
		"--reviewer", "david",
		"--kind", "human",
		"--notes-file", notes,
		"--result-hash", strings.Repeat("0", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "result_sha256") {
		t.Fatalf("want stale hash rejection, got %v", err)
	}
}

func TestComposeCLIPoisonedVerifierNoExecution(t *testing.T) {
	root, _, _ := composeCLIFixture(t)
	artifacts := filepath.Join(root, "artifacts")
	journal := filepath.Join(root, "journal")
	ctx := context.Background()
	composeCLIPrepareManifestAndImpl(t, artifacts)

	marker := filepath.Join(root, "verifier-ran")
	var impl ImplementationRequest
	if err := readStrictJSON(filepath.Join(artifacts, "impl.json"), &impl); err != nil {
		t.Fatal(err)
	}
	poison := filepath.Join(artifacts, "poison.sh")
	composeTestWriteScript(t, poison, "#!/bin/sh\nprintf 'new' > candidate.txt\nprintf 'hacked' > verify.sh\n")
	wrapped := filepath.Join(artifacts, "wrap-verify.sh")
	composeTestWriteScript(t, wrapped, "#!/bin/sh\ntouch '"+marker+"'\nexit 0\n")
	impl.DelegateArgv = []string{poison}
	impl.VerifyCommand = []string{wrapped}
	impl.AttemptID = "att-poison"
	impl.FenceToken = "fence-poison"
	impl.IdempotencyKey = impl.IdempotencyKey + "|poison"
	composeCLIWriteJSON(t, filepath.Join(artifacts, "impl-poison.json"), impl)

	out, err := composeCLICapture(t, ctx, "compose", "dispatch",
		"--request", filepath.Join(artifacts, "request.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--impl", filepath.Join(artifacts, "impl-poison.json"),
		"--journal", journal,
	)
	// dispatch returns error for non-verified states
	if err == nil {
		t.Fatal("expected poisoned verifier dispatch error")
	}
	var result AttemptResult
	if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
		t.Fatalf("decode: %v out=%s", jerr, out)
	}
	if result.State != AttemptFailedTerminal {
		t.Fatalf("want failed_terminal, got %s (%q)", result.State, result.Error)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("verifier must not execute after protected tamper")
	}
}

func TestComposeCLICancelUncertainty(t *testing.T) {
	root, _, _ := composeCLIFixture(t)
	artifacts := filepath.Join(root, "artifacts")
	journal := filepath.Join(root, "journal")
	ctx := context.Background()
	composeCLIPrepareManifestAndImpl(t, artifacts)

	var impl ImplementationRequest
	if err := readStrictJSON(filepath.Join(artifacts, "impl.json"), &impl); err != nil {
		t.Fatal(err)
	}
	hang := filepath.Join(artifacts, "hang.sh")
	composeTestWriteScript(t, hang, "#!/bin/sh\nsleep 60\n")
	impl.DelegateArgv = []string{hang}
	impl.AttemptID = "att-cancel"
	impl.FenceToken = "fence-cancel"
	impl.IdempotencyKey = impl.IdempotencyKey + "|cancel"
	impl.MaxSeconds = 30
	composeCLIWriteJSON(t, filepath.Join(artifacts, "impl-cancel.json"), impl)

	errCh := make(chan error, 1)
	var out string
	go func() {
		var err error
		out, err = composeCLICapture(t, ctx, "compose", "dispatch",
			"--request", filepath.Join(artifacts, "request.json"),
			"--catalog", filepath.Join(artifacts, "catalog.json"),
			"--evidence", filepath.Join(artifacts, "evidence.json"),
			"--decisions", filepath.Join(artifacts, "decisions"),
			"--manifest", filepath.Join(artifacts, "manifest.json"),
			"--impl", filepath.Join(artifacts, "impl-cancel.json"),
			"--journal", journal,
		)
		errCh <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for request.json")
		}
		if _, err := os.Stat(filepath.Join(journal, "attempts", "att-cancel", "request.json")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := mainContext(ctx, []string{"compose", "cancel",
		"--journal", journal,
		"--attempt", "att-cancel",
		"--fence", "fence-cancel",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case err := <-errCh:
		var result AttemptResult
		if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
			t.Fatalf("decode: %v out=%s err=%v", jerr, out, err)
		}
		if result.State != AttemptBlockedUncertain {
			t.Fatalf("want blocked_uncertain, got %s (%q) err=%v", result.State, result.Error, err)
		}
		if !result.UnresolvedEffects {
			t.Fatal("cancel must set unresolved_effects")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dispatch did not finish after cancel")
	}
}

func TestComposeCLIFlagValidation(t *testing.T) {
	err := mainContext(context.Background(), []string{"compose", "validate", "--request", "x"})
	if err == nil {
		t.Fatal("expected missing flags error")
	}
	err = mainContext(context.Background(), []string{"compose", "decide", "--request", "x", "--import", "a", "--command", "echo"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want exclusive backend error, got %v", err)
	}
	err = mainContext(context.Background(), []string{"compose", "catalog", "--index", "testdata/compose/catalog/index.json", "extra"})
	if err == nil {
		t.Fatal("expected positional refusal")
	}
}

func TestComposeCLIValidateRejectsMissingWorkspace(t *testing.T) {
	root, _, _ := composeCLIFixture(t)
	artifacts := filepath.Join(root, "artifacts")
	composeCLIPrepareManifestAndImpl(t, artifacts)

	var req CompositionRequest
	if err := composeReadStrictJSONFile(filepath.Join(artifacts, "request.json"), &req, composeExecutableJSONMax); err != nil {
		t.Fatal(err)
	}
	req.Workspace = ""
	composeCLIWriteJSON(t, filepath.Join(artifacts, "request-blank-ws.json"), req)

	err := mainContext(context.Background(), []string{"compose", "validate",
		"--request", filepath.Join(artifacts, "request-blank-ws.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--manifest", filepath.Join(artifacts, "manifest.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("want workspace rejection from compose validate, got %v", err)
	}
}

func TestComposeJournalAcceptsPlatformTempRoot(t *testing.T) {
	// macOS t.TempDir often lives under /var -> /private/var; canonicalization
	// must allow this while still rejecting workspace-relative symlink escapes.
	root := t.TempDir()
	j, err := openJournal(filepath.Join(root, "journal"))
	if err != nil {
		t.Fatalf("openJournal on platform temp: %v", err)
	}
	if j.Dir == "" {
		t.Fatal("empty journal dir")
	}
	evil := filepath.Join(root, "evil-link")
	if err := os.Symlink("/etc", evil); err != nil {
		t.Fatal(err)
	}
	if err := composeRejectSymlinkPath(evil); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func composeCLIPrepareManifestAndImpl(t *testing.T, artifacts string) {
	t.Helper()
	ctx := context.Background()
	if err := mainContext(ctx, []string{"compose", "manifest",
		"--request", filepath.Join(artifacts, "request.json"),
		"--decisions", filepath.Join(artifacts, "decisions"),
		"--draft", filepath.Join(artifacts, "draft.json"),
		"--catalog", filepath.Join(artifacts, "catalog.json"),
		"--evidence", filepath.Join(artifacts, "evidence.json"),
		"--out", filepath.Join(artifacts, "manifest.json"),
	}); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var man ApplicationManifest
	if err := readStrictJSON(filepath.Join(artifacts, "manifest.json"), &man); err != nil {
		t.Fatal(err)
	}
	manHash, err := hashApplicationManifest(man)
	if err != nil {
		t.Fatal(err)
	}
	var impl ImplementationRequest
	if err := readStrictJSON(filepath.Join(artifacts, "impl.json"), &impl); err != nil {
		t.Fatal(err)
	}
	impl.ManifestHash = manHash
	composeCLIWriteJSON(t, filepath.Join(artifacts, "impl.json"), impl)
}

func TestComposeCLILoadPreservesArgvLiterals(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "product")
	out := filepath.Join(root, "output")
	art := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	composeTestWriteScript(t, filepath.Join(ws, "verify.sh"), "#!/bin/sh\ntest \"$(cat candidate.txt)\" = new\n")
	if err := os.WriteFile(filepath.Join(ws, "candidate.txt"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	wsCanon, err := composeCanonicalRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	implPath := filepath.Join(art, "impl.json")
	literal := "test -f /tmp/foo && printf new > candidate.txt"
	flagArg := "--output=dir/sub/file"
	urlArg := "https://example.com/path/to/resource"
	impl := ImplementationRequest{
		SchemaVersion:   compositionSchemaVersion,
		AttemptID:       "att-argv",
		ParentID:        "parent",
		ManifestHash:    strings.Repeat("a", 64),
		RequestHash:     strings.Repeat("b", 64),
		FenceToken:      "fence",
		Owner:           "operator",
		WritePaths:      []string{"candidate.txt"},
		ProtectedPaths:  []string{"verify.sh"},
		VerifyCommand:   []string{"./verify.sh"}, // explicit workspace-relative; not bare PATH
		DelegateArgv:    []string{"/bin/sh", "-c", literal, flagArg, urlArg},
		Workspace:       wsCanon,
		OutputDir:       out,
		ReservedUSD:     0.1,
		MaxSeconds:      10,
		DeadlineRFC3339: composeTestFutureDeadline(),
		IdempotencyKey:  "parent|hash|argv",
	}
	composeCLIWriteJSON(t, implPath, impl)

	loaded, err := composeLoadImplementation(implPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.DelegateArgv) != 5 {
		t.Fatalf("argv rewritten: %#v", loaded.DelegateArgv)
	}
	if loaded.DelegateArgv[2] != literal || loaded.DelegateArgv[3] != flagArg || loaded.DelegateArgv[4] != urlArg {
		t.Fatalf("slash-bearing literals corrupted: %#v", loaded.DelegateArgv)
	}
	if loaded.VerifyCommand[0] != "./verify.sh" {
		t.Fatalf("relative verifier path rewritten: %#v", loaded.VerifyCommand)
	}

	// Execution resolves ./verify.sh against workspace and keeps argv literals.
	code, _, err := runDelegate(context.Background(), DelegateConfig{
		Argv:       []string{"/bin/sh", "-c", "printf new > candidate.txt"},
		Workspace:  wsCanon,
		OutputDir:  out,
		MaxSeconds: 5,
	})
	if err != nil || code != 0 {
		t.Fatalf("delegate: code=%d err=%v", code, err)
	}
	resolved, err := composeResolveAbsoluteArgv([]string{"./verify.sh"}, wsCanon)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resolved[0], string(filepath.Separator)+"verify.sh") && !strings.HasSuffix(resolved[0], "/verify.sh") {
		t.Fatalf("expected workspace-relative ./verify.sh resolution, got %#v", resolved)
	}
	if len(resolved) != 1 {
		t.Fatalf("argv[1+] must not be invented: %#v", resolved)
	}
}

func TestComposeTutorialRejectsNonemptyTarget(t *testing.T) {
	occupied := t.TempDir()
	keep := filepath.Join(occupied, "keep.txt")
	if err := os.WriteFile(keep, []byte("preserve-me"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "examples/compose/run-tutorial.sh", occupied)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected nonempty rejection, output=%s", out)
	}
	if !strings.Contains(string(out), "empty") && !strings.Contains(string(out), "absent") {
		t.Fatalf("want empty/absent message, got %s", out)
	}
	body, err := os.ReadFile(keep)
	if err != nil || string(body) != "preserve-me" {
		t.Fatalf("existing files must be preserved: %q err=%v", body, err)
	}

	fresh := filepath.Join(t.TempDir(), "fresh-target")
	cmd = exec.Command("sh", "examples/compose/run-tutorial.sh", fresh)
	cmd.Dir = "."
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh tutorial failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "verified_pending_review") {
		t.Fatalf("tutorial missing verified_pending_review: %s", out)
	}
}
