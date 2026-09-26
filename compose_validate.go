package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var allowedVerdicts = map[Verdict]struct{}{
	VerdictReuseReference: {},
	VerdictAdaptLocal:     {},
	VerdictProductLocal:   {},
	VerdictProposeAMSL:    {},
	VerdictDefer:          {},
}

var allowedBindingKinds = map[string]struct{}{
	"reuse":             {},
	"adapt":             {},
	"local_planned":     {},
	"local_implemented": {},
	"fallback":          {},
}

// executableProvenanceSources are the only provenance labels permitted on
// decisions that enter an executable composition bundle/dispatch path.
// selector_glm and shadow_jev exist as type vocabulary for future/shadow
// backends; contract 1.1 ships import/command only, so those labels must be
// converted by an operator (to operator/import) before bundle admission.
var executableProvenanceSources = map[string]struct{}{
	"operator": {},
	"import":   {},
}

// validateCompositionRequest checks executable request fields: schema, IDs,
// finite positive budgets, path shape, protected/write nesting, and optional
// content hash. It does not require planned write files to exist on disk.
func validateCompositionRequest(r CompositionRequest) error {
	if r.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("composition request schema_version must be %d", compositionSchemaVersion)
	}
	if !simpleID(r.RequestID) {
		return fmt.Errorf("request_id %q is not a simple id", r.RequestID)
	}
	if !simpleID(r.ParentID) {
		return fmt.Errorf("parent_id %q is not a simple id", r.ParentID)
	}
	if strings.TrimSpace(r.ProductRevision) == "" {
		return fmt.Errorf("product_revision is required")
	}
	if !isSHA256Hex(r.CatalogPin) {
		return fmt.Errorf("catalog_pin must be a 64-hex digest")
	}
	if len(r.Requirements) == 0 {
		return fmt.Errorf("requirements must be nonempty")
	}
	for i, line := range r.Requirements {
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("requirements[%d] is empty", i)
		}
	}
	if len(r.Behaviors) == 0 {
		return fmt.Errorf("behaviors must be nonempty")
	}
	seenBehavior := map[string]struct{}{}
	for i, b := range r.Behaviors {
		if !simpleID(b.BehaviorID) {
			return fmt.Errorf("behaviors[%d].behavior_id %q is not a simple id", i, b.BehaviorID)
		}
		if _, dup := seenBehavior[b.BehaviorID]; dup {
			return fmt.Errorf("duplicate behavior_id %q", b.BehaviorID)
		}
		seenBehavior[b.BehaviorID] = struct{}{}
		if strings.TrimSpace(b.Contract) == "" {
			return fmt.Errorf("behaviors[%d] (%s): contract is required", i, b.BehaviorID)
		}
	}
	if strings.TrimSpace(r.Workspace) == "" {
		return fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(r.AuthorityRef) == "" {
		return fmt.Errorf("authority_ref is required")
	}
	if _, err := time.Parse(time.RFC3339, r.DeadlineRFC3339); err != nil {
		return fmt.Errorf("deadline must be RFC3339: %w", err)
	}
	if err := validateCompositionBudget(r.Budget); err != nil {
		return err
	}
	if err := validateRequestScopePaths(r); err != nil {
		return err
	}
	if err := checkOptionalContentSHA256(r.ContentSHA256, func() (string, error) {
		return hashCompositionRequest(r)
	}); err != nil {
		return err
	}
	return nil
}

func validateCompositionBudget(b CompositionBudget) error {
	if b.MaxAttempts <= 0 {
		return fmt.Errorf("budget.max_attempts must be positive")
	}
	if b.MaxSeconds <= 0 {
		return fmt.Errorf("budget.max_seconds must be positive")
	}
	if err := requireFinitePositive("budget.max_estimated_usd", b.MaxEstimatedUSD); err != nil {
		return err
	}
	if err := requireFiniteNonNegative("budget.reserved_usd", b.ReservedUSD); err != nil {
		return err
	}
	if b.ReservedUSD > b.MaxEstimatedUSD {
		return fmt.Errorf("budget.reserved_usd exceeds max_estimated_usd")
	}
	if strings.TrimSpace(b.CurrencyNote) == "" {
		return fmt.Errorf("budget.currency_note is required")
	}
	return nil
}

