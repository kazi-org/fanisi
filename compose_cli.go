package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const composeUsageText = `fanisi compose - offline composition catalog, validate, decide, dispatch

  fanisi compose catalog   --index PATH [--family NAME] [--id ID --revision REV] [--json]
  fanisi compose hash      --file PATH --kind request|decision|manifest|impl|result
  fanisi compose validate  --request PATH --decisions DIR --manifest PATH --catalog PATH --evidence PATH
  fanisi compose decide    --request PATH --import PATH|--command ARGV... [--out PATH]
  fanisi compose manifest  --request PATH --decisions DIR --draft PATH --out PATH --catalog PATH --evidence PATH
  fanisi compose dispatch  --request PATH --catalog PATH --evidence PATH --decisions DIR --manifest PATH --impl PATH --journal DIR
  fanisi compose status    --journal DIR [--attempt ID]
  fanisi compose cancel    --journal DIR --attempt ID --fence TOKEN
  fanisi compose review    --journal DIR --attempt ID --decision accept|reject --reviewer NAME --kind human|agent --notes-file PATH --result-hash HEX

Relative artifact paths resolve beside the primary config file for that command
(request, index, file, or journal). Workspace/output fields in JSON also resolve
beside their owning file. Read/write/protected scope paths stay workspace-relative.
Delegate/verifier argv is preserved byte-for-byte except argv[0] executable
resolution at execution (child cwd is workspace). Bare argv[0] uses PATH
(LookPath); workspace scripts use ./script or an absolute workspace path.
env_names lists opt-in names only (PATH/TMPDIR plus those names); credential
values are never logged or stored. Canonical hashing omits only each record's
own content_sha256. Hash prints the digest without modifying files. All written
artifacts use exclusive create. Failures exit 1 via mainContext.

Trust boundary: delegates and verifiers are trusted local operator selections, not
an OS sandbox. Explicit env_names (including credential names) are operator
allowlist entries the model cannot author. Human review identity is the local CLI
invoker attribution string. Remote adapters and provider/model qualification are
out of scope; native model defaults and runCommand credential stripping are
unchanged. Unknown usage stays nil/false. Cancel/timeout yields blocked_uncertain
with no automatic retry.
`

// composeManifestDraft is the explicit bindings/edges input for compose manifest.
// It must not invent targets from prose; operators author bindings and edges.
type composeManifestDraft struct {
	SchemaVersion  int            `json:"schema_version"`
	ManifestID     string         `json:"manifest_id"`
	Bindings       []Binding      `json:"bindings"`
	Edges          []ManifestEdge `json:"dependency_edges"`
	LocalPolicies  []string       `json:"local_policies,omitempty"`
	SecretRefs     []string       `json:"secret_refs,omitempty"`
	StateOwner     string         `json:"state_owner,omitempty"`
	MigrationNotes string         `json:"migration_notes,omitempty"`
	DeliveryRecipe string         `json:"delivery_recipe,omitempty"`
	RollbackLimits string         `json:"rollback_limits,omitempty"`
}

func composeCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(composeUsageText)
		return nil
	}
	switch args[0] {
	case "catalog":
		return composeCLICatalog(args[1:])
	case "hash":
		return composeCLIHash(args[1:])
	case "validate":
		return composeCLIValidate(args[1:])
	case "decide":
		return composeCLIDecide(ctx, args[1:])
	case "manifest":
		return composeCLIManifest(args[1:])
	case "dispatch":
		return composeCLIDispatch(ctx, args[1:])
	case "status":
		return composeCLIStatus(args[1:])
	case "cancel":
		return composeCLICancel(args[1:])
	case "review":
		return composeCLIReview(args[1:])
	default:
		return fmt.Errorf("unknown compose subcommand %q; run fanisi compose --help", args[0])
	}
}

