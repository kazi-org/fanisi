# Design

Fanisi turns a task contract into a verified candidate change. Acceptance remains
an external review decision. Its loop is deliberately small:

1. Validate explicit scope, verification command, context and spend limits.
2. Build a deterministic packet, preserving the mandatory contract and fitting
   optional source slices into their separate allowance.
3. Record an API request and native usage, then execute declared bounded tools.
4. Continue until the verifier passes against the final changed files, a limit
   stops the run, or an error prevents trustworthy continuation.
5. Preserve the candidate and evidence for independent review.

Configuration and packet code know file paths and context budgets. The run loop
knows model messages and tool results. Command execution uses fixed operator
argv, separate from model-authored text. Profiling operates on immutable response
artifacts and distinguishes known data from missing data.

The current CLI is a single Go package split by responsibility. It has no service,
shared database, background indexing or framework. New packages should follow
actual reuse rather than anticipated architecture. A controller such as Kazi may
invoke Fanisi as a worker in future; no integration is assumed by this release.

The operator owns worktree creation, acceptance criteria, review, and landing.
File hashes ensure that a verification result is not reused after an edit within
the same run. They do not certify semantic correctness or protect against other
programs modifying the repository concurrently. Tests are trusted local programs;
OS-level isolation belongs to the enclosing execution environment.

Pending work should be justified by measured costs on unfamiliar changes:
verified reuse of context, evaluation across more change types, review/repair
accounting, and broader execution adapters. Neither a large graph index nor
model routing is needed to validate the present workflow.
