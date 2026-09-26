package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleValidRequest(t *testing.T, catalogPin string) CompositionRequest {
	t.Helper()
	req := CompositionRequest{
		SchemaVersion:   compositionSchemaVersion,
		RequestID:       "req-compose-001",
		ParentID:        "parent-local-001",
		ProductRevision: "product@deadbeef",
		CatalogPin:      catalogPin,
		Requirements:    []string{"Async voice note preview with durable thread ids"},
		Behaviors: []BehaviorNeed{
			{BehaviorID: "thread-store", Required: true, Contract: "contracts/thread-store.md"},
			{BehaviorID: "audio-bridge", Required: true, Contract: "contracts/audio-bridge.md"},
		},
		ReadPaths:       []string{"contracts/thread-store.md"},
		WritePaths:      []string{"app/Bridge.swift"},
		ProtectedPaths:  []string{"testdata/compose/protected/acceptance.md"},
		AuthorityRef:    "operator:local-c00",
		DeadlineRFC3339: "2026-10-03T00:00:00Z",
		Budget: CompositionBudget{
			MaxAttempts:     2,
			MaxSeconds:      1800,
			MaxEstimatedUSD: 0.5,
			ReservedUSD:     0.2,
			CurrencyNote:    "admission estimate; provider receipts separate",
		},
	}
	// Production validation requires a real workspace with protected/read files.
	return attachScopedWorkspace(t, req)
}

