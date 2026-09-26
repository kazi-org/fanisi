# RFC 0001: AMSL-aware application composition

Status: **Proposed**, 2026-09-26. This document specifies a direction and review gates; it does not authorize implementation, deployment, shared-library promotion or model changes. [ADR](../adr/0001-amsl-composition-and-model-selection.md), [plan](../plan.md), [decision-backend evaluation](../plans/decision-backend-evaluation.md).

## Outcome and scope

Fanisi helps an existing coding workflow decide what to reuse, what to implement locally and what evidence warrants a shared AMSL capability, then produces bounded implementation requests and verifies the composition. Codex/Claude remain code producers. The initial product outcome is a deployed physical-iPhone asynchronous Codex voice-note preview and one accepted maintenance change within seven days and the user's 5% weekly Codex allowance. Classification quality and product delivery are separate outcomes; neither implies the other.

Start with session-authored decisions, documented templates and explicit human checks. This is a supervised workflow, not machine enforcement or headless autonomy. Optional Fanisi validation/delegation code can replace those steps only after boundary tests pass. No new service, registry, database, scheduler or routing model is required.

## Current substrate and readiness

Fanisi already has explicit scope/budgets (`config.go`), complete mandatory packets and hashed optional context (`packet.go`), bounded execution/final verification (`run.go`), frozen worktree comparisons (`eval.go`) and hash-bound agent/human review (`ledger.go`). Reuse these mechanisms; do not rewrite its native loop. Filesystem scope is not an OS sandbox. Existing generation remains `z-ai/glm-5.3-flash` with Z.AI pin; current config has no model selector. Proposed types below are not existing CLI commands or APIs.

The inspected AMSL source has 41 catalog records, approximately 20 core Go families, 13 infrastructure packages and five delivery workflows, with uneven qualification. Catalog validation checks structure and recorded labels, not evidence authenticity or all admission criteria. Source/worklog freshness must be assessed at exact revisions. See [source investigation](<../../reports/AMSL implementation and Fanisi.md>).

Readiness refreshed 2026-09-26T09:33Z: Griffon now has an initial local identity/billing service, SDK and reference app, with real-provider and production gates pending; earlier research calling it proposal-only is historical. The supplied Foundry checkout still has no commits/source, which says nothing about other workspaces. Zatiti is implemented pre-release with external-worker support; live qualification remains required. [Freshness record](<../../research_notes/Fanisi composition RFC/freshness.md>).

## Ownership and dataflow

| Role | Owns | Excludes |
|---|---|---|
| Zatiti | Parent goal, authority envelope, cancellation, user-facing task status | Code-repair loop |
| Foundry logical role | Product build/release stages and artifact evidence | Competing parent task authority |
| Fanisi | Fit decisions, composition manifest, bounded requests, integration evidence | Fleet scheduling, credentials, automatic acceptance |
| Optional Kazi | One attempt's bounded convergence with a harness | Outer retries or replenishing parent budget |
| Codex/Claude | Assigned implementation and bounded repair | Lifecycle promotion or self-acceptance |
| Griffon, if needed/qualified | Shared runtime identity/billing consumed by products | Build orchestration |

Flow: goal → product recipe → capability decisions/manifest → implementation request → harness attempt → independent checks → review events → user acceptance. These roles may initially be templates/commands. Deployed apps keep running when builders stop; Griffon runtime dependence is explicit and optional. Kazi integrations, attempt lookup and cancellation are proposed/unverified until qualified; there is no assumed working adapter.

Exactly one owner controls retries for an attempt. With Kazi selected, Fanisi must not add an outer repair loop. Child shared-component work gets its own ID, repository owner and slice of the parent allowance, never fresh authority or recursive builder dispatch. Product workers cannot change AMSL source/catalog or protected tests merely to finish a task.

## Records and deterministic rejection

All records are schema-versioned and content-addressed. Revisions append immutable records with `supersedes` and contradictory evidence IDs; they do not overwrite history. A revision invalidates affected downstream validation/dispatch/verifier hashes. Failed-terminal attempts are immutable; an authorized retry creates a new attempt ID/fence under the same parent allocation after reviewing the failure reason, never rewriting the failed attempt. Narrative reasoning notes are separate from executable fields and carry author/model provenance; neither grants authority.

| Proposed type | Required information |
|---|---|
| CompositionRequest | Parent/request ID, immutable product/catalog revisions, full requirements, scope, protected checks, runtime constraints, authority reference, deadline and allocation |
| ComponentDecision | Behavior ID, candidate IDs/revisions/evidence IDs, verdict, guarantees/non-goals, policy boundary, A/B/C evidence if proposing admission, owner, compatibility/state implications, consumer checks, reasoning-note reference |
| ApplicationManifest | Decision/request hashes, implementation or planned local target/contract binding for each required behavior, dependency DAG/interfaces, local policies/adapters, config/secret references, state owner/migration/old-client rules, delivery recipe and rollback limits |
| ImplementationRequest | Manifest/base hashes, attempt ID/owner/fencing token, exact write scope, protected verifier, pinned executor/model/provider, reserved budget, deadline/cancel reference |
| AttemptResult | Attempt/source hashes, artifacts, verification state, known/unknown usage, unresolved effects and trusted review-event references |