func composeCLICatalog(args []string) error {
	fs := flag.NewFlagSet("compose catalog", flag.ContinueOnError)
	indexPath := fs.String("index", "", "offline AMSL or normalized catalog JSON")
	family := fs.String("family", "", "optional family/domain filter")
	id := fs.String("id", "", "optional capability id")
	rev := fs.String("revision", "", "optional implementation revision")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *indexPath == "" || fs.NArg() != 0 {
		return errors.New("compose catalog requires --index and no positional arguments")
	}
	if (*id == "") != (*rev == "") {
		return errors.New("compose catalog --id and --revision must be supplied together")
	}
	path, err := composeAbsPath(*indexPath)
	if err != nil {
		return err
	}
	idx, err := loadCatalog(path)
	if err != nil {
		return err
	}
	if *id != "" {
		rec, err := lookupCatalog(idx, *id, *rev)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(rec)
		}
		fmt.Printf("%s@%s family=%s maturity=%s\n", rec.ID, rec.Revision, rec.Family, rec.Maturity)
		return nil
	}
	recs := listCatalog(idx, *family)
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"schema_version": compositionSchemaVersion,
			"pin":            idx.Pin,
			"source_label":   idx.SourceLabel,
			"count":          len(recs),
			"records":        recs,
		})
	}
	fmt.Printf("pin=%s count=%d source=%s\n", idx.Pin, len(recs), idx.SourceLabel)
	for _, rec := range recs {
		fmt.Printf("%s@%s family=%s\n", rec.ID, rec.Revision, rec.Family)
	}
	return nil
}

func composeCLIHash(args []string) error {
	fs := flag.NewFlagSet("compose hash", flag.ContinueOnError)
	filePath := fs.String("file", "", "JSON record to hash")
	kind := fs.String("kind", "", "request|decision|manifest|impl|result")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *filePath == "" || *kind == "" || fs.NArg() != 0 {
		return errors.New("compose hash requires --file, --kind and no positional arguments")
	}
	path, err := composeAbsPath(*filePath)
	if err != nil {
		return err
	}
	var digest string
	switch *kind {
	case "request":
		r, loadErr := composeLoadRequest(path)
		if loadErr != nil {
			return loadErr
		}
		digest, err = hashCompositionRequest(r)
	case "decision":
		var d ComponentDecision
		if err := composeReadStrictJSONFile(path, &d, composeExecutableJSONMax); err != nil {
			return err
		}
		digest, err = hashComponentDecision(d)
	case "manifest":
		var m ApplicationManifest
		if err := composeReadStrictJSONFile(path, &m, composeExecutableJSONMax); err != nil {
			return err
		}
		digest, err = hashApplicationManifest(m)
	case "impl":
		r, loadErr := composeLoadImplementation(path)
		if loadErr != nil {
			return loadErr
		}
		digest, err = hashImplementationRequest(r)
	case "result":
		var r AttemptResult
		if err := composeReadStrictJSONFile(path, &r, composeExecutableJSONMax); err != nil {
			return err
		}
		digest, err = hashAttemptResult(r)
	default:
		return fmt.Errorf("unknown hash kind %q", *kind)
	}
	if err != nil {
		return err
	}
	fmt.Println(digest)
	return nil
}

func composeCLIValidate(args []string) error {
	fs := flag.NewFlagSet("compose validate", flag.ContinueOnError)
	requestPath := fs.String("request", "", "composition request JSON")
	decisionsPath := fs.String("decisions", "", "directory of decision JSON files")
	manifestPath := fs.String("manifest", "", "application manifest JSON")
	catalogPath := fs.String("catalog", "", "catalog index JSON")
	evidencePath := fs.String("evidence", "", "evidence JSON array")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *requestPath == "" || *decisionsPath == "" || *manifestPath == "" || *catalogPath == "" || *evidencePath == "" || fs.NArg() != 0 {
		return errors.New("compose validate requires --request --decisions --manifest --catalog --evidence and no positionals")
	}
	b, err := composeLoadBundleBeside(*requestPath, *catalogPath, *evidencePath, *decisionsPath, *manifestPath)
	if err != nil {
		return err
	}
	if err := validateCompositionBundle(b); err != nil {
		return err
	}
	fmt.Println("ok")
	return nil
}