// attachScopedWorkspace materializes frozen reads and protected paths under a
// temp workspace so request/bundle validation exercises scopedPath + existence.
func attachScopedWorkspace(t *testing.T, req CompositionRequest) CompositionRequest {
	t.Helper()
	dir := t.TempDir()
	writeFixture := func(rel string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range frozenReadPaths(req.ReadPaths, req.WritePaths) {
		writeFixture(p)
	}
	for _, p := range req.ProtectedPaths {
		writeFixture(p)
	}
	for _, p := range req.WritePaths {
		parent := filepath.Join(dir, filepath.Dir(p))
		if err := os.MkdirAll(parent, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	req.Workspace = dir
	return req
}

func TestComposeValidateRequestHappyAndHash(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := attachScopedWorkspace(t, sampleValidRequest(t, idx.Pin))
	if err := validateCompositionRequest(req); err != nil {
		t.Fatal(err)
	}
	h, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentSHA256 = h
	if err := validateCompositionRequest(req); err != nil {
		t.Fatal(err)
	}
	req.ContentSHA256 = strings.Repeat("a", 64)
	if err := validateCompositionRequest(req); err == nil || !strings.Contains(err.Error(), "content_sha256 mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
	req.ContentSHA256 = ""
	if err := validateCompositionRequest(req); err != nil {
		t.Fatalf("empty content_sha256 must be permitted: %v", err)
	}
}

func TestComposeValidateRequestBudgetsAndIDs(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	base := sampleValidRequest(t, idx.Pin)

	badBudget := base
	badBudget.Budget.MaxAttempts = 0
	if err := validateCompositionRequest(badBudget); err == nil {
		t.Fatal("expected max_attempts rejection")
	}
	badBudget = base
	badBudget.Budget.MaxEstimatedUSD = math.Inf(1)
	if err := validateCompositionRequest(badBudget); err == nil || !strings.Contains(err.Error(), "finite") {
		t.Fatalf("expected non-finite rejection, got %v", err)
	}
	badID := base
	badID.RequestID = "bad id"
	if err := validateCompositionRequest(badID); err == nil {
		t.Fatal("expected request_id rejection")
	}
	blankWS := base
	blankWS.Workspace = ""
	if err := validateCompositionRequest(blankWS); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("expected blank workspace rejection, got %v", err)
	}
	missingWS := base
	missingWS.Workspace = filepath.Join(t.TempDir(), "does-not-exist")
	if err := validateCompositionRequest(missingWS); err == nil {
		t.Fatal("expected missing workspace directory rejection")
	}
}

func TestComposeValidateProtectedWriteOverlap(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.ProtectedPaths = []string{"app/Bridge.swift"}
	if err := validateCompositionRequest(req); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("expected overlap rejection, got %v", err)
	}
	req = sampleValidRequest(t, idx.Pin)
	req.WritePaths = []string{"app/out/file.swift"}
	req.ProtectedPaths = []string{"app/out"}
	if err := validateCompositionRequest(req); err == nil || !strings.Contains(err.Error(), "nests") {
		t.Fatalf("expected nest rejection, got %v", err)
	}
}

func TestComposeValidatePlannedWritesNeedNotExist(t *testing.T) {
	dir := t.TempDir()
	prot := filepath.Join(dir, "accept.md")
	if err := os.WriteFile(prot, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.Workspace = dir
	req.WritePaths = []string{"planned/NewFile.swift"}
	req.ProtectedPaths = []string{"accept.md"}
	req.ReadPaths = nil
	if err := validateCompositionRequest(req); err != nil {
		t.Fatalf("planned missing write should be allowed: %v", err)
	}
}

func TestComposeValidateFrozenReads(t *testing.T) {
	reads := []string{"a.md", "b.md", "c.md"}
	writes := []string{"b.md"}
	got := frozenReadPaths(reads, writes)
	if len(got) != 2 || got[0] != "a.md" || got[1] != "c.md" {
		t.Fatalf("frozen reads: %#v", got)
	}
}

func TestComposeValidateDecisionEveryVerdict(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	evidence := loadEvidenceFile(t, "testdata/compose/evidence/registry.json")
	req := sampleValidRequest(t, idx.Pin)
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	type tc struct {
		name    string
		dec     ComponentDecision
		wantErr string
	}
	cases := []tc{
		{
			name: "reuse",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-reuse",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"compose.thread-store"},
				CandidateRevs: []string{"1111111111111111111111111111111111111111"},
				Verdict:       VerdictReuseReference,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
		},
		{
			name: "adapt",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-adapt",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"compose.thread-store"},
				CandidateRevs: []string{"2222222222222222222222222222222222222222"},
				Verdict:       VerdictAdaptLocal,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
		},
		{
			name: "product_local",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-local",
				RequestHash:   reqHash,
				BehaviorID:    "audio-bridge",
				Verdict:       VerdictProductLocal,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "operator", Author: "operator"},
			},
		},
		{
			name: "propose_amsl",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-propose",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				EvidenceIDs:   []string{"ev-b-thread"},
				Verdict:       VerdictProposeAMSL,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
		},
		{
			name: "defer",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-defer",
				RequestHash:   reqHash,
				BehaviorID:    "audio-bridge",
				Verdict:       VerdictDefer,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
		},
		{
			name: "mutable_revision",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-mutable",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"compose.mutable-branch"},
				CandidateRevs: []string{"feature/not-a-commit"},
				Verdict:       VerdictReuseReference,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
			wantErr: "immutable",
		},
		{
			name: "unknown_candidate",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-miss",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"compose.missing"},
				CandidateRevs: []string{"1111111111111111111111111111111111111111"},
				Verdict:       VerdictReuseReference,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
			wantErr: "lookup",
		},
		{
			name: "empty_revision_not_executable",
			dec: ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-empty-rev",
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"identity.planned-only"},
				CandidateRevs: []string{""},
				Verdict:       VerdictReuseReference,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			},
			wantErr: "immutable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateComponentDecision(tc.dec, req, idx, evidence)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q got %v", tc.wantErr, err)
			}
		})
	}
}

