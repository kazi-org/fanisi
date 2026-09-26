# Fanisi composition contract — revision 1.2 (as built)

This document replaces the superseded implementation sketches in revision 1.1. It records the implemented seams; it does not activate optional providers or peer-product adapters. [Implementation plan](../plan.md), [RFC](../rfc/0001-amsl-composition.md), [ADR](../adr/0001-amsl-composition-and-model-selection.md).

## Authoritative records and ownership

The exact Go fields and JSON names live in [compose_types.go](../../compose_types.go). All records use composition schema version 1, separate from the native task schema. Shared records and canonical hashes belong to F0; L1–L3 never declare substitute copies. L4 owns integration changes after handoff.

| Record | Contract |
|---|---|
| `CatalogIndex`, `CatalogRecord` | Offline catalog, source pin, immutable implementation references and descriptive maturity metadata. No promotion mutation API. |
| `CompositionRequest`, `BehaviorNeed`, `CompositionBudget` | Parent/request IDs, full requirements and behavior contracts, actual product revision, catalog pin, workspace, read/write/protected scopes, authority, deadline and admission limits. |
| `EvidenceRef` | Registry ID, A/B/C class, explicitly classified product IDs, implementation/mature implementation IDs and source references. Operator-attested structure, not independently established adoption. |
| `ComponentDecision`, `DecisionProvenance` | One behavior's verdict, explanation/evidence, candidates and provenance. Executable provenance is operator/import; shadow results cannot satisfy a binding directly. |
| `ApplicationManifest`, `Binding`, `ManifestEdge` | Exact request/decision hashes, explicit behavior targets, binding kinds and dependency edges; delivery/state/migration/secret-reference metadata. |
| `ImplementationRequest` | Parent/attempt/owner/fence identity; request/manifest hashes; exact workspace/scope; verifier/delegate argv; output, reservation, deadline, duration, explicit environment names and idempotency key. |
| `AttemptResult`, `UsageKnown` | Immutable candidate/evidence hashes, verifier outcome, attempt state, unresolved-effects flag, error and independently known/unknown usage. |
| `JournalEvent`, `CompositionReview` | Append-only transition/review facts. Review points at the immutable result hash and never rewrites that result. |
| `FiniteChoiceRequest`, `FiniteChoiceResult` | Complete question/state/evidence and a closed choice set including abstention; result selected/reason IDs, confidence and backend provenance. Advisory only. |
| `CompositionBundle` | Request, catalog, trusted evidence registry, component decisions and manifest validated together before execution. |

## Hashing and input rules

`canonicalCompositionJSON` sorts object keys and removes insignificant whitespace. Arrays retain order. Numbers retain their JSON representation without conversion through float64. Only the top-level record's own `content_sha256` is omitted; nested/link hashes remain. Values are not mutated. Typed hash functions operate on the canonical record representation, including zero-value fields emitted by its JSON tags.

Catalog `Pin` is SHA-256 of the exact source bytes; moving an unchanged catalog does not alter that pin. `pinCatalog` is the normalized-body helper and excludes its local root/pin fields; it is not a substitute for a supplied source pin.

At the CLI, the primary file path is resolved from the caller's working directory. Related artifact flags resolve beside that primary file. Request workspace and implementation workspace/output fields resolve beside their owning JSON file and are canonicalized before typed hashing. Scope paths remain exact workspace-relative paths. Command arguments are opaque; only argv[0] is resolved for execution. Bare argv[0] uses ordinary PATH/`LookPath` semantics and never prefers a same-named workspace file. Workspace scripts must use an explicit `./script` or absolute workspace path (symlink-safe under the workspace root). External/PATH installation symlinks remain allowed. Authors use explicit paths when an argument refers to a file outside the child workspace. `compose hash` uses the same loaders/conventions as validation and execution.

Composition input readers reject unknown fields, recursively duplicate object keys, trailing JSON values, unsupported schema versions, oversized files and special files. General executable JSON is capped at 256 KiB; catalogs at 8 MiB; finite results at 64 KiB. No truncation of mandatory request text. Artifact outputs use exclusive creation and refuse overwrite. Native readers/commands keep their existing semantics.

## Catalog and policy API (L1)