func requireFinitePositive(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fmt.Errorf("%s must be finite and positive", name)
	}
	return nil
}

func requireFiniteNonNegative(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return fmt.Errorf("%s must be finite and non-negative", name)
	}
	return nil
}

func validateRequestScopePaths(r CompositionRequest) error {
	writes := append([]string(nil), r.WritePaths...)
	reads := append([]string(nil), r.ReadPaths...)
	protected := append([]string(nil), r.ProtectedPaths...)
	if len(writes) == 0 {
		return fmt.Errorf("write_paths must be nonempty")
	}
	if len(protected) == 0 {
		return fmt.Errorf("protected_paths must be nonempty")
	}
	seen := map[string]struct{}{}
	for _, p := range writes {
		if err := validRelativePath(p); err != nil {
			return fmt.Errorf("write_paths: %w", err)
		}
		if _, dup := seen[p]; dup {
			return fmt.Errorf("duplicate write path %q", p)
		}
		seen[p] = struct{}{}
	}
	seenRead := map[string]struct{}{}
	for _, p := range reads {
		if err := validRelativePath(p); err != nil {
			return fmt.Errorf("read_paths: %w", err)
		}
		if _, dup := seenRead[p]; dup {
			return fmt.Errorf("duplicate read path %q", p)
		}
		seenRead[p] = struct{}{}
	}
	seenProt := map[string]struct{}{}
	for _, p := range protected {
		if err := validRelativePath(p); err != nil {
			return fmt.Errorf("protected_paths: %w", err)
		}
		if _, dup := seenProt[p]; dup {
			return fmt.Errorf("duplicate protected path %q", p)
		}
		seenProt[p] = struct{}{}
	}
	for _, p := range protected {
		for _, w := range writes {
			if pathsOverlapOrNest(p, w) {
				return fmt.Errorf("protected path %q overlaps or nests with write path %q", p, w)
			}
		}
	}
	_ = frozenReadPaths(reads, writes)

	if err := probeScopeFilesystem(r.Workspace, reads, writes, protected); err != nil {
		return err
	}
	return nil
}

// frozenReadPaths returns read paths that are not also writes.
func frozenReadPaths(reads, writes []string) []string {
	writeSet := map[string]struct{}{}
	for _, w := range writes {
		writeSet[w] = struct{}{}
	}
	var out []string
	for _, r := range reads {
		if _, isWrite := writeSet[r]; isWrite {
			continue
		}
		out = append(out, r)
	}
	return out
}

func pathsOverlapOrNest(a, b string) bool {
	if a == b {
		return true
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	sep := string(filepath.Separator)
	if strings.HasPrefix(b, a+sep) || strings.HasPrefix(a, b+sep) {
		return true
	}
	return false
}

// probeScopeFilesystem always runs scopedPath on every write/read/protected
// path—including missing write leaves—so parent symlink escapes are rejected
// before any existence rule. The workspace root must be a nonempty existing
// directory. Planned write targets may be absent; frozen reads and protected
// paths must exist as regular files.
func probeScopeFilesystem(workspace string, reads, writes, protected []string) error {
	if strings.TrimSpace(workspace) == "" {
		return fmt.Errorf("workspace is required")
	}
	root, err := composeCanonicalRoot(workspace)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("workspace %q: %w", workspace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace %q is not a directory", workspace)
	}

	allowedReads := append(append([]string{}, reads...), writes...)
	for _, p := range frozenReadPaths(reads, writes) {
		if _, err := scopedPath(root, p, allowedReads); err != nil {
			return fmt.Errorf("read path %q: %w", p, err)
		}
		if err := requireExistingRegularFile(root, p); err != nil {
			return fmt.Errorf("read path %q: %w", p, err)
		}
	}
	for _, p := range writes {
		if _, err := scopedPath(root, p, writes); err != nil {
			return fmt.Errorf("write path %q: %w", p, err)
		}
		full := filepath.Join(root, p)
		st, err := os.Lstat(full)
		if err != nil {
			if os.IsNotExist(err) {
				continue // planned write may be absent after scopedPath
			}
			return fmt.Errorf("write path %q: %w", p, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("write path %q: must be a regular file when present", p)
		}
	}
	for _, p := range protected {
		if _, err := scopedPath(root, p, protected); err != nil {
			return fmt.Errorf("protected path %q: %w", p, err)
		}
		if err := requireExistingRegularFile(root, p); err != nil {
			return fmt.Errorf("protected path %q: %w", p, err)
		}
	}
	return nil
}

func requireExistingRegularFile(root, rel string) error {
	full := filepath.Join(root, rel)
	st, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("must exist as a regular file")
		}
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("must be a regular file")
	}
	return nil
}