func TestComposeValidateDependencyDAG(t *testing.T) {
	behaviors := []string{"thread-store", "audio-bridge", "session"}
	if err := validateDependencyDAG(behaviors, []ManifestEdge{
		{From: "audio-bridge", To: "thread-store"},
		{From: "session", To: "thread-store"},
	}); err != nil {
		t.Fatal(err)
	}
	// Diamond is fine.
	if err := validateDependencyDAG([]string{"a", "b", "c", "d"}, []ManifestEdge{
		{From: "a", To: "b"},
		{From: "a", To: "c"},
		{From: "b", To: "d"},
		{From: "c", To: "d"},
	}); err != nil {
		t.Fatal(err)
	}
	err := validateDependencyDAG(behaviors, []ManifestEdge{
		{From: "thread-store", To: "audio-bridge"},
		{From: "audio-bridge", To: "thread-store"},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle, got %v", err)
	}
	err = validateDependencyDAG(behaviors, []ManifestEdge{{From: "thread-store", To: "missing"}})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown node, got %v", err)
	}
}

func TestComposeValidateBundleHappyPath(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	evidence := loadEvidenceFile(t, "testdata/compose/evidence/registry.json")
	req := attachScopedWorkspace(t, sampleValidRequest(t, idx.Pin))
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	d1 := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-thread",
		RequestHash:   reqHash,
		BehaviorID:    "thread-store",
		CandidateIDs:  []string{"compose.thread-store"},
		CandidateRevs: []string{"1111111111111111111111111111111111111111"},
		Verdict:       VerdictAdaptLocal,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
	}
	d2 := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-audio",
		RequestHash:   reqHash,
		BehaviorID:    "audio-bridge",
		Verdict:       VerdictProductLocal,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "operator", Author: "operator"},
	}
	h1, err := hashComponentDecision(d1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashComponentDecision(d2)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-001",
		RequestHash:    reqHash,
		DecisionHashes: []string{h1, h2},
		Bindings: []Binding{
			{
				BehaviorID:   "thread-store",
				DecisionHash: h1,
				Kind:         "adapt",
				TargetRef:    "app/ThreadStore.swift",
				CatalogID:    "compose.thread-store",
				CatalogRev:   "1111111111111111111111111111111111111111",
			},
			{
				BehaviorID:   "audio-bridge",
				DecisionHash: h2,
				Kind:         "local_planned",
				TargetRef:    "app/AudioBridge.swift",
			},
		},
		Edges: []ManifestEdge{{From: "audio-bridge", To: "thread-store"}},
	}
	if err := validateCompositionBundle(CompositionBundle{
		Request:   req,
		Catalog:   idx,
		Evidence:  evidence,
		Decisions: []ComponentDecision{d1, d2},
		Manifest:  man,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestComposeValidateProposeRequiresFallback(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	evidence := loadEvidenceFile(t, "testdata/compose/evidence/registry.json")
	req := attachScopedWorkspace(t, sampleValidRequest(t, idx.Pin))
	req.Behaviors = []BehaviorNeed{
		{BehaviorID: "thread-store", Required: true, Contract: "contracts/thread-store.md"},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	d := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-propose",
		RequestHash:   reqHash,
		BehaviorID:    "thread-store",
		EvidenceIDs:   []string{"ev-b-thread"},
		Verdict:       VerdictProposeAMSL,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
	}
	h, err := hashComponentDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-propose",
		RequestHash:    reqHash,
		DecisionHashes: []string{h},
		Bindings: []Binding{{
			BehaviorID:   "thread-store",
			DecisionHash: h,
			Kind:         "local_implemented",
			TargetRef:    "shared/proposal",
		}},
	}
	err = validateCompositionBundle(CompositionBundle{
		Request: req, Catalog: idx, Evidence: evidence,
		Decisions: []ComponentDecision{d}, Manifest: man,
	})
	if err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("expected propose fallback requirement, got %v", err)
	}

	man.Bindings = append(man.Bindings, Binding{
		BehaviorID:   "thread-store",
		DecisionHash: h,
		Kind:         "local_planned",
		TargetRef:    "app/ThreadStore.swift",
	})
	if err := validateCompositionBundle(CompositionBundle{
		Request: req, Catalog: idx, Evidence: evidence,
		Decisions: []ComponentDecision{d}, Manifest: man,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestComposeValidateDeferRequiresFallback(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := attachScopedWorkspace(t, sampleValidRequest(t, idx.Pin))
	req.Behaviors = []BehaviorNeed{
		{BehaviorID: "audio-bridge", Required: true, Contract: "contracts/audio-bridge.md"},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	d := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-defer",
		RequestHash:   reqHash,
		BehaviorID:    "audio-bridge",
		Verdict:       VerdictDefer,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
	}
	h, err := hashComponentDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-defer",
		RequestHash:    reqHash,
		DecisionHashes: []string{h},
		Bindings: []Binding{{
			BehaviorID:   "audio-bridge",
			DecisionHash: h,
			Kind:         "local_implemented",
			TargetRef:    "deferred",
		}},
	}
	err = validateCompositionBundle(CompositionBundle{
		Request: req, Catalog: idx, Evidence: map[string]EvidenceRef{},
		Decisions: []ComponentDecision{d}, Manifest: man,
	})
	if err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("expected defer fallback requirement, got %v", err)
	}
}

func TestComposeValidateDuplicateBindingsAndExtras(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.Behaviors = []BehaviorNeed{
		{BehaviorID: "thread-store", Required: true, Contract: "contracts/thread-store.md"},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	d := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-local",
		RequestHash:   reqHash,
		BehaviorID:    "thread-store",
		Verdict:       VerdictProductLocal,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "operator", Author: "operator"},
	}
	h, err := hashComponentDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-dup",
		RequestHash:    reqHash,
		DecisionHashes: []string{h},
		Bindings: []Binding{
			{BehaviorID: "thread-store", DecisionHash: h, Kind: "local_planned", TargetRef: "a"},
			{BehaviorID: "thread-store", DecisionHash: h, Kind: "local_planned", TargetRef: "b"},
		},
	}
	err = validateApplicationManifest(man, req, []ComponentDecision{d})
	if err == nil || !strings.Contains(err.Error(), "duplicate binding") {
		t.Fatalf("expected duplicate binding, got %v", err)
	}
}

