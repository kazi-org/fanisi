# ADR 0001: Template-first composition and separate model decisions

Original proposal status: **Proposed**, 2026-09-26. Implementation was subsequently authorized; model-default changes remain separately gated.

Implementation update, 2026-09-26: local composition v1 is implemented under the [implementation plan](../plan.md) and [as-built contract](../plans/fanisi-composition-contract.md). The proposal below records the original investigation; the supervised product trial, live adapters, shared-library promotion and model evaluation remain separate gates. Historical catalog counts describe the investigated revision, not the current catalog.

## Context

The user clarified that Fanisi should classify missing behavior as AMSL-first or product-local, delegate code and compose an app. Existing AMSL implementation makes this narrower role plausible. The original general-agent question and first Quorum review did not evaluate this exact architecture. Current evidence and readiness are summarized in [RFC 0001](../rfc/0001-amsl-composition.md).

A final two-round Quorum review challenged the architecture before these documents were written. Eight first-round responses completed; five second-round responses completed and three truncated, with earlier responses retained; chair synthesis completed. Shared evidence is not independent verification. Provider-reported cost was $2.05050600576 for 17 calls, 244,627 prompt and 62,096 completion tokens. Reconciliation and artifacts (historical local research record; not published).

## Proposed decision

Run a supervised template/import composition trial first. Add only proven-useful validation/delegation code, retaining existing Fanisi scope, verification, review and accounting. The product preview/update does not depend on completing that code. Freeze unfinished composer code at end of day three and use documented operator checks; report manual interventions honestly.

Preserve generation model/provider defaults. Jev, if separately qualified, is a classifier-only shadow candidate, never a code generator or architecture narrator. Deterministic filtering and a bounded current-model selector are baselines; model confidence cannot grant authority. A stronger reasoning fallback requires an explicitly approved pin and capped allocation, or imported reasoning from the existing session; no implicit expensive routing/config is introduced.

Keep Zatiti parent authority, Foundry product-stage responsibility, Fanisi composition, optional Kazi single-attempt convergence and Griffon runtime services distinct. Initial logical roles need not be separately implemented services. Shared admission and human acceptance remain external to models.

## Options and tradeoffs

| Option | Benefit | Cost/risk | Disposition |
|---|---|---|---|
| Templates over existing harness | Smallest immediate investment, exposes real decisions | Manual validation/interventions; not headless autonomy | First trial |
| Small Fanisi validation/composition path | Repeatable records and rejecting boundaries | Schema/adapter work can distract from product | Add only within reserved effort and day-three cutoff |
| New general agent/service | Unified custom runtime | Duplicates mature harness/controller work | Out of scope |
| Jev by default | Potential fast/cheap typed selection | Domain shift, uncalibrated confidence, account uncertainty | Rejected pending shadow evidence and explicit selection |
| Deterministic only | Auditable and inexpensive | Cannot reliably judge semantic fit | Required baseline, not presumed universal solution |

## Jev evidence and limits

The prior Zerfoo experiment used **30 unique cases**, twice per model, not 60 independent cases. Both Jev and GLM scored 56/60 strict; Jev was faster/cheaper under different uncontrolled hosted routing. Its confidence gate was post-hoc; a missed unknown-audio-provenance clarification is relevant to this product. The follow-up improved actual Worker outputs by correcting contracts/recovery while retaining GLM. These support a narrow selector trial, not default replacement or architectural superiority. Historical evidence (historical local research record; not published).

Provider documentation says Jev does not generate text/code and uses a distinct typed protocol. Account availability and actual version resolution are unverified. Narrative notes must remain separate from finite choice/evidence IDs. Provider evidence (historical local research record; not published), [evaluation protocol](../plans/decision-backend-evaluation.md).

## Consequences and reconsideration

The workflow can deliver useful evidence even if no new Fanisi code ships. Classification benefit, integrated product success, shared admission and human acceptance are separate outcomes. Revisit automation only after measured repeated manual work; revisit Jev only after triggered qualification and held-out evaluation. A second real consumer strengthens reuse evidence but is separately scoped/budgeted. No cross-repository mutation, dependency adoption, deployment or promotion follows automatically from this ADR.