func composeCLIDecide(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("compose decide", flag.ContinueOnError)
	requestPath := fs.String("request", "", "FiniteChoiceRequest JSON")
	importPath := fs.String("import", "", "FiniteChoiceResult JSON to import")
	useCommand := fs.Bool("command", false, "remaining argv is the decision command backend")
	outPath := fs.String("out", "", "exclusive-create result JSON path")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *requestPath == "" {
		return errors.New("compose decide requires --request")
	}
	if (*importPath == "") == !*useCommand {
		return errors.New("compose decide requires exactly one of --import PATH or --command ARGV...")
	}
	if *useCommand && fs.NArg() == 0 {
		return errors.New("compose decide --command requires argv after flags")
	}
	if !*useCommand && fs.NArg() != 0 {
		return errors.New("compose decide refuses unexpected positional arguments with --import")
	}
	reqFile, err := composeAbsPath(*requestPath)
	if err != nil {
		return err
	}
	var req FiniteChoiceRequest
	if err := composeReadStrictJSONFile(reqFile, &req, composeExecutableJSONMax); err != nil {
		return err
	}
	var backend DecisionBackend
	if *importPath != "" {
		imp, err := composeResolveBeside(reqFile, *importPath)
		if err != nil {
			return err
		}
		backend = NewImportBackend(imp)
	} else {
		backend = NewCommandBackend(append([]string{}, fs.Args()...))
	}
	result, err := composeDecide(ctx, backend, req)
	if err != nil {
		return err
	}
	if *outPath == "" {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	out, err := composeResolveBeside(reqFile, *outPath)
	if err != nil {
		return err
	}
	return composeWriteExclusiveJSON(out, result)
}

func composeCLIManifest(args []string) error {
	fs := flag.NewFlagSet("compose manifest", flag.ContinueOnError)
	requestPath := fs.String("request", "", "composition request JSON")
	decisionsPath := fs.String("decisions", "", "directory of decision JSON files")
	draftPath := fs.String("draft", "", "explicit bindings/edges draft JSON")
	outPath := fs.String("out", "", "exclusive-create manifest output")
	catalogPath := fs.String("catalog", "", "catalog index JSON")
	evidencePath := fs.String("evidence", "", "evidence JSON array")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *requestPath == "" || *decisionsPath == "" || *draftPath == "" || *outPath == "" || *catalogPath == "" || *evidencePath == "" || fs.NArg() != 0 {
		return errors.New("compose manifest requires --request --decisions --draft --out --catalog --evidence and no positionals")
	}
	reqFile, err := composeAbsPath(*requestPath)
	if err != nil {
		return err
	}
	req, err := composeLoadRequest(reqFile)
	if err != nil {
		return err
	}
	catFile, err := composeResolveBeside(reqFile, *catalogPath)
	if err != nil {
		return err
	}
	idx, err := loadCatalog(catFile)
	if err != nil {
		return err
	}
	evFile, err := composeResolveBeside(reqFile, *evidencePath)
	if err != nil {
		return err
	}
	evidence, err := composeLoadEvidence(evFile)
	if err != nil {
		return err
	}
	decDir, err := composeResolveBeside(reqFile, *decisionsPath)
	if err != nil {
		return err
	}
	decisions, err := composeLoadDecisionsDir(decDir)
	if err != nil {
		return err
	}
	draftFile, err := composeResolveBeside(reqFile, *draftPath)
	if err != nil {
		return err
	}
	var draft composeManifestDraft
	if err := composeReadStrictJSONFile(draftFile, &draft, composeExecutableJSONMax); err != nil {
		return err
	}
	man, err := composeBuildManifestFromDraft(req, decisions, draft)
	if err != nil {
		return err
	}
	b := CompositionBundle{Request: req, Catalog: idx, Evidence: evidence, Decisions: decisions, Manifest: man}
	if err := validateCompositionBundle(b); err != nil {
		return err
	}
	out, err := composeResolveBeside(reqFile, *outPath)
	if err != nil {
		return err
	}
	return composeWriteExclusiveJSON(out, man)
}

func composeCLIDispatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("compose dispatch", flag.ContinueOnError)
	requestPath := fs.String("request", "", "composition request JSON")
	catalogPath := fs.String("catalog", "", "catalog index JSON")
	evidencePath := fs.String("evidence", "", "evidence JSON array")
	decisionsPath := fs.String("decisions", "", "directory of decision JSON files")
	manifestPath := fs.String("manifest", "", "application manifest JSON")
	implPath := fs.String("impl", "", "implementation request JSON")
	journalDir := fs.String("journal", "", "attempt journal directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *requestPath == "" || *catalogPath == "" || *evidencePath == "" || *decisionsPath == "" || *manifestPath == "" || *implPath == "" || *journalDir == "" || fs.NArg() != 0 {
		return errors.New("compose dispatch requires --request --catalog --evidence --decisions --manifest --impl --journal and no positionals")
	}
	b, err := composeLoadBundleBeside(*requestPath, *catalogPath, *evidencePath, *decisionsPath, *manifestPath)
	if err != nil {
		return err
	}
	reqFile, err := composeAbsPath(*requestPath)
	if err != nil {
		return err
	}
	implFile, err := composeResolveBeside(reqFile, *implPath)
	if err != nil {
		return err
	}
	impl, err := composeLoadImplementation(implFile)
	if err != nil {
		return err
	}
	// Align workspace strings before validation/hash so request and impl match.
	if impl.Workspace != b.Request.Workspace {
		return errors.New("implementation workspace must match request workspace")
	}
	journal, err := composeResolveBeside(reqFile, *journalDir)
	if err != nil {
		return err
	}
	result, err := dispatchComposition(ctx, b, impl, journal)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(result); encErr != nil {
		return encErr
	}
	return err
}

