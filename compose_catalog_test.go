package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeCatalogLoadAMSLSchema(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if idx.SchemaVersion != compositionSchemaVersion {
		t.Fatalf("schema_version: %d", idx.SchemaVersion)
	}
	raw, err := os.ReadFile("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if idx.Pin != digest(raw) {
		t.Fatalf("pin is not raw source digest: got %s want %s", idx.Pin, digest(raw))
	}
	if !filepath.IsAbs(idx.Root) {
		t.Fatalf("root not absolute: %q", idx.Root)
	}
	if len(idx.Records) < 6 {
		t.Fatalf("expected >=6 records, got %d", len(idx.Records))
	}
	families := map[string]struct{}{}
	for _, rec := range idx.Records {
		families[rec.Family] = struct{}{}
	}
	if len(families) < 2 {
		t.Fatalf("expected >=2 families, got %v", families)
	}

	// Multiple revisions of one capability are preserved.
	thread := listCatalog(idx, "compose")
	var threadRevs []string
	for _, rec := range thread {
		if rec.ID == "compose.thread-store" {
			threadRevs = append(threadRevs, rec.Revision)
		}
	}
	if len(threadRevs) < 2 {
		t.Fatalf("expected multiple thread-store revisions, got %v", threadRevs)
	}

	// Inspection-only row without implementation revision.
	if _, err := lookupCatalog(idx, "identity.planned-only", ""); err != nil {
		t.Fatalf("inspection row missing: %v", err)
	}

	// Mutable branch revision is retained for inspection but is not immutable.
	mutable, err := lookupCatalog(idx, "compose.mutable-branch", "feature/not-a-commit")
	if err != nil {
		t.Fatal(err)
	}
	if immutableCatalogRevision(mutable.Revision) {
		t.Fatal("mutable revision unexpectedly treated as immutable")
	}
}

func TestComposeCatalogPinRelocationIndependent(t *testing.T) {
	src := "testdata/compose/catalog/index.json"
	idx1, err := loadCatalog(src)
	if err != nil {
		t.Fatal(err)
	}
	pinBody1, err := pinCatalog(idx1)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "relocated-index.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	idx2, err := loadCatalog(dst)
	if err != nil {
		t.Fatal(err)
	}
	if idx1.Pin != idx2.Pin {
		t.Fatalf("raw pin changed after relocation: %s vs %s", idx1.Pin, idx2.Pin)
	}
	if idx1.Root == idx2.Root {
		t.Fatal("expected Root to change after relocation")
	}
	pinBody2, err := pinCatalog(idx2)
	if err != nil {
		t.Fatal(err)
	}
	if pinBody1 != pinBody2 {
		t.Fatalf("pinCatalog changed after relocation: %s vs %s", pinBody1, pinBody2)
	}
}

func TestComposeCatalogStalePin(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	req := sampleValidRequest(t, idx.Pin)
	req.CatalogPin = strings.Repeat("0", 64)
	if err := validateCompositionBundle(CompositionBundle{
		Request:   req,
		Catalog:   idx,
		Evidence:  map[string]EvidenceRef{},
		Decisions: nil,
		Manifest:  ApplicationManifest{SchemaVersion: compositionSchemaVersion, ManifestID: "man-1"},
	}); err == nil || !strings.Contains(err.Error(), "catalog pin mismatch") {
		t.Fatalf("expected stale catalog pin mismatch, got %v", err)
	}
}

func TestComposeCatalogLookupCaseSensitive(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lookupCatalog(idx, "compose.thread-store", "1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupCatalog(idx, "Compose.thread-store", "1111111111111111111111111111111111111111"); err == nil {
		t.Fatal("expected case-sensitive id miss")
	}
	if _, err := lookupCatalog(idx, "compose.thread-store", "1111111111111111111111111111111111111112"); err == nil {
		t.Fatal("expected revision miss")
	}
}

func TestComposeCatalogDuplicatesRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.json")
	doc := map[string]any{
		"schema_version": 1,
		"capabilities": []map[string]any{
			{
				"id":                  "dup.cap",
				"domain":              "compose",
				"summary":             "dup",
				"status":              "CANDIDATE",
				"schema_version":      1,
				"name":                "dup",
				"use_when":            []string{},
				"non_goals":           []string{},
				"proposed_guarantees": []string{},
				"failure_modes":       []string{},
				"evidence":            []any{},
				"alternatives":        []any{},
				"implementations": []map[string]string{
					{"revision": "1111111111111111111111111111111111111111", "url": "https://example.invalid/a"},
					{"revision": "1111111111111111111111111111111111111111", "url": "https://example.invalid/b"},
				},
				"verification":   []any{},
				"open_questions": []string{},
				"disposition":    "WATCH",
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCatalog(path); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate identity error, got %v", err)
	}
}

func TestComposeCatalogNormalizedShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "normalized.json")
	doc := normalizedCatalogFile{
		SchemaVersion: 1,
		SourceLabel:   "test-normalized",
		Records: []CatalogRecord{
			{ID: "norm.one", Revision: "1111111111111111111111111111111111111111", Family: "norm", Summary: "a", Maturity: "CANDIDATE"},
			{ID: "norm.two", Revision: "", Family: "norm", Summary: "inspect", Maturity: "DISCOVERED"},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := loadCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Records) != 2 {
		t.Fatalf("records: %d", len(idx.Records))
	}
	if idx.SourceLabel != "test-normalized" {
		t.Fatalf("source_label: %q", idx.SourceLabel)
	}
}

func TestComposeCatalogListFamily(t *testing.T) {
	idx, err := loadCatalog("testdata/compose/catalog/index.json")
	if err != nil {
		t.Fatal(err)
	}
	all := listCatalog(idx, "")
	compose := listCatalog(idx, "compose")
	identity := listCatalog(idx, "identity")
	if len(all) != len(idx.Records) {
		t.Fatalf("list all: %d vs %d", len(all), len(idx.Records))
	}
	if len(compose) == 0 || len(identity) == 0 {
		t.Fatalf("family filters empty: compose=%d identity=%d", len(compose), len(identity))
	}
	for _, rec := range compose {
		if rec.Family != "compose" {
			t.Fatalf("unexpected family %q", rec.Family)
		}
	}
}

func TestComposeCatalogOptionalReferencePresent(t *testing.T) {
	path := ".fanisi/reference-catalog.json"
	if _, err := os.Stat(path); err != nil {
		t.Skip("optional reference catalog absent")
	}
	idx, err := loadCatalog(path)
	if err != nil {
		t.Fatalf("reference catalog load: %v", err)
	}
	if len(idx.Records) < 6 {
		t.Fatalf("reference catalog too small: %d", len(idx.Records))
	}
	// Ensure no-impl capabilities survive as inspection rows.
	foundEmpty := false
	for _, rec := range idx.Records {
		if rec.Revision == "" {
			foundEmpty = true
			break
		}
	}
	if !foundEmpty {
		t.Fatal("expected at least one inspection-only empty revision in reference catalog")
	}
}

func TestComposeCatalogCapabilitySchemaVersionRequired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing-cap-schema.json")
	doc := map[string]any{
		"schema_version": 1,
		"capabilities": []map[string]any{
			{
				"id":                  "cap.missing-schema",
				"domain":              "compose",
				"summary":             "missing per-capability schema_version",
				"status":              "CANDIDATE",
				"name":                "missing",
				"use_when":            []string{},
				"non_goals":           []string{},
				"proposed_guarantees": []string{},
				"failure_modes":       []string{},
				"evidence":            []any{},
				"alternatives":        []any{},
				"implementations": []map[string]string{
					{"revision": "1111111111111111111111111111111111111111", "url": "https://example.invalid/a"},
				},
				"verification":   []any{},
				"open_questions": []string{},
				"disposition":    "WATCH",
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCatalog(path); err == nil || !strings.Contains(err.Error(), "missing schema_version") {
		t.Fatalf("expected missing per-capability schema_version, got %v", err)
	}
}

func TestComposeCatalogCapabilityFutureSchemaVersionRejected(t *testing.T) {
	// Real catalog (schema_version 1 per capability) must keep loading.
	if _, err := loadCatalog("testdata/compose/catalog/index.json"); err != nil {
		t.Fatalf("real catalog must remain loadable: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "future-cap-schema.json")
	doc := map[string]any{
		"schema_version": 1,
		"capabilities": []map[string]any{
			{
				"id":                  "cap.future",
				"domain":              "compose",
				"summary":             "future per-capability schema",
				"status":              "CANDIDATE",
				"schema_version":      compositionSchemaVersion + 1,
				"name":                "future",
				"use_when":            []string{},
				"non_goals":           []string{},
				"proposed_guarantees": []string{},
				"failure_modes":       []string{},
				"evidence":            []any{},
				"alternatives":        []any{},
				"implementations": []map[string]string{
					{"revision": "1111111111111111111111111111111111111111", "url": "https://example.invalid/a"},
				},
				"verification":   []any{},
				"open_questions": []string{},
				"disposition":    "WATCH",
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCatalog(path); err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("expected future per-capability schema_version rejection, got %v", err)
	}
}