func TestComposeValidateBindingMustMatchDecision(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.Behaviors = []BehaviorNeed{
		{BehaviorID: "thread-store", Required: true, Contract: "contracts/thread-store.md"},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	d := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-adapt",
		RequestHash:   reqHash,
		BehaviorID:    "thread-store",
		CandidateIDs:  []string{"compose.thread-store"},
		CandidateRevs: []string{"1111111111111111111111111111111111111111"},
		Verdict:       VerdictAdaptLocal,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
	}
	h, err := hashComponentDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-mismatch",
		RequestHash:    reqHash,
		DecisionHashes: []string{h},
		Bindings: []Binding{{
			BehaviorID:   "thread-store",
			DecisionHash: h,
			Kind:         "adapt",
			TargetRef:    "app/ThreadStore.swift",
			CatalogID:    "compose.thread-store",
			CatalogRev:   "2222222222222222222222222222222222222222",
		}},
	}
	err = validateApplicationManifest(man, req, []ComponentDecision{d})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected binding/candidate mismatch, got %v", err)
	}
}

func TestComposeValidateSymlinkParentEscapeMissingLeaf(t *testing.T) {
	// Parent symlink + absent leaf must still run scopedPath and reject escape.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link-to-outside")); err != nil {
		t.Fatal(err)
	}
	prot := filepath.Join(root, "accept.md")
	if err := os.WriteFile(prot, []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.Workspace = root
	req.ReadPaths = nil
	req.WritePaths = []string{"link-to-outside/new.go"}
	req.ProtectedPaths = []string{"accept.md"}
	err = validateCompositionRequest(req)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection for missing leaf under link-to-outside, got %v", err)
	}
}

