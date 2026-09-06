# Initial experiment: Kazi help completeness

On 2026-09-06, the prototype fixed Kazi issue #1792 at frozen source
`ec7d77ac7426b94bcc15a07f3058e0816f69be21`: human help omitted 21 registered long
flags, and its regression tests checked command names but not flag coverage.

| Method | Native tokens including cached input | OpenRouter USD | Run seconds |
|---|---:|---:|---:|
| Plain Claude Code | 1,035,669 | 0.027327185 | 653.44 |
| Source packet plus Claude Code | 825,846 | 0.026398740 | 899.90 |
| All Go attempts including failure and repair | 203,745 | 0.007821440 | 253.49 |

All used `z-ai/glm-5.3-flash` via OpenRouter. The failed first Go attempt used
SiliconFlow once and Z.AI for its other requests; the optimized attempt and repair
pinned Z.AI. The Go first-attempt-to-verified-repair interval was 583.70 seconds;
run seconds exclude gaps. Prototype engineering and coordinator usage were not
included. Generation receipts reconciled the measured model totals.

The first Go attempt exhausted a conservative admission budget before acceptance.
The optimized attempt initially passed automated checks but source review rejected
a regex that let a longer hyphenated flag satisfy a shorter flag token. GLM repaired
it. The final patch passed 70 focused tests, formatting, checks against the
original implementation, and both token-boundary mutations. The reported Go totals
include those failures and repair calls.

The former packet arm spent 85.7% of its tokens on cached input and 82.4% of its
output on reasoning. It reread roughly 60 KB of source despite its prepared packet.
The Go approach used narrower context, bounded tools and compact verification.
Several variables changed together, so these numbers do not isolate any single
optimization. One repeatedly studied issue does not establish general savings.

Fanisi refines that prototype into a standalone tool: generic task configuration
and source packets, configurable verification/formatting, scoped file creation,
workspace exclusion, and explicit verified-pending-review results. Raw original
experiment artifacts remain in the originating Kazi workspace; credentials and
private streams are not copied into this repository. The new offline tests prove
execution contracts, not a fresh live-model performance benchmark.
