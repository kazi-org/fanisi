package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"testing"
)

func sampleCompositionRequest() CompositionRequest {
	return CompositionRequest{
		SchemaVersion:   compositionSchemaVersion,
		RequestID:       "req-voice-compose-001",
		ParentID:        "parent-local-001",
		ProductRevision: "product@deadbeef",
		CatalogPin:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Requirements:    []string{"Async voice note preview with durable thread ids"},
		Behaviors: []BehaviorNeed{
			{BehaviorID: "thread-store", Required: true, Contract: "contracts/thread-store.md"},
			{BehaviorID: "audio-bridge", Required: true, Contract: "contracts/audio-bridge.md"},
		},
		Workspace:       ".",
		WritePaths:      []string{"app/Bridge.swift"},
		ProtectedPaths:  []string{"testdata/compose/protected/acceptance.md"},
		AuthorityRef:    "operator:david/local-c00",
		DeadlineRFC3339: "2026-10-03T00:00:00Z",
		Budget: CompositionBudget{
			MaxAttempts:     2,
			MaxSeconds:      1800,
			MaxEstimatedUSD: 0.5,
			ReservedUSD:     0.2,
			CurrencyNote:    "admission estimate; provider receipts separate",
		},
	}
}

func sampleComponentDecision() ComponentDecision {
	return ComponentDecision{
		SchemaVersion: compositionSchemaVersion,
		DecisionID:    "dec-thread-001",
		RequestHash:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		BehaviorID:    "thread-store",
		CandidateIDs:  []string{"cap.thread.store"},
		CandidateRevs: []string{"1"},
		Verdict:       VerdictAdaptLocal,
		Owner:         "local-operator",
		Provenance: DecisionProvenance{
			Source: "import",
			Author: "operator",
		},
	}
}

func sampleApplicationManifest() ApplicationManifest {
	return ApplicationManifest{
		SchemaVersion:  compositionSchemaVersion,
		ManifestID:     "man-001",
		RequestHash:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		DecisionHashes: []string{"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		Bindings: []Binding{
			{
				BehaviorID:   "thread-store",
				DecisionHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Kind:         "adapt",
				TargetRef:    "app/ThreadStore.swift",
			},
		},
		Edges: []ManifestEdge{{From: "audio-bridge", To: "thread-store"}},
	}
}

func sampleImplementationRequest() ImplementationRequest {
	return ImplementationRequest{
		SchemaVersion:   compositionSchemaVersion,
		AttemptID:       "att-001",
		ParentID:        "parent-local-001",
		ManifestHash:    "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		RequestHash:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		FenceToken:      "fence-001",
		Owner:           "local-operator",
		ReadPaths:       []string{"contracts/thread-store.md"},
		WritePaths:      []string{"app/Bridge.swift"},
		ProtectedPaths:  []string{"testdata/compose/protected/acceptance.md"},
		VerifyCommand:   []string{"./testdata/compose/delegate/ok.sh"},
		DelegateArgv:    []string{"./testdata/compose/delegate/ok.sh"},
		Workspace:       ".",
		OutputDir:       "out",
		ReservedUSD:     0.2,
		MaxSeconds:      60,
		EnvNames:        []string{"PATH"},
		DeadlineRFC3339: "2026-10-03T00:00:00Z",
		IdempotencyKey:  "parent-local-001|bbbb|intent",
	}
}

func sampleAttemptResult() AttemptResult {
	return AttemptResult{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     "att-001",
		FenceToken:    "fence-001",
		SourceHashes: map[string]string{
			"contracts/thread-store.md": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		},
		ProtectedHashes: map[string]string{
			"testdata/compose/protected/acceptance.md": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		},
		FinalWriteHashes: map[string]string{
			"app/Bridge.swift": "1111111111111111111111111111111111111111111111111111111111111111",
		},
		VerifierExit:   0,
		VerifierLogSHA: "2222222222222222222222222222222222222222222222222222222222222222",
		State:          AttemptVerifiedPendingReview,
		Usage: UsageKnown{
			UsageComplete: false,
		},
	}
}

