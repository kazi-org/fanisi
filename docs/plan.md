# Fanisi composition v1 — implementation and qualification plan

Status: implementation completed and locally qualified on 2026-09-26 using headless Cursor `agent`. F0, L1–L4 and G0 are complete; user review remains pending. See [roadmap](roadmap.md) and [validation evidence](validation.md) for current outcomes. This is the executable build plan, not a claim of human acceptance.

Architecture: [RFC 0001](rfc/0001-amsl-composition.md), [ADR 0001](adr/0001-amsl-composition-and-model-selection.md). Wire records and function signatures: [composition contract](plans/fanisi-composition-contract.md). Downstream product experiment: [voice app](plans/voice-app-experiment.md). Optional model qualification: [decision backend evaluation](plans/decision-backend-evaluation.md).

## 1. Deliverable and boundaries

Build an offline-default, AMSL-aware composition path in the existing Go executable. Fanisi reads a pinned catalog, checks component decisions and an explicit application manifest, delegates a bounded implementation attempt, independently verifies the candidate, and records reviewable evidence. Reuse, adapt-local, product-local, propose-AMSL, and defer are distinct decisions. A recommendation alone never supplies a required runtime behavior or promotes an AMSL capability.

The v1 host integration is versioned JSON and a local journal. Zatiti can supply goals and receive status; Foundry can consume delivery records; Kazi can own an explicitly delegated implementation loop; Griffon can be a selected runtime capability. Those are integration boundaries, not claims that live adapters already exist.

Native Fanisi generation remains pinned to `z-ai/glm-5.3-flash` / Z.AI. This build supplies imported and command-backed finite-choice decisions. It does not activate Jev, add provider stubs, deploy an app, publish an AMSL package, or automatically accept a candidate. The iOS voice-note preview plus a subsequent change remains the separate proving experiment.

Use only the standard library and root Go package. Preserve the intentionally failing nested `examples/go-fix` module. Keep all default tests offline. Arbitrary operator-selected delegates/verifiers are trusted executables; process groups and scope detection are not an OS sandbox.

## 2. Parallel work graph and ownership

```text
F0: freeze shared records, hashes and contract
  ├── L1: catalog, evidence policy, bundle validation
  ├── L2: journal, delegate, verification, review
  └── L3: finite-choice import/command backend
          ↓ join all three lanes
L4: integration corrections, CLI, end-to-end tests, tutorial, docs
          ↓
G0: independent review, full vet/race, fresh tutorial, source handoff
```

| Lane | Exclusive files while parallel | Dependencies and handoff |
|---|---|---|
| F0 | `compose_types.go`, `compose_hash.go`, hash tests; shared contract | Must land before workers start. Shared structs are never duplicated or stubbed. |
| L1 | `compose_catalog.go`, `compose_policy.go`, `compose_validate.go`, their tests; catalog/evidence fixtures | Reads F0. Provides pure policy/catalog/bundle APIs. |
| L2 | `compose_journal.go`, `compose_delegate.go`, `compose_dispatch.go`, `compose_review.go`, their tests | Reads F0; calls real L1 bundle validator after integration. No temporary validator stubs. |
| L3 | `compose_backend.go`, `compose_decide.go`, their tests | Reads F0. Returns advisory finite results; does not mutate manifests/catalogs or dispatch. |
| L4 | `main.go` compose routing/help only; `compose_cli.go`, CLI tests, composition JSON reader, examples, README and docs | Takes ownership of integrated files after lane handoff and fixes cross-lane defects. |
| G0 | Validation record, roadmap, final plan status | Serial coordinator qualification against final source. |

Coordinator owns integration and shared documentation. Workers use isolated worktrees. They do not edit another lane's files, change shared signatures, run full builds in parallel, or declare final verification. A worker handoff lists files, exact APIs, tests actually executed, and known defects. Read-only reviews can run concurrently with coding; source mutations and tests remain coordinated.

## 3. Foundation: F0