func composeCLIStatus(args []string) error {
	fs := flag.NewFlagSet("compose status", flag.ContinueOnError)
	journalDir := fs.String("journal", "", "attempt journal directory")
	attemptID := fs.String("attempt", "", "optional attempt id")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *journalDir == "" || fs.NArg() != 0 {
		return errors.New("compose status requires --journal and no positional arguments")
	}
	journal, err := composeAbsPath(*journalDir)
	if err != nil {
		return err
	}
	results, err := compositionStatus(journal, *attemptID)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}

func composeCLICancel(args []string) error {
	fs := flag.NewFlagSet("compose cancel", flag.ContinueOnError)
	journalDir := fs.String("journal", "", "attempt journal directory")
	attemptID := fs.String("attempt", "", "attempt id")
	fence := fs.String("fence", "", "fence token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *journalDir == "" || *attemptID == "" || *fence == "" || fs.NArg() != 0 {
		return errors.New("compose cancel requires --journal --attempt --fence and no positionals")
	}
	journal, err := composeAbsPath(*journalDir)
	if err != nil {
		return err
	}
	return requestCompositionCancel(journal, *attemptID, *fence)
}

func composeCLIReview(args []string) error {
	fs := flag.NewFlagSet("compose review", flag.ContinueOnError)
	journalDir := fs.String("journal", "", "attempt journal directory")
	attemptID := fs.String("attempt", "", "attempt id")
	decision := fs.String("decision", "", "accept or reject")
	reviewer := fs.String("reviewer", "", "local operator attribution string")
	kind := fs.String("kind", "", "human or agent")
	notesFile := fs.String("notes-file", "", "review notes file")
	resultHash := fs.String("result-hash", "", "immutable attempt result content_sha256")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *journalDir == "" || *attemptID == "" || *decision == "" || *reviewer == "" || *kind == "" || *notesFile == "" || *resultHash == "" || fs.NArg() != 0 {
		return errors.New("compose review requires --journal --attempt --decision --reviewer --kind --notes-file --result-hash and no positionals")
	}
	journal, err := composeAbsPath(*journalDir)
	if err != nil {
		return err
	}
	notesPath, err := composeResolveBeside(journal, *notesFile)
	if err != nil {
		return err
	}
	// Same substantive notes cap as recordCompositionReview / ledger (16000).
	notes, err := composeReadBoundedRegularFile(notesPath, 16000)
	if err != nil {
		return err
	}
	return recordCompositionReview(journal, CompositionReview{
		SchemaVersion: compositionSchemaVersion,
		AttemptID:     *attemptID,
		Decision:      *decision,
		Reviewer:      *reviewer,
		Kind:          *kind,
		Notes:         string(notes),
		ResultHash:    *resultHash,
		At:            time.Now().UTC(),
	})
}