```go
loadCatalog(path string) (CatalogIndex, error)
pinCatalog(idx CatalogIndex) (string, error)
lookupCatalog(idx CatalogIndex, id, revision string) (CatalogRecord, error)
listCatalog(idx CatalogIndex, family string) []CatalogRecord
validateEvidenceForVerdict(v Verdict, refs []EvidenceRef, known map[string]EvidenceRef) error
validateCompositionRequest(r CompositionRequest) error
validateComponentDecision(d ComponentDecision, req CompositionRequest, cat CatalogIndex, evidence map[string]EvidenceRef) error
validateApplicationManifest(m ApplicationManifest, req CompositionRequest, decisions []ComponentDecision) error
validateDependencyDAG(behaviors []string, edges []ManifestEdge) error
validateCompositionBundle(b CompositionBundle) error
```

The catalog loader accepts real AMSL `{schema_version, capabilities}` and the documented normalized record form. Per-capability schema versions are checked. Records with no immutable implementation revision remain inspection-only. Executable references use exact 40/64-hex revisions and must exist in the supplied pinned catalog.

`reuse_reference`, `adapt_local`, `product_local`, `propose_amsl`, and `defer` are distinct. Required behaviors need concrete supported bindings; a proposal/deferral alone cannot fulfill them. Planned local targets permit implementation work, not a claim that working code already exists. A verified attempt and subsequent review are separate from the manifest's plan.

All supplied candidates and binding catalog references are checked, including local/fallback decisions. Partial catalog pairs, unknown IDs/revisions, wrong behavior/decision links, missing required bindings, stale hashes and DAG cycles fail. Required scopes are exact-file scopes; protected paths cannot overlap/nest writes. Parent symlinks must be checked even if a write leaf does not yet exist.

A/B/C references resolve against the operator registry. A needs two distinct real products and two implementations; B needs two real products and one implementation; C needs a mature implementation and two real products. Source references are required. Fixtures/worktrees/hypothetical products do not count. Fanisi checks this structure; the operator remains responsible for its truth and no promotion occurs automatically.

## Execution and journal API (L2)

```go
openJournal(dir string) (*Journal, error)
(*Journal).lock() (unlock func(), err error)
(*Journal).appendEvent(e JournalEvent) error
(*Journal).reserveBudget(parentID string, budget CompositionBudget, usd float64) error
(*Journal).loadAttempt(id string) (ImplementationRequest, AttemptResult, error)
transitionAllowed(from, to AttemptState) bool
runDelegate(ctx context.Context, cfg DelegateConfig) (exitCode int, usage UsageKnown, err error)
runIndependentVerifier(ctx context.Context, workspace string, verify []string, writePaths, protected []string) (AttemptResult, error)
dispatchComposition(ctx context.Context, b CompositionBundle, impl ImplementationRequest, journalDir string) (AttemptResult, error)
requestCompositionCancel(journalDir, attemptID, fence string) error
compositionStatus(journalDir, attemptID string) ([]AttemptResult, error)
recordCompositionReview(journalDir string, r CompositionReview) error
```

Journal directories contain append-only events, attempts, parent budgets/reservations and idempotency records. Record creation is exclusive and durable. Short journal transactions use bounded contention waits without stale-lock deletion. An independent workspace lease spans admission snapshots through finalization; it does not block status or cancellation transactions for the duration of a subprocess.

Freeze parent budgets on first use. Persist a conservative reservation before spawn; all reservations remain counted after failure, cancellation or unknown usage. Enforce attempt count and estimated-USD admission limits. This is not a receipt-based provider spending cap; arbitrary external delegates must implement their own provider budget enforcement. Unknown usage remains nil/false.

An idempotency key binds an exact implementation payload. The durable idempotency filename is a full-key digest (not a lossy punctuation collapse). Identical replay returns observed durable state without executing again, even after deadlines expire. A changed payload conflicts. Crash/incomplete state is not permission to retry; inspect it and reconcile externally. A new attempt requires an explicit operator decision, available budget and an appropriate clean workspace.

Before execution, require actual clean Git HEAD to equal ProductRevision. Capture read-only/protected/candidate hashes and Git HEAD/index. Resolve and freeze verifier argv[0] before the delegate runs; an in-workspace verifier executable must already be a protected input or have its hash frozen into the integrity snapshot. Reject changed protected or undeclared paths before executing the verifier. Recheck after verification and before human acceptance. Candidate success is `verified_pending_review`; no automatic human acceptance. Snapshot hashing opens declared paths with non-blocking, no-follow semantics and refuses nonregular files (including FIFOs) without hanging.

Delegate and verifier share a bounded attempt context: earliest caller deadline, request deadline, implementation deadline or duration. Cancellation covers verification and final checks. Cancel/timeout/incomplete work stays `blocked_uncertain`. Cancellation records are fence-bound; only the live owning dispatcher cancels its owned process group. Never kill a persisted PID. Kill remaining children in the owned group after the parent exits, including children holding output pipes open.