func TestComposeHashStability(t *testing.T) {
	req := sampleCompositionRequest()
	h1, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("unstable or malformed request hash: %q vs %q", h1, h2)
	}

	// Map insertion order must not affect the canonical digest.
	a := map[string]any{"z": 1, "a": map[string]any{"m": true, "b": "x"}, "content_sha256": "ignored"}
	b := map[string]any{"a": map[string]any{"b": "x", "m": true}, "z": 1, "content_sha256": "other"}
	ha, err := hashCompositionValue(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := hashCompositionValue(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("map key order changed hash: %s vs %s", ha, hb)
	}

	pretty, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var decoded CompositionRequest
	if err := json.Unmarshal(pretty, &decoded); err != nil {
		t.Fatal(err)
	}
	hPretty, err := hashCompositionRequest(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if hPretty != h1 {
		t.Fatalf("pretty round-trip changed hash: %s vs %s", h1, hPretty)
	}

	for _, tc := range []struct {
		name string
		fn   func() (string, error)
	}{
		{"decision", func() (string, error) { return hashComponentDecision(sampleComponentDecision()) }},
		{"manifest", func() (string, error) { return hashApplicationManifest(sampleApplicationManifest()) }},
		{"impl", func() (string, error) { return hashImplementationRequest(sampleImplementationRequest()) }},
		{"result", func() (string, error) { return hashAttemptResult(sampleAttemptResult()) }},
	} {
		h, err := tc.fn()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		again, err := tc.fn()
		if err != nil {
			t.Fatalf("%s second: %v", tc.name, err)
		}
		if h != again || len(h) != 64 {
			t.Fatalf("%s unstable hash: %q vs %q", tc.name, h, again)
		}
	}
}

func TestComposeHashChangedFields(t *testing.T) {
	req := sampleCompositionRequest()
	base, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	req.RequestID = "req-other"
	changed, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if changed == base {
		t.Fatal("request_id change did not affect hash")
	}

	dec := sampleComponentDecision()
	dh, err := hashComponentDecision(dec)
	if err != nil {
		t.Fatal(err)
	}
	dec.Verdict = VerdictDefer
	dh2, err := hashComponentDecision(dec)
	if err != nil {
		t.Fatal(err)
	}
	if dh2 == dh {
		t.Fatal("verdict change did not affect decision hash")
	}

	man := sampleApplicationManifest()
	mh, err := hashApplicationManifest(man)
	if err != nil {
		t.Fatal(err)
	}
	man.Edges = append(man.Edges, ManifestEdge{From: "thread-store", To: "audio-bridge"})
	mh2, err := hashApplicationManifest(man)
	if err != nil {
		t.Fatal(err)
	}
	if mh2 == mh {
		t.Fatal("edge change did not affect manifest hash")
	}

	impl := sampleImplementationRequest()
	ih, err := hashImplementationRequest(impl)
	if err != nil {
		t.Fatal(err)
	}
	impl.MaxSeconds = 120
	ih2, err := hashImplementationRequest(impl)
	if err != nil {
		t.Fatal(err)
	}
	if ih2 == ih {
		t.Fatal("max_seconds change did not affect impl hash")
	}

	res := sampleAttemptResult()
	rh, err := hashAttemptResult(res)
	if err != nil {
		t.Fatal(err)
	}
	res.VerifierExit = 1
	rh2, err := hashAttemptResult(res)
	if err != nil {
		t.Fatal(err)
	}
	if rh2 == rh {
		t.Fatal("verifier_exit change did not affect result hash")
	}
}

func TestComposeHashExcludesContentSHA256(t *testing.T) {
	req := sampleCompositionRequest()
	without, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	with, err := hashCompositionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if without != with {
		t.Fatalf("content_sha256 affected request hash: %s vs %s", without, with)
	}

	canon, err := canonicalCompositionJSON(req)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canon, []byte(`"content_sha256"`)) {
		t.Fatalf("canonical JSON retained content_sha256: %s", canon)
	}
	if digest(canon) != with {
		t.Fatal("hashCompositionRequest disagreed with digest(canonical)")
	}

	impl := sampleImplementationRequest()
	impl.ContentSHA256 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	ih1, err := hashImplementationRequest(impl)
	if err != nil {
		t.Fatal(err)
	}
	impl.ContentSHA256 = ""
	ih2, err := hashImplementationRequest(impl)
	if err != nil {
		t.Fatal(err)
	}
	if ih1 != ih2 {
		t.Fatal("impl content_sha256 affected hash")
	}

	res := sampleAttemptResult()
	res.ContentSHA256 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	rh1, err := hashAttemptResult(res)
	if err != nil {
		t.Fatal(err)
	}
	res.ContentSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rh2, err := hashAttemptResult(res)
	if err != nil {
		t.Fatal(err)
	}
	if rh1 != rh2 {
		t.Fatal("result content_sha256 affected hash")
	}
}