func composeLoadBundleBeside(requestPath, catalogPath, evidencePath, decisionsPath, manifestPath string) (CompositionBundle, error) {
	var empty CompositionBundle
	reqFile, err := composeAbsPath(requestPath)
	if err != nil {
		return empty, err
	}
	req, err := composeLoadRequest(reqFile)
	if err != nil {
		return empty, err
	}
	catFile, err := composeResolveBeside(reqFile, catalogPath)
	if err != nil {
		return empty, err
	}
	idx, err := loadCatalog(catFile)
	if err != nil {
		return empty, err
	}
	evFile, err := composeResolveBeside(reqFile, evidencePath)
	if err != nil {
		return empty, err
	}
	evidence, err := composeLoadEvidence(evFile)
	if err != nil {
		return empty, err
	}
	decDir, err := composeResolveBeside(reqFile, decisionsPath)
	if err != nil {
		return empty, err
	}
	decisions, err := composeLoadDecisionsDir(decDir)
	if err != nil {
		return empty, err
	}
	manFile, err := composeResolveBeside(reqFile, manifestPath)
	if err != nil {
		return empty, err
	}
	var man ApplicationManifest
	if err := composeReadStrictJSONFile(manFile, &man, composeExecutableJSONMax); err != nil {
		return empty, err
	}
	return CompositionBundle{
		Request:   req,
		Catalog:   idx,
		Evidence:  evidence,
		Decisions: decisions,
		Manifest:  man,
	}, nil
}

func composeLoadRequest(path string) (CompositionRequest, error) {
	var req CompositionRequest
	if err := composeReadStrictJSONFile(path, &req, composeExecutableJSONMax); err != nil {
		return req, err
	}
	if req.Workspace != "" && !filepath.IsAbs(req.Workspace) {
		resolved, err := composeResolveBeside(path, req.Workspace)
		if err != nil {
			return req, err
		}
		req.Workspace = resolved
	}
	if req.Workspace != "" {
		canon, err := composeCanonicalRoot(req.Workspace)
		if err != nil {
			return req, err
		}
		req.Workspace = canon
	}
	return req, nil
}

func composeLoadImplementation(path string) (ImplementationRequest, error) {
	var impl ImplementationRequest
	if err := composeReadStrictJSONFile(path, &impl, composeExecutableJSONMax); err != nil {
		return impl, err
	}
	base := path
	for _, p := range []*string{&impl.Workspace, &impl.OutputDir} {
		if *p != "" && !filepath.IsAbs(*p) {
			resolved, err := composeResolveBeside(base, *p)
			if err != nil {
				return impl, err
			}
			*p = resolved
		}
	}
	if impl.Workspace != "" {
		canon, err := composeCanonicalRoot(impl.Workspace)
		if err != nil {
			return impl, err
		}
		impl.Workspace = canon
	}
	if impl.OutputDir != "" {
		canon, err := composeCanonicalRoot(impl.OutputDir)
		if err != nil {
			return impl, err
		}
		impl.OutputDir = canon
	}
	// DelegateArgv and VerifyCommand are preserved byte-for-byte. Only argv[0]
	// is resolved at execution time (see composeResolveAbsoluteArgv). Bare
	// argv[0] uses PATH; explicit ./script or absolute workspace paths select
	// workspace executables. Relative script/data arguments resolve against the
	// child cwd (workspace) or must be authored as absolute paths by the operator.
	return impl, nil
}

