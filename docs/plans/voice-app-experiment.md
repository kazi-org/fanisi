# Fanisi composition experiment plan

Status: **Proposed**, 2026-09-26. All rows below are NOT STARTED. Roles are assignments to make, not active workers or authority to modify peer projects. [RFC](../rfc/0001-amsl-composition.md), [ADR](../adr/0001-amsl-composition-and-model-selection.md).

## Product bar and resource contract

Within seven days, aim for Zatiti delivering a physical-iPhone Codex voice-note preview and one accepted update. David is the proposed human accepter; confirm the trusted review channel at C00. A simulator, generated code or direct-harness delivery alone does not establish Zatiti delivery. Direct tools are a labeled fallback for orchestration failure, not signing/transport failure.

Before paid work C00 must fill this run-specific ledger: experiment start/end; actual weekly allowance baseline/units/reset; meter source/granularity/lag; maximum delta corresponding to 5%; conservative overshoot reserve; remaining allowance; allocations for qualification, app implementation, review/deployment, maintenance, and optional composer work; external-worker dollar cap; per-attempt deadline/call limit. Sum reservations within available allowance and preserve review/maintenance capacity before optional work. Do not invent a fixed phase percentage. Read meter at start, after qualification, before each paid batch, before optional comparison, and at completion; if reset/lag prevents meaningful accounting, stop new automatic spending at already reserved bounds and report compliance unknown. API tokens cannot prove subscription percentage.

Independent verifier inputs and final acceptance rubric are owned by the reviewer/operator, outside implementer write scope. All paid failures, repair attempts, coordinator work and review time enter the ledger. UNKNOWN usage remains distinct.

## Tasks and dependencies

| ID | Dependency | Owner role; scope | Done evidence |
|---|---|---|---|
| C00 | None | Operator + product reviewer; freeze scope, budget/meter, authority and device/session canaries | Signed empty build installed on actual iPhone; authenticated remote Codex-thread text round trip; timed Zatiti task through independent verification; ledger and named accepter recorded |
| C01T | None; spending after C00 ledger | Composition author + reviewer; use existing templates for request/decision/manifest | Frozen examples for real reuse, justified local policy, unsupported shared proposal; explicit manual checks for IDs, pins, evidence, bindings and authority. Not called machine-enforced |
| C01V | C01T; optional allocation | Fanisi implementer; separate read-only schema/validator | Reject invalid pins/IDs, cycles, missing binding/fallback, stale hashes, unsafe paths, absent evidence and forbidden promotion; current CLI/model behavior unchanged |
| C02 | C01T | Composition author + independent reviewer; select actual product dependencies | Pin relevant qualified dependency or document why none fits; separate reasoning from executable records; invalid A/B/C, stale/conflicting evidence and injection cases rejected/escalated; no forced adoption |
| C03 | C01V,C02; optional | Fanisi integration owner; one delegate/result seam | Real boundary tests for out-of-scope/protected-test edits, final-hash mismatch, unknown cost, failure, duplicate result, cancel/restart, stale owner and timeout reconciliation; no assumed Kazi API |
| C04 | C00,C02; C01T workflow OR qualified C03 | Product implementer + release operator | Native audio UI and durable bridge; real same-thread remote audio, restart/history, duplicate upload, denied microphone and approval-identity tests; installed preview with source/build/version evidence |
| C05 | C04 | Product implementer + David/reviewer | Edit/cancel pending outbox transcript or equivalent frozen real change; old messages/clients retained, race/uncertain submission checked; updated preview accepted and total effort reconciled |
| C06 | C05; new scope/budget | AMSL maintainer + second consumer owner | Genuine independent product use; A/B/C evidence and policy differences reviewed; no duplicate worktree/synthetic consumer; promotion separate from product success |
| J00/J01 | Trigger and separate allowance | Evaluation owner + adjudicator | Qualification and classifier shadow protocol in linked plan; no runtime activation/default change |

C04 explicitly does **not** wait for new C01V/C03 code. The operator can execute C01T manually with existing harnesses and verifier tools, recording interventions and limitations. Existing Fanisi native/Claude worker pins stay unchanged. Other executor selections require explicit pin/authority records.

## Seven-day checkpoints and switch rules

Day numbers are relative to the recorded experiment start at C00, not this document's calendar date.

Day 1 establishes C00 and starts C01T. A proposed two-hour cap applies only to **new Zatiti glue**: if exceeded, stop controller work and evaluate direct-tool delivery, labeled accurately. Signing or authenticated transport failure blocks the actual device outcome and must be reported; no simulator substitution. The durable bridge is new product work, not assumed infrastructure.

Days 2–3 freeze the minimal decisions and build the product. Optional C01V/C03 runs only within its reserved allocation. **At end of day 3, freeze any unfinished new Fanisi implementation and continue via C01T/manual workflow.** Retain failed work/evidence; do not silently expand the schedule or budget. Record whether manual decisions exposed repeated automation value.

Days 4–5 integrate, independently verify and install the preview. Days 6–7 implement/install the subsequent change, review and reconcile. Preserve enough allocation for these stages before funding comparisons. The week is not a promise of feasibility before C00.

A matched patch/regeneration comparison is optional: same accepted base, model/provider, requirements, context permissions, scope, checks and repair caps. Count both arms' failed work, integration and review. One shipped method yields feasibility evidence only. Do not mix a Jev selector experiment into that comparison.

## Isolation, lifecycle and stop conditions

One owner controls an attempt's repairs. Request hashes and fencing prevent stale results; timed-out effects are blocked/uncertain pending reconciliation, never blindly retried with fresh allowance. Existing adapters must demonstrate these semantics before autonomous use. Until then an operator owns recovery, and the report says so.

Shared source changes require the target repository's actual owner/claim and isolated worktree; a Fanisi product worker may propose, not silently land them. Do not change catalog lifecycle, reviewer identity or protected tests from model output. Independent human acceptance references exact candidate/build evidence. Review events can occur independently; no manufactured sequential approvals.

Stop new paid work when reservations or deadline exhaust. Freeze composer work on day-three cutoff; stop optional evaluation on its predeclared failure/budget gate. Treat repeated contract/state regressions as evidence against that replacement boundary, not automatic authorization to redesign platforms. Foundry/Griffon/Gist/Jev are not prerequisites. Use existing deployment only when qualified; repair concrete composition seams only when needed by the chosen product route.

## Required validation when implementation is later authorized

Keep default tests offline. Exercise actual dispatch/run boundaries rather than implementation snapshots. Run gofmt, go vet ./... and go test -race ./... for relevant Fanisi code changes under repository lease/load rules. Protect source fixtures such as intentionally red examples/go-fix. Live device/provider/cloud checks are separately labeled and authorized; recorded source tests are not executed proof.