- F0.1 Define schema-versioned catalog, request, evidence, component decision, manifest, implementation, attempt, review, journal event, finite-choice request/result, and usage records.
- F0.2 Separate admission estimates from provider receipts. Unknown cost/tokens remain unknown, never zero inferred from success or byte counts.
- F0.3 Canonicalize object-key order, preserve arrays and number precision, exclude only the record's own top-level `content_sha256`, and leave input values untouched.
- F0.4 Publish typed request/decision/manifest/implementation/result hash functions. Test ordering, own-hash omission, nested hashes, number precision, and mutation resistance.
- F0.5 Freeze cross-lane APIs in the contract. Resolve path, evidence, cancellation, budget, and human-review semantics before parallel coding.
- F0.6 Establish baseline vet/race results and preserve existing user work. Foundation implementation landed as local commit `63645fc`.

Exit: shared code compiles, hash tests execute and pass, and all lanes use the same records.

## 4. Catalog and policy: L1

- L1.1 Parse the actual AMSL `schema_version`/`capabilities` format, including each capability's schema version. Support the documented normalized catalog format separately.
- L1.2 Pin exact catalog source bytes; keep the pin independent of checkout location. Retain records without implementation revisions for inspection only.
- L1.3 List/filter records and look up exact immutable revisions. Reject unsupported schemas, duplicate entries, mutable executable revisions, and unknown references.
- L1.4 Validate complete requirements, behavior IDs/contracts, authority, deadlines, budget numbers, scopes, and optional content hashes. Missing mandatory fields fail before dispatch.
- L1.5 Check every scope path, including a nonexistent write leaf, for parent symlinks. Frozen reads/protected inputs must be regular files. Protected and write paths may not overlap or nest.
- L1.6 Resolve evidence IDs against the operator's evidence registry. Do not trust model-supplied evidence bodies or infer product reality from ID spelling.
- L1.7 Enforce A/B/C structural eligibility: A requires two real products and two implementations; B requires two real products and one implementation; C requires a mature implementation used by two real products. Require source references. Fixture/worktree/hypothetical products do not count.
- L1.8 Treat structural eligibility as operator-attested evidence, not proof of maturity. No API automatically changes catalog maturity or publishes a component.
- L1.9 Validate every supplied candidate/reference against the pinned catalog, regardless of verdict. Reject partial catalog ID/revision pairs and hidden unknown references in local bindings.
- L1.10 Link each manifest binding to its behavior and exact decision hash; link the manifest to the exact request and decision set. Require concrete planned/local/fallback targets where appropriate.
- L1.11 Reject missing required bindings, cycles, unknown graph nodes, incompatible verdict/binding combinations, and direct shadow decisions. A proposed capability cannot be the only fulfillment of a required behavior.
- L1.12 Test every verdict, evidence class, stale pin/hash, wrong schema, missing leaf symlink, injected catalog reference, DAG cycle/diamond, and required fallback rule. Include a read-only smoke of the real local AMSL catalog.

Exit: catalog/policy/validation suites pass on the joined code without network access or fabricated real-world evidence.

## 5. Journal and bounded execution: L2