func composeLoadEvidence(path string) (map[string]EvidenceRef, error) {
	var list []EvidenceRef
	if err := composeReadStrictJSONFile(path, &list, composeExecutableJSONMax); err != nil {
		return nil, err
	}
	out := make(map[string]EvidenceRef, len(list))
	for i, ev := range list {
		if strings.TrimSpace(ev.ID) == "" {
			return nil, fmt.Errorf("evidence[%d] missing id", i)
		}
		if _, dup := out[ev.ID]; dup {
			return nil, fmt.Errorf("duplicate evidence id %q", ev.ID)
		}
		out[ev.ID] = ev
	}
	return out, nil
}

func composeLoadDecisionsDir(dir string) ([]ComponentDecision, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []ComponentDecision
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var d ComponentDecision
		if err := composeReadStrictJSONFile(filepath.Join(dir, e.Name()), &d, composeExecutableJSONMax); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no decision JSON files in %s", dir)
	}
	return out, nil
}

func composeBuildManifestFromDraft(req CompositionRequest, decisions []ComponentDecision, draft composeManifestDraft) (ApplicationManifest, error) {
	if draft.SchemaVersion == 0 {
		draft.SchemaVersion = compositionSchemaVersion
	}
	if draft.SchemaVersion != compositionSchemaVersion {
		return ApplicationManifest{}, fmt.Errorf("draft schema_version must be %d", compositionSchemaVersion)
	}
	if !simpleID(draft.ManifestID) {
		return ApplicationManifest{}, errors.New("draft manifest_id must be a simple id")
	}
	reqHash, err := hashCompositionRequest(req)
	if err != nil {
		return ApplicationManifest{}, err
	}
	var hashes []string
	seen := map[string]struct{}{}
	for _, d := range decisions {
		h, err := hashComponentDecision(d)
		if err != nil {
			return ApplicationManifest{}, err
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		hashes = append(hashes, h)
	}
	man := ApplicationManifest{
		SchemaVersion:  draft.SchemaVersion,
		ManifestID:     draft.ManifestID,
		RequestHash:    reqHash,
		DecisionHashes: hashes,
		Bindings:       append([]Binding{}, draft.Bindings...),
		Edges:          append([]ManifestEdge{}, draft.Edges...),
		LocalPolicies:  append([]string{}, draft.LocalPolicies...),
		SecretRefs:     append([]string{}, draft.SecretRefs...),
		StateOwner:     draft.StateOwner,
		MigrationNotes: draft.MigrationNotes,
		DeliveryRecipe: draft.DeliveryRecipe,
		RollbackLimits: draft.RollbackLimits,
	}
	digest, err := hashApplicationManifest(man)
	if err != nil {
		return ApplicationManifest{}, err
	}
	man.ContentSHA256 = digest
	return man, nil
}

func composeAbsPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	return filepath.Abs(p)
}

// composeResolveBeside resolves relative artifact paths beside primaryFile.
// Absolute paths are returned cleaned via Abs. Scope paths must not use this.
func composeResolveBeside(primaryFile, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(p) {
		return filepath.Abs(p)
	}
	base := filepath.Dir(primaryFile)
	return filepath.Abs(filepath.Join(base, p))
}

func composeWriteExclusiveJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(path, append(b, '\n'))
}