// validateComponentDecision checks a decision against the request, catalog, and
// trusted evidence map. Catalog maturity is never mutated; candidate revisions
// for executable reuse must be immutable 40/64-hex pins present in the catalog.
func validateComponentDecision(d ComponentDecision, req CompositionRequest, cat CatalogIndex, evidence map[string]EvidenceRef) error {
	if d.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("decision schema_version must be %d", compositionSchemaVersion)
	}
	if !simpleID(d.DecisionID) {
		return fmt.Errorf("decision_id %q is not a simple id", d.DecisionID)
	}
	if !simpleID(d.BehaviorID) {
		return fmt.Errorf("behavior_id %q is not a simple id", d.BehaviorID)
	}
	if !behaviorInRequest(req, d.BehaviorID) {
		return fmt.Errorf("decision behavior_id %q is not in the request", d.BehaviorID)
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		return err
	}
	if d.RequestHash != reqHash {
		return fmt.Errorf("decision request_hash mismatch")
	}
	if _, ok := allowedVerdicts[d.Verdict]; !ok {
		return fmt.Errorf("unknown verdict %q", d.Verdict)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return fmt.Errorf("decision owner is required")
	}
	if _, ok := executableProvenanceSources[d.Provenance.Source]; !ok {
		return fmt.Errorf("decision provenance.source %q is not allowed in an executable bundle (operator must convert/import; contract 1.1 admits import/command only)", d.Provenance.Source)
	}
	if strings.TrimSpace(d.Provenance.Author) == "" {
		return fmt.Errorf("decision provenance.author is required")
	}
	if len(d.CandidateIDs) != len(d.CandidateRevs) {
		return fmt.Errorf("candidate_ids and candidate_revisions length mismatch")
	}
	refs := make([]EvidenceRef, 0, len(d.EvidenceIDs))
	for _, id := range d.EvidenceIDs {
		refs = append(refs, EvidenceRef{ID: id})
	}
	if err := validateEvidenceForVerdict(d.Verdict, refs, evidence); err != nil {
		return err
	}
	switch d.Verdict {
	case VerdictReuseReference, VerdictAdaptLocal:
		if len(d.CandidateIDs) == 0 {
			return fmt.Errorf("verdict %s requires catalog candidate_ids", d.Verdict)
		}
	case VerdictProductLocal, VerdictProposeAMSL, VerdictDefer:
		// Candidates optional; required fallback/local_planned binding is at manifest level.
	}
	// Any supplied candidate refs must independently resolve as immutable catalog pins,
	// including product_local / defer / propose (unknown or mutable refs reject).
	for i, id := range d.CandidateIDs {
		rev := d.CandidateRevs[i]
		if !immutableCatalogRevision(rev) {
			return fmt.Errorf("candidate %q revision %q is not an immutable 40/64-hex pin", id, rev)
		}
		if _, err := lookupCatalog(cat, id, rev); err != nil {
			return fmt.Errorf("candidate lookup: %w", err)
		}
	}
	if err := checkOptionalContentSHA256(d.ContentSHA256, func() (string, error) {
		return hashComponentDecision(d)
	}); err != nil {
		return err
	}
	return nil
}