- L2.1 Create a local append-only journal with exclusive record creation, durable writes, monotonic events, immutable attempts/results, and separate review events.
- L2.2 Use short transaction locks. Bound waits for active transactions; never remove a stale lock automatically. Status/cancellation remain available while a delegate runs.
- L2.3 Freeze each parent budget on first admission. Reserve before spawning; count all reservations and attempts, including failed and uncertain work. Never silently refund or retry.
- L2.4 Bind attempts to parent, request/manifest hashes, owner, fence, scope, deadline, and idempotency key. Same key plus changed payload fails. Same key plus identical payload returns stored status without spawning again, including after deadline expiry.
- L2.5 Acquire an exclusive workspace lease before snapshots and keep it through verification/finalization. Require the actual clean Git HEAD to match the requested product revision.
- L2.6 Snapshot frozen reads, protected inputs, candidate paths, HEAD, and index. Check tracked/untracked scope outside the declared writes; document the ignored-file and external-host limitations.
- L2.7 Execute argv directly with a minimal environment. Preserve literal arguments. Only explicit operator environment names may be forwarded; never dump environment values into JSON or logs.
- L2.8 Apply one attempt context bounded by request deadline, implementation deadline, duration, and caller context. Reject a duration above the parent limit.
- L2.9 Own the process group; kill its remaining children after parent exit and on cancellation. Bound captured output. Do not kill a PID loaded from an old journal record.
- L2.10 Check forbidden/protected changes before invoking the verifier so a modified acceptance script cannot run. Run an independent operator-selected verifier with controlled cache/home settings.
- L2.11 Keep cancellation/deadline handling active through verification and final checks. Timeouts, cancellation, incomplete/crashed work and unresolved effects become `blocked_uncertain`.
- L2.12 Repeat scope, protected/read-only, HEAD/index, and candidate checks after verification. Success is `verified_pending_review`; a delegate's success claim alone is insufficient.
- L2.13 Cancellation appends a fence-bound request; only the live owning dispatcher cancels its group. Stale fences fail. Never imply that a crashed attempt can be safely resumed automatically.
- L2.14 Review checks the event-derived latest state under lock, the immutable result hash, and current candidate/integrity evidence. Agent reviews cannot accept. Terminal reviews cannot be repeated or reversed.
- L2.15 Keep human review attribution honest: this is a trusted local CLI invocation, not remote identity authentication.
- L2.16 Test real subprocess success/failure, child cleanup, delegate/verifier timeout, cancellation contention, concurrent workspace ownership, poisoned verifier, undeclared verifier writes, post-verification drift, stale fences, unknown usage, budget exhaustion, idempotent replay and terminal review finality.

Exit: joined journal/delegate/dispatch/review suites pass through real process boundaries. No automatic retry, acceptance, deployment, or credential inheritance.

## 6. Finite decisions: L3

- L3.1 Validate complete question/state/evidence text and closed candidate IDs. Require 2–255 choices including explicit abstention; cap the request at 256 KiB without trimming mandatory text.
- L3.2 Import a bounded regular-file result, rejecting special files, unknown/duplicate fields and trailing values.
- L3.3 Implement the command protocol: complete request JSON on stdin, one result on stdout, bounded stdout/stderr, default timeout, minimal environment and owned process-group cleanup.
- L3.4 Reject unknown selected/reason IDs, non-finite/out-of-range confidence, inconsistent abstention, and spoofed backend provenance. Provider/process errors are errors, not silent abstention or fallback.
- L3.5 Keep results advisory. Explicitly authored/imported ComponentDecision records still pass the full policy and manifest validation path.
- L3.6 Test valid import/command behavior, invalid requests before execution, oversized/special inputs, malformed/duplicate output, timeout/children, provenance spoofing, and unchanged native model pin.
- L3.7 Leave live GLM/Jev adapters and automatic selection to the separate model evaluation plan. Do not add success-shaped placeholders.

Exit: finite-decision tests pass offline. The bounded import regression must fail if the old unbounded read is restored.

## 7. CLI, integration and operator workflow: L4

- L4.1 Expose `catalog`, `hash`, `validate`, `decide`, `manifest`, `dispatch`, `status`, `cancel`, and `review` under `fanisi compose` without changing native commands.
- L4.2 Reject missing/extra flags, unknown fields, duplicate JSON keys, trailing values, oversized/special files and incompatible backend options. Keep composition readers separate from native behavior.
- L4.3 Define one consistent path/hash convention: artifact references resolve beside their primary configuration; workspace/output roots are canonicalized; scopes remain workspace-relative; command arguments remain opaque. `hash` and execution must agree.
- L4.4 `validate` and `dispatch` load the complete request/catalog/evidence/decision/manifest bundle. Dispatch additionally validates the implementation record before creating an attempt.
- L4.5 `decide` accepts a FiniteChoiceRequest and import/command backend; it writes only a finite result. `manifest` requires explicit `--draft` bindings/edges and never synthesizes integrations from prose.
- L4.6 Use exclusive artifact output creation; never overwrite inputs. Route failures through existing CLI exit status 1.
- L4.7 Exercise `mainContext` end to end: hash/validate, finite decision, draft manifest, real local dispatch, pending status, agent-accept rejection, human review, stale evidence rejection, idempotency, poisoned verifier and cancellation.
- L4.8 Ship a runnable offline tutorial generating fresh Git HEADs, hashes and deadlines in an absent/empty target directory. Reject nonempty targets before writes. Mark synthetic evidence explicitly.
- L4.9 Document actual commands, JSON contracts, trust boundaries, crash recovery, unknown accounting, environmental opt-in, platform behavior, and unsupported live integrations.
- L4.10 Remove contradictory planning sketches and keep shared ownership and deferred work explicit. Update the roadmap and validation record with actual results, not worker estimates.