func TestComposeHashNumericLossless(t *testing.T) {
	budget := CompositionBudget{
		MaxAttempts:     2,
		MaxSeconds:      1800,
		MaxEstimatedUSD: 0.5,
		ReservedUSD:     0.2,
		CurrencyNote:    "admission estimate; provider receipts separate",
	}
	canon, err := canonicalCompositionJSON(budget)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canon, []byte(`"max_estimated_usd":0.5`)) {
		t.Fatalf("expected lossless 0.5 in canonical JSON, got %s", canon)
	}
	if !bytes.Contains(canon, []byte(`"reserved_usd":0.2`)) {
		t.Fatalf("expected lossless 0.2 in canonical JSON, got %s", canon)
	}

	// Large integer must survive as a JSON number, not a float rewrite.
	raw := json.RawMessage(`{"n":9007199254740993,"x":0.1}`)
	h1, err := hashCompositionValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var node any
	if err := dec.Decode(&node); err != nil {
		t.Fatal(err)
	}
	canon2, err := canonicalCompositionJSON(node)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canon2, []byte(`"n":9007199254740993`)) {
		t.Fatalf("integer not preserved: %s", canon2)
	}
	if !bytes.Contains(canon2, []byte(`"x":0.1`)) {
		t.Fatalf("fraction not preserved: %s", canon2)
	}
	h2, err := hashCompositionValue(node)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("raw vs decoded number hash mismatch: %s vs %s", h1, h2)
	}

	req := sampleCompositionRequest()
	req.Budget.ReservedUSD = 0.2
	canonReq, err := canonicalCompositionJSON(req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonReq, []byte(`"reserved_usd":0.2`)) {
		t.Fatalf("request budget reserved_usd not lossless: %s", canonReq)
	}
}

func TestComposeHashDoesNotMutateInput(t *testing.T) {
	req := sampleCompositionRequest()
	req.ContentSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req.Budget.ReservedUSD = 0.2
	beforeSHA := req.ContentSHA256
	beforeBudget := req.Budget

	if _, err := hashCompositionRequest(req); err != nil {
		t.Fatal(err)
	}
	if req.ContentSHA256 != beforeSHA {
		t.Fatalf("mutated ContentSHA256: %q", req.ContentSHA256)
	}
	if req.Budget != beforeBudget {
		t.Fatalf("mutated Budget: %+v", req.Budget)
	}

	src := map[string]string{"b": "2", "a": "1"}
	res := sampleAttemptResult()
	res.SourceHashes = maps.Clone(src)
	res.ContentSHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := hashAttemptResult(res); err != nil {
		t.Fatal(err)
	}
	if res.ContentSHA256 != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal("mutated result ContentSHA256")
	}
	if !maps.Equal(res.SourceHashes, src) {
		t.Fatalf("mutated SourceHashes: %#v", res.SourceHashes)
	}

	payload := map[string]any{
		"z":              1,
		"a":              "keep",
		"content_sha256": "should-remain",
	}
	clone := maps.Clone(payload)
	if _, err := hashCompositionValue(payload); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(payload, clone) {
		t.Fatalf("mutated map input: %#v", payload)
	}
}