func behaviorInRequest(req CompositionRequest, id string) bool {
	for _, b := range req.Behaviors {
		if b.BehaviorID == id {
			return true
		}
	}
	return false
}

// validateApplicationManifest checks request/decision hash links, bindings,
// required-behavior coverage including defer/propose fallbacks, and the DAG.
func validateApplicationManifest(m ApplicationManifest, req CompositionRequest, decisions []ComponentDecision) error {
	if m.SchemaVersion != compositionSchemaVersion {
		return fmt.Errorf("manifest schema_version must be %d", compositionSchemaVersion)
	}
	if !simpleID(m.ManifestID) {
		return fmt.Errorf("manifest_id %q is not a simple id", m.ManifestID)
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		return err
	}
	if m.RequestHash != reqHash {
		return fmt.Errorf("manifest request_hash mismatch")
	}

	decisionByHash := map[string]ComponentDecision{}
	for _, d := range decisions {
		h, err := hashComponentDecision(d)
		if err != nil {
			return err
		}
		if d.ContentSHA256 != "" && d.ContentSHA256 != h {
			return fmt.Errorf("decision %q content_sha256 mismatch", d.DecisionID)
		}
		if _, dup := decisionByHash[h]; dup {
			return fmt.Errorf("duplicate decision hash %s", h)
		}
		decisionByHash[h] = d
	}

	if len(m.DecisionHashes) != len(decisions) {
		return fmt.Errorf("manifest decision_hashes count %d != decisions %d", len(m.DecisionHashes), len(decisions))
	}
	seenListed := map[string]struct{}{}
	for _, h := range m.DecisionHashes {
		if !isSHA256Hex(h) {
			return fmt.Errorf("manifest decision hash %q is not a 64-hex digest", h)
		}
		if _, ok := decisionByHash[h]; !ok {
			return fmt.Errorf("manifest decision hash %s not linked to a provided decision", h)
		}
		if _, dup := seenListed[h]; dup {
			return fmt.Errorf("duplicate decision hash in manifest: %s", h)
		}
		seenListed[h] = struct{}{}
	}
	for h := range decisionByHash {
		if _, ok := seenListed[h]; !ok {
			return fmt.Errorf("decision hash %s missing from manifest decision_hashes", h)
		}
	}

	behaviorIDs := make([]string, 0, len(req.Behaviors))
	behaviorSet := map[string]struct{}{}
	for _, b := range req.Behaviors {
		behaviorIDs = append(behaviorIDs, b.BehaviorID)
		behaviorSet[b.BehaviorID] = struct{}{}
	}

	type bindKey struct{ behavior, kind string }
	seenBind := map[bindKey]struct{}{}
	bindingsByBehavior := map[string][]Binding{}

	for i, b := range m.Bindings {
		if !simpleID(b.BehaviorID) {
			return fmt.Errorf("bindings[%d].behavior_id %q is not a simple id", i, b.BehaviorID)
		}
		if _, ok := behaviorSet[b.BehaviorID]; !ok {
			return fmt.Errorf("bindings[%d]: unknown behavior_id %q", i, b.BehaviorID)
		}
		if _, ok := allowedBindingKinds[b.Kind]; !ok {
			return fmt.Errorf("bindings[%d]: unknown kind %q", i, b.Kind)
		}
		if strings.TrimSpace(b.TargetRef) == "" {
			return fmt.Errorf("bindings[%d]: target_ref is required", i)
		}
		if !isSHA256Hex(b.DecisionHash) {
			return fmt.Errorf("bindings[%d]: decision_hash must be 64-hex", i)
		}
		dec, ok := decisionByHash[b.DecisionHash]
		if !ok {
			return fmt.Errorf("bindings[%d]: decision_hash not linked", i)
		}
		if dec.BehaviorID != b.BehaviorID {
			return fmt.Errorf("bindings[%d]: decision behavior %q does not match binding behavior %q", i, dec.BehaviorID, b.BehaviorID)
		}
		key := bindKey{b.BehaviorID, b.Kind}
		if _, dup := seenBind[key]; dup {
			return fmt.Errorf("duplicate binding for behavior %q kind %q", b.BehaviorID, b.Kind)
		}
		seenBind[key] = struct{}{}
		if err := bindingMatchesDecision(b, dec); err != nil {
			return fmt.Errorf("bindings[%d]: %w", i, err)
		}
		bindingsByBehavior[b.BehaviorID] = append(bindingsByBehavior[b.BehaviorID], b)
	}

	for _, need := range req.Behaviors {
		if !need.Required {
			continue
		}
		binds := bindingsByBehavior[need.BehaviorID]
		if len(binds) == 0 {
			return fmt.Errorf("required behavior %q has no binding", need.BehaviorID)
		}
		var governing ComponentDecision
		foundGoverning := false
		hasEscape := false
		for _, b := range binds {
			if b.Kind == "fallback" || b.Kind == "local_planned" {
				hasEscape = true
			}
			dec := decisionByHash[b.DecisionHash]
			if b.Kind != "fallback" && b.Kind != "local_planned" {
				governing = dec
				foundGoverning = true
			} else if !foundGoverning {
				governing = dec
				foundGoverning = true
			}
		}
		if !foundGoverning {
			return fmt.Errorf("required behavior %q has no governing decision", need.BehaviorID)
		}
		if governing.Verdict == VerdictProposeAMSL || governing.Verdict == VerdictDefer {
			if !hasEscape {
				return fmt.Errorf("required behavior %q with verdict %s needs explicit fallback or local_planned binding", need.BehaviorID, governing.Verdict)
			}
		}
	}

	if err := validateDependencyDAG(behaviorIDs, m.Edges); err != nil {
		return err
	}
	if err := checkOptionalContentSHA256(m.ContentSHA256, func() (string, error) {
		return hashApplicationManifest(m)
	}); err != nil {
		return err
	}
	return nil
}