Exit: command-line suites and the fresh tutorial succeed; review findings are resolved or explicitly documented as non-blocking limitations.

## 8. Serial qualification and handoff: G0

1. Confirm no worker is still mutating the integration tree. Inspect file ownership and preserve all existing root work/research.
2. Check machine load. For multi-package heavy commands obtain the shared build lease; this project currently has one root Go package and a separately excluded demonstration module.
3. Require clean `gofmt` output, `go vet ./...`, and `go test -race -count=1 -json ./...`. Record named tests, executed counts, failures and skips. No live credentials/API calls.
4. Check the existing macOS/Linux CI workflow includes the root suite. Do not claim Linux execution merely because CI is configured for it.
5. Build the actual executable and run the tutorial in a newly created empty directory. Confirm the resulting state and immutable result hash. Run real local AMSL catalog inspection read-only.
6. Exercise invalid CLI exit behavior and nonempty tutorial rejection. Preserve the intentionally red nested example and unchanged module dependencies/model/provider pin.
7. Re-run only tests justified by changes or unresolved failures. Do not call green an unexecuted, skipped or cached-only suite.
8. Record fixes and material limitations. Integrate the tested source into the original workspace without resetting, cleaning or overwriting other work. Compare source hashes to the verified tree.
9. Release only this session's resource claim using its original SHA. Leave code and records ready for user review; no push, shared publication, deployment or automatic acceptance.

## 9. Cursor execution recipe

Use the installed binary `agent` (headless Cursor). The CLI has no `--name` flag; put the lane's name and exact ownership in its prompt. Preserve the configured model selection unless explicitly changed.

```sh
agent --print --output-format stream-json --force --trust \
  --workspace /absolute/path/to/isolated-worktree "$(cat /path/to/lane-prompt.txt)" \
  > /path/to/lane-log.jsonl 2>&1
```

Each prompt includes this plan, frozen contract, owned files, constraints, acceptance tests and handoff format. Supply prompts as literal argv from a runner where practical. Do not interpolate credential values. Use built-in source tools for these lanes; stalled graph/plugin calls were avoided with an explicit direct-file instruction. Record the chat ID so a completed lane can be resumed for a bounded fix pass. Stop only processes positively identified as belonging to this session.

Run F0 once, then three independent L1–L3 worktrees. Join real implementations before testing cross-lane behavior. Run L4 after handoff; it owns integration corrections. G0 is coordinator-only. Local execution logs and prompts live under ignored `.fanisi/implementation/`; durable decisions belong in the RFC/ADR/plan/validation records.

## 10. Following this build

The next decision gate is the seven-day Zatiti voice-note preview plus one subsequent change, with the agreed Codex allowance and lower-cost assistance. Measure user supervision, correctness, elapsed time, known provider cost, unknown accounting, and maintenance effort. Compare bounded replacement against targeted edits on equivalent requirements; do not infer superiority from greenfield generation speed alone.

Live peer-product adapters, remote review authentication, stronger sandboxing, AMSL publication, and a production decision-model choice require their own concrete contracts and qualification. Fanisi v1 supplies a reviewable local composition/delegation boundary; it does not by itself establish end-to-end autonomous app delivery.