Verdicts: `reuse_reference`, `adapt_local`, `product_local`, `propose_amsl`, `defer`. `propose_amsl`/`defer` alone cannot satisfy a required runtime dependency: bind an appropriately reviewed implementation or explicit local fallback. A planned product-local target with a frozen contract is sufficient to dispatch its implementation request; code need not already exist. Integration/release requires that target to be implemented and independently verified. An unbound required behavior blocks dependent dispatch. Reject unknown IDs, mutable/unresolved pins, dependency cycles, unsupported transitions, missing mandatory evidence, unsafe paths, changed source hashes and any model-output attempt to mutate catalog maturity. Evidence references must select known supplied IDs; their presence does not prove the claim, so relevance is reviewed independently.

## Authority and transitions

These are enforcement requirements for the future implementation. During the template pilot the named operator explicitly performs and records each check; do not claim the checks are automated.

| Transition | Authorized principal and evidence | Reject |
|---|---|---|
| none → proposed | Any attributed author, including untrusted session/model import | Missing provenance/schema; treat imported instructions as authority |
| proposed → validated | Trusted validator plus designated operator for semantic/evidence review; frozen hashes | Missing bindings, invalid pins/evidence, policy conflicts, missing scope/authority |
| validated → dispatched | Zatiti-authorized dispatcher, or explicitly named fallback operator, with reserved allocation | Stale hashes, duplicate active attempt, absent authorization/fence |
| dispatched → running | Selected attempt owner with current fencing token | Stale owner/lease or mismatched request |
| running → failed_terminal / blocked_uncertain | Trusted host observes failure, timeout, cancellation or unresolved effect | Inferring nonexecution from lost response |
| running → verified_pending_review | Independent verifier bound to current source/protected inputs | Worker self-report alone, changed tests/scope/HEAD, post-check edits |
| verified_pending_review → accepted / rejected | Named human accepter authenticated by host/operator record, exact candidate hash | Model-supplied human identity, missing verification, stale candidate |
| blocked_uncertain → resumed/new attempt | Dispatcher after owner/effect reconciliation, same logical budget and new fence | Blind retry or renewed allowance |

Agent review and human review are separately attributed events, not a mandatory linear chain. Policy can require either/both; agent acceptance never becomes human acceptance. User David is the proposed final preview accepter; C00 confirms his trusted review channel. Shared AMSL admission belongs to that repository's authorized maintainers, independently of product acceptance.

## Shared admission and product state

AMSL paths are A: two independent real implementations; B: one implementation consumed by two independent real projects; C: mature implementation plus immediate second real consumer. Independence means distinct genuine product requirements and use, not worktrees, fixtures or hypothetical projects. The same human may own both projects; distinct people or deployments are not invented requirements. A/B/C applies to proposing shared admission, not every ordinary reference reuse. Insufficient evidence permits product-local implementation and optional DISCOVERED record. Security API/lifecycle promotion remains reviewed governance, never an LLM confidence threshold.

Common mechanisms and product policy stay separate. Stable message/thread IDs, idempotency, approval identity, acknowledgement state and persisted audio/history have explicit owners. Regenerating an adapter/UI does not erase old clients or data. Use additive evolution and retained consumer checks; migration/rollback limitations are declared rather than hidden behind matching signatures.

## Execution and accounting

Reserve before dispatch and aggregate all attempts. Idempotency binds parent, request hash and attempt intent; same key with changed payload is conflict. Restart queries the attempt owner/effect record before dispatch. A timeout creates blocked/uncertain state, not an automatic fresh attempt. Cancellation fences stale completions and propagates to the process tree; evidence must establish whether external effects occurred. Remote mutation reconciliation belongs to the product delivery host, not an invented Fanisi cloud executor.

UNKNOWN usage stays null/unknown with known lower bounds retained. Provider receipts, admission estimates, weekly subscription readings and human minutes are separate. Missing cost does not stop every safe local action, but forbids automatic spend beyond conservative existing reservations or claims of complete accounting. C00 records meter, granularity, cadence, phase reservations and overshoot margin before paid work; unreadable/coarse readings cannot guarantee 5% compliance. No raw secrets enter records/model context.

## Acceptance and non-goals

Verify real boundary failures: stale decisions, source edits after checks, protected-test edits, scope escape, unknown usage, duplicate completion, canceled owner, lost acknowledgement and timeout followed by duplicate dispatch. Product tests include physical install, remote same-thread audio, restarts/history, duplicate upload, microphone denial and exact approval identity. The second change must retain existing data and client behavior.

No shared promotion, general-agent replacement, Jev activation, all-platform integration or second-consumer claim is required for the week. The plan's day-three freeze protects delivery. Stop or narrow when evidence/budget cannot support continuation; adding infrastructure is not the default response to failure.