func bindingMatchesDecision(b Binding, d ComponentDecision) error {
	hasID := b.CatalogID != ""
	hasRev := b.CatalogRev != ""
	if hasID != hasRev {
		return fmt.Errorf("catalog_id and catalog_revision must both be set or both empty")
	}
	switch b.Kind {
	case "reuse", "adapt":
		if !hasID || !hasRev {
			return fmt.Errorf("kind %s requires catalog_id and catalog_revision", b.Kind)
		}
		matched := false
		for i, id := range d.CandidateIDs {
			if id == b.CatalogID && d.CandidateRevs[i] == b.CatalogRev {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("binding catalog %s@%s does not match decision candidates", b.CatalogID, b.CatalogRev)
		}
		if b.Kind == "reuse" && d.Verdict != VerdictReuseReference {
			return fmt.Errorf("reuse binding requires reuse_reference verdict")
		}
		if b.Kind == "adapt" && d.Verdict != VerdictAdaptLocal {
			return fmt.Errorf("adapt binding requires adapt_local verdict")
		}
	case "local_planned", "local_implemented", "fallback":
		// Local-only bindings may omit catalog refs. A complete pair is allowed
		// structurally here; validateCompositionBundle independently looks it up
		// in the bundle catalog regardless of decision candidates/verdict.
	}
	return nil
}

// validateBindingCatalogLookups requires every complete binding catalog pair to
// resolve as an immutable pin in the bundle catalog, independent of whether the
// linked decision listed candidates.
func validateBindingCatalogLookups(bindings []Binding, cat CatalogIndex) error {
	for i, b := range bindings {
		if b.CatalogID == "" && b.CatalogRev == "" {
			continue
		}
		if b.CatalogID == "" || b.CatalogRev == "" {
			return fmt.Errorf("bindings[%d]: catalog_id and catalog_revision must both be set or both empty", i)
		}
		if !immutableCatalogRevision(b.CatalogRev) {
			return fmt.Errorf("bindings[%d]: catalog_revision %q is not an immutable 40/64-hex pin", i, b.CatalogRev)
		}
		if _, err := lookupCatalog(cat, b.CatalogID, b.CatalogRev); err != nil {
			return fmt.Errorf("bindings[%d]: catalog lookup: %w", i, err)
		}
	}
	return nil
}

// validateDependencyDAG rejects unknown nodes and cycles (Kahn's algorithm).
func validateDependencyDAG(behaviors []string, edges []ManifestEdge) error {
	nodes := map[string]struct{}{}
	for _, b := range behaviors {
		nodes[b] = struct{}{}
	}
	indeg := map[string]int{}
	for b := range nodes {
		indeg[b] = 0
	}
	adj := map[string][]string{}
	seenEdge := map[string]struct{}{}
	for i, e := range edges {
		if _, ok := nodes[e.From]; !ok {
			return fmt.Errorf("dependency edge[%d] unknown from %q", i, e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return fmt.Errorf("dependency edge[%d] unknown to %q", i, e.To)
		}
		key := e.From + "->" + e.To
		if _, dup := seenEdge[key]; dup {
			return fmt.Errorf("duplicate dependency edge %s", key)
		}
		seenEdge[key] = struct{}{}
		adj[e.From] = append(adj[e.From], e.To)
		indeg[e.To]++
	}
	var queue []string
	for b, d := range indeg {
		if d == 0 {
			queue = append(queue, b)
		}
	}
	visited := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		visited++
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				queue = append(queue, m)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("dependency graph contains a cycle")
	}
	return nil
}

// validateCompositionBundle validates the full offline composition bundle:
// catalog pin match, linked decisions, required behaviors exactly resolved.
func validateCompositionBundle(b CompositionBundle) error {
	if err := validateCompositionRequest(b.Request); err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if b.Catalog.Pin != b.Request.CatalogPin {
		return fmt.Errorf("catalog pin mismatch: catalog %s request %s", b.Catalog.Pin, b.Request.CatalogPin)
	}
	seenDecisionID := map[string]struct{}{}
	for i, d := range b.Decisions {
		if _, dup := seenDecisionID[d.DecisionID]; dup {
			return fmt.Errorf("duplicate decision_id %q", d.DecisionID)
		}
		seenDecisionID[d.DecisionID] = struct{}{}
		if err := validateComponentDecision(d, b.Request, b.Catalog, b.Evidence); err != nil {
			return fmt.Errorf("decisions[%d] (%s): %w", i, d.DecisionID, err)
		}
	}
	byBehavior := map[string][]ComponentDecision{}
	for _, d := range b.Decisions {
		byBehavior[d.BehaviorID] = append(byBehavior[d.BehaviorID], d)
	}
	for _, need := range b.Request.Behaviors {
		list := byBehavior[need.BehaviorID]
		if need.Required && len(list) == 0 {
			return fmt.Errorf("required behavior %q has no decision", need.BehaviorID)
		}
		if len(list) > 1 {
			return fmt.Errorf("behavior %q has duplicate decisions", need.BehaviorID)
		}
	}
	for behaviorID := range byBehavior {
		if !behaviorInRequest(b.Request, behaviorID) {
			return fmt.Errorf("extra decision for unknown behavior %q", behaviorID)
		}
	}
	if err := validateApplicationManifest(b.Manifest, b.Request, b.Decisions); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if err := validateBindingCatalogLookups(b.Manifest.Bindings, b.Catalog); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	return nil
}

func checkOptionalContentSHA256(supplied string, hashFn func() (string, error)) error {
	if supplied == "" {
		return nil
	}
	if !isSHA256Hex(supplied) {
		return fmt.Errorf("content_sha256 must be a 64-hex digest when supplied")
	}
	got, err := hashFn()
	if err != nil {
		return err
	}
	if supplied != got {
		return fmt.Errorf("content_sha256 mismatch")
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