func TestComposeValidateInjectedCatalogRefOnLocalBinding(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := attachScopedWorkspace(t, sampleValidRequest(t, idx.Pin))
	req.Behaviors = []BehaviorNeed{
		{BehaviorID: "audio-bridge", Required: true, Contract: "contracts/audio-bridge.md"},
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	// product_local with no candidates; local_implemented/fallback must not
	// silently accept an injected catalog pair that is absent from the bundle catalog.
	d := ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-local-injected",
		RequestHash:   reqHash,
		BehaviorID:    "audio-bridge",
		Verdict:       VerdictProductLocal,
		Owner:         "local-operator",
		Provenance:    DecisionProvenance{Source: "operator", Author: "operator"},
	}
	h, err := hashComponentDecision(d)
	if err != nil {
		t.Fatal(err)
	}
	fakeRev := "ffffffffffffffffffffffffffffffffffffffff"
	man := ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-injected",
		RequestHash:    reqHash,
		DecisionHashes: []string{h},
		Bindings: []Binding{
			{
				BehaviorID:   "audio-bridge",
				DecisionHash: h,
				Kind:         "local_implemented",
				TargetRef:    "app/AudioBridge.swift",
				CatalogID:    "injected.not-in-catalog",
				CatalogRev:   fakeRev,
			},
			{
				BehaviorID:   "audio-bridge",
				DecisionHash: h,
				Kind:         "fallback",
				TargetRef:    "app/AudioBridgeFallback.swift",
				CatalogID:    "injected.not-in-catalog",
				CatalogRev:   fakeRev,
			},
		},
	}
	err = validateCompositionBundle(CompositionBundle{
		Request: req, Catalog: idx, Evidence: map[string]EvidenceRef{},
		Decisions: []ComponentDecision{d}, Manifest: man,
	})
	if err == nil || !strings.Contains(err.Error(), "catalog lookup") {
		t.Fatalf("expected injected catalog ref rejection, got %v", err)
	}

	// Partial catalog pair on local binding rejects even without candidates.
	man.Bindings = []Binding{{
		BehaviorID:   "audio-bridge",
		DecisionHash: h,
		Kind:         "local_implemented",
		TargetRef:    "app/AudioBridge.swift",
		CatalogID:    "compose.thread-store",
		CatalogRev:   "",
	}}
	err = validateApplicationManifest(man, req, []ComponentDecision{d})
	if err == nil || !strings.Contains(err.Error(), "both be set or both empty") {
		t.Fatalf("expected partial catalog pair rejection, got %v", err)
	}
}

func TestComposeValidateUnknownCandidatesAcrossVerdicts(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	evidence := loadEvidenceFile(t, "testdata/compose/evidence/registry.json")
	req := sampleValidRequest(t, idx.Pin)
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	fakeRev := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tc := range []struct {
		name    string
		verdict Verdict
		evid    []string
	}{
		{name: "product_local", verdict: VerdictProductLocal},
		{name: "defer", verdict: VerdictDefer},
		{name: "propose", verdict: VerdictProposeAMSL, evid: []string{"ev-b-thread"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-" + tc.name,
				RequestHash:   reqHash,
				BehaviorID:    "thread-store",
				CandidateIDs:  []string{"compose.missing-injected"},
				CandidateRevs: []string{fakeRev},
				EvidenceIDs:   tc.evid,
				Verdict:       tc.verdict,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: "import", Author: "operator"},
			}
			err := validateComponentDecision(d, req, idx, evidence)
			if err == nil || !strings.Contains(err.Error(), "lookup") {
				t.Fatalf("expected unknown candidate rejection, got %v", err)
			}
		})
	}
}

func TestComposeValidateShadowJevProvenanceRejected(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"shadow_jev", "selector_glm"} {
		t.Run(src, func(t *testing.T) {
			d := ComponentDecision{
				SchemaVersion: compositionSchemaVersion,
				DecisionID:    "dec-shadow",
				RequestHash:   reqHash,
				BehaviorID:    "audio-bridge",
				Verdict:       VerdictProductLocal,
				Owner:         "local-operator",
				Provenance:    DecisionProvenance{Source: src, Author: "shadow"},
			}
			err := validateComponentDecision(d, req, idx, map[string]EvidenceRef{})
			if err == nil || !strings.Contains(err.Error(), "executable bundle") {
				t.Fatalf("expected executable provenance rejection for %s, got %v", src, err)
			}
		})
	}
}
