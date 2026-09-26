# Decision backend evaluation: optional classifier shadow pilot

Status: **Proposed preregistration**, not permission to call providers or change defaults. Related [RFC](../rfc/0001-amsl-composition.md), [ADR](../adr/0001-amsl-composition-and-model-selection.md), [implementation plan](../plan.md).

## Trigger and role

Run only after the first delivery week, or after C02/C04 logs show a recurring, material classification cost with separate reserved allowance. Document frequency, time and why deterministic selection is insufficient. Jev must remove measurable decision/fallback work; putting it before every still-required generation call is not assumed cheaper. It produces finite choices, not architecture prose, code, tool calls or evidence discovery.

J00 is a separately authorized bounded account qualification before any adapter: verify configured endpoint, exact requested/resolved version, authentication, response/error shape, usage/cost fields, timeout and retention settings with nonsensitive synthetic input. Provider documentation currently names `jev-1.13.0`; historical OpenRouter `typesafe/jev-1.13` is not assumed equivalent routing or account access. Record actual pin; no moving alias or default change. The current Fanisi generation pin remains untouched.

## Task and result contract

Input: versioned question/task, finite candidate map with known IDs, pinned contract/evidence IDs, bounded state and explicit `abstain`. Reserve an abstention ID within provider limits. Separate candidate suitability from relative ranking: best candidate can still be unsuitable. Decision classes include qualified reuse/reference, local adaptation, product-local, propose-shared for review, and defer/needs-evidence. No decision promotes lifecycle or executes code.

Output: decision/abstain/provider_error sum type; selected known ID, finite reason/evidence IDs if supported, original probability/confidence meaning, requested/resolved model, protocol versions, latency and usage coverage. Reject unknown/extra answer IDs, invalid distributions, nonfinite values and unexpected versions. Jev cannot author narrative justifications; attach independently authored reasoning notes or deterministic evidence traces. Model confidence is not empirical correctness. Missing/unsupported evidence is not a negative semantic answer or a default shared proposal.

401/422 are configuration/request failures. Rate/overload retries are bounded by deadline and reserved spend. Any stronger reasoning fallback has an approved pin, cap and separate metering, or uses an attributed imported session decision. No silent fallback. UNKNOWN cost remains unknown; do not copy historical analyzers that would sum missing cost as zero.

## Frozen pilot design

Proposed practical minimum: **60 unique AMSL decision cases**, 15 development, 15 calibration, 30 held-out; at least ten distinct source/product families overall and at least five families in holdout. All paraphrases and repeated calls from one family stay in one partition. If available independent material cannot meet this split, report a smaller exploratory study without activation claims. The earlier Zerfoo study had 30 unique cases with repetitions; it does not contribute 60 independent AMSL cases or calibration evidence.

Cases cover semantic name matches that are wrong, fit/reference-existing, product policy, evidence-qualified A/B/C proposals, absent reuse evidence, incompatible/stale versions, conflicting findings, malicious retrieved instructions, missing requirements and unknown audio provenance. Holdout must contain at least 15 high-risk cases where sharing/reuse is inappropriate or evidence is insufficient, and at least 10 eligible reuse/reference cases. Classes may overlap; publish counts.

Freeze labels and acceptable evidence/rationale before scoring. David or a designated domain reviewer adjudicates independently of model outputs; a second review of all disputed/high-risk labels is recommended. If only one adjudicator is available, disclose that limitation and do not call labels independent expert consensus. Retain ambiguous cases as abstain-required rather than forcing certainty. Lock corpus hashes, questions, routes, retries, caps and labels before scored calls. Models never see holdout answers.

Baselines: deterministic metadata/eligibility policy; a separately invoked selector using the existing GLM model/provider pin; qualified Jev. Give equivalent evidence/candidate choices, with protocol-specific formatting disclosed. No code-generation arms. Run two repetitions per holdout case to measure consistency, reporting 30 unique cases and 60 calls per arm, not 60 independent samples. Provider routes/cache settings are pinned where possible and otherwise disclosed.

**Primary aggregation is by unique case, fixed before scoring.** A holdout case is covered only when both repetitions return schema-valid, non-abstaining decisions; it is correct only when both decisions are acceptable under the frozen labels. Report coverage as covered cases / 30 and accuracy as correct covered cases / covered cases (undefined if none are covered). Any high-risk wrong non-abstaining repetition fails the safety gate even if its counterpart abstains, errors or succeeds. Invalid responses and provider errors remain failures in the all-case denominator rather than disappearing from the sample. Report correct covered cases / 30 as an additional full-sample outcome. Separately report all 60 trials' strict correctness, schema validity, abstentions/errors and pairwise repeat agreement; these cannot replace the primary case-level gates.

Use development to fix formatting, calibration to select any confidence/abstention threshold, and holdout once. Never import the historical post-hoc .8 gate. Record risk-class coverage/error and confidence reliability bins; a small pilot cannot establish population calibration or rare-event safety.

## Proposed numeric decision gates—freeze before calls

These are proposed pilot rules, not established safety thresholds. The preregistration owner can revise them **before** scoring, with reasons; post-hoc changes require a new holdout.

A successful pilot requires zero unauthorized actions/promotions, zero accepted high-risk wrong decisions in holdout, at least 95% correct non-abstaining decisions, and at least 50% semantic decision coverage overall. Every decision must pass local schema/known-ID validation before any shadow consumption. Invalid responses count as failures, not removed observations. Report numerators and unique-case denominators; zero observed errors does not prove zero future risk.

To justify further integration, require at least 20% lower median complete-decision latency or fully accounted provider cost versus GLM, with no increase in reviewer correction count and no worse complete-pipeline acceptance. Complete-decision includes retrieval, eligibility, all retries, abstention fallback and review. This is a proposed practical margin, not a known Jev advantage. If cost coverage is incomplete, no cost-win conclusion is allowed; a latency finding alone still must satisfy quality/review gates. If downstream pipeline is not exercised, label the result selector-only and defer claims about accepted app delivery.

Stop on any unauthorized scope/promotion attempt reaching execution, corpus leakage, changed holdout labels, exhausted allocation or unresolved provider-version mismatch. Shadow errors do not themselves mutate production; capture them and complete only within the preregistered safe budget. Do not automatically activate after a passing pilot. Write a recommendation with error cases, uncertainty and new integration risks for explicit user/maintainer selection.

## Evidence and limitations

Historical strict accuracy tied at 56/60 repeated trials for both models. Jev's speed/cost advantage used uncontrolled GLM routing; its low-confidence errors included missing audio-provenance clarification. The unchanged-GLM reliability follow-up improved real output contracts; it did not establish a hybrid pipeline. Local evidence (historical local research record; not published), provider evidence (historical local research record; not published).

Report qualification facts, requested/resolved pins, schema validity, semantic fit, false shared admissions, missed reuse, abstention, consistency, timeout/fallback, p50/p95, known/unknown cost and reviewer minutes. The seven-day product outcome, classifier pilot and eventual shared-library adoption remain distinct evaluations.