Use a minimal environment plus only explicit operator `env_names`; credential forwarding is an explicit trust decision, not implicit inheritance. Do not serialize environment values. Verifiers receive controlled HOME/Go cache locations. Raw child output is bounded but an explicitly trusted child can print a secret; Fanisi does not promise general-purpose output redaction.

Results are immutable. Status overlays later review events while preserving the referenced original result hash. Review evaluates event-derived current state, rejects repeated/reversed terminal reviews, and verifies exact current candidate/integrity evidence. Agent review can reject but cannot accept. Human attribution is a trusted local CLI invocation, not remote authenticated identity.

### State rules

- Proposed → validated → dispatched → running is the normal admission path.
- Resolved failure → `failed_terminal`; unresolved/canceled/timed-out work → `blocked_uncertain`.
- Successful independent verification → `verified_pending_review`.
- Pending review → accepted/rejected by the permitted reviewer; uncertain work may be explicitly rejected.
- Terminal accepted/rejected/failed states do not restart automatically. Reviews do not alter immutable result contents.

### Trust limitations

This is cooperative local execution, not an OS sandbox. Operator-selected programs can access the host. Ignored files, writes outside the repository, detached sessions and hostile attempts to defeat local locks require stronger external isolation. The local journal assumes trusted filesystem ownership. Product deployment, remote approval authentication and shared AMSL publication are outside v1.

## Finite decision API (L3)

```go
NewImportBackend(path string) DecisionBackend
NewCommandBackend(argv []string) DecisionBackend
validateFiniteChoiceRequest(r FiniteChoiceRequest) error
validateFiniteChoiceResult(r FiniteChoiceRequest, result FiniteChoiceResult) error
composeDecide(ctx context.Context, backend DecisionBackend, req FiniteChoiceRequest) (FiniteChoiceResult, error)
```

`DecisionBackend.Choose` receives a complete finite request and returns a finite result. Require 2–255 distinct choices including abstention, bounded full text, known evidence IDs, valid selected/reason IDs, consistent abstention and finite confidence in [0,1]. Backend provenance cannot be spoofed by imported/command output.

Command backend: direct argv, full request JSON on stdin, one result on stdout, 64 KiB stdout and 8 KiB stderr limits, default 30-second timeout, minimal environment, owned process-group cleanup. Process/provider errors are errors, not abstention or a silent fallback. Existing LLM wrappers can implement this protocol; raw Cursor output is not presumed compatible.

Only import and command backends are implemented. Any reserved GLM/Jev identifiers in shared types do not imply a callable backend. Jev qualification and default selection require the separate evaluation plan. Native Fanisi's pinned generation model/provider is unchanged.

## Actual CLI (L4)

```text
fanisi compose catalog --index PATH [--family NAME] [--id ID --revision REV] [--json]
fanisi compose hash --file PATH --kind request|decision|manifest|impl|result
fanisi compose validate --request PATH --decisions DIR --manifest PATH --catalog PATH --evidence PATH
fanisi compose decide --request PATH --import PATH [--out PATH]
fanisi compose decide --request PATH [--out PATH] --command -- EXECUTABLE ARG...
fanisi compose manifest --request PATH --decisions DIR --draft PATH --out PATH --catalog PATH --evidence PATH
fanisi compose dispatch --request PATH --catalog PATH --evidence PATH --decisions DIR --manifest PATH --impl PATH --journal DIR
fanisi compose status --journal DIR [--attempt ID]
fanisi compose cancel --journal DIR --attempt ID --fence TOKEN
fanisi compose review --journal DIR --attempt ID --decision accept|reject --reviewer NAME --kind human|agent --notes-file PATH --result-hash HEX
```

Put Fanisi flags before the command separator; subsequent command arguments belong to the child. `decide --request` means FiniteChoiceRequest. Its output is advisory and cannot alter a catalog, manifest or journal. Decisions are loaded from JSON files in a directory; evidence is an array keyed by unique IDs. `manifest --draft` requires explicit bindings/edges; Fanisi does not hallucinate integration recipes from prose. `hash` prints without modifying input. Failures use the existing main error path and exit 1.

The fresh [tutorial](../../examples/compose/README.md) is the executable usage example. The [validation record](../validation.md) distinguishes implemented behavior, tests actually run and deferred qualification. A passing verifier or test suite is not automatic acceptance by the user.
