# Evaluate accepted changes

`fanisi eval` runs a real repository change from a frozen Git commit in a fresh
worktree. Compare `fanisi`, `claude`, and `claude-packet` with the same task,
write scope, and independent verifier. Claude arms require an installed Claude
Code binary; all arms use OpenRouter's `z-ai/glm-5.3-flash`.

Create an evaluation manifest alongside a normal task configuration:

```json
{
  "schema_version": 1,
  "task_id": "issue-123",
  "repository": "../../product",
  "base": "FULL_COMMIT_SHA",
  "worktree_root": "../../evaluation-worktrees",
  "output": "./attempts",
  "task_config": "./task.json",
  "protected_files": ["./verify.sh"],
  "preparation_seconds": null,
  "claude_max_estimated_cost_usd": 5,
  "claude_max_output_tokens": 8192
}
```

Paths resolve relative to the manifest. Use a full commit SHA, not a branch name.
The verifier must fail with exit 1 on the baseline and pass on a correct change.
List its external files as protected inputs. Supply the same acceptance brief to
all arms; source slices in the task's `context` become the packet treatment.
Freeze the protocol before running, and record amendments separately.

```sh
fanisi eval --config evaluation.json --arm fanisi --attempt 1
fanisi eval --config evaluation.json --arm claude --attempt 1
fanisi eval --config evaluation.json --arm claude-packet --attempt 1
```

Each attempt gets a separate branch, worktree, configuration snapshot, prompt,
logs, and candidate patch. Existing attempts are never overwritten. Worktrees
remain available for review; the runner does not commit, push, merge, or remove
them. Arrange project work claims before running against a shared repository.

The independent verifier runs after the worker, including on worker failure.
A candidate requires an actual scoped edit, unchanged protected inputs, an
unchanged base commit, and no tracked or untracked changes outside write scope.
Passing records `verified_pending_review`. It does not establish acceptance.
These checks detect changes afterward; they are not an operating-system sandbox.
Ignored build outputs and the rest of the host filesystem are not sealed.

## Claude isolation and limits

Claude uses a scratch home and configuration directory, process-local OpenRouter
auth, explicit model selection, scrubbed credential variables, no inherited Git
configuration or SSH agent socket, no hooks, no MCP servers,
and no session persistence. Existing subscription sessions and configuration
files remain untouched. Claude retains a general shell and file tools, unlike
Fanisi's scoped tools. Protect sensitive host files through an external sandbox
when that is a requirement.

`claude_max_estimated_cost_usd` is required for Claude arms and is a CLI estimate,
not an OpenRouter invoice or a comparable native Fanisi dollar allowance.
`claude_max_output_tokens` is optional: zero or absent retains the CLI default;
1024..64000 sets its per-response output cap through a process-local environment
variable. Record this as a treatment when comparing results. Set `claude_provider` to `"Z.AI"` to pin the same provider as native Fanisi through an authenticated process-local relay; omit it to retain OpenRouter routing. The relay keeps the provider key out of the child process and records request metadata without headers or source bodies. Receipt reconciliation must still confirm actual provider selection. Both arms inherit
the task's wall-clock and call/turn limits, whose semantics differ by harness.
The installed CLI's offline request-shape test can check the cap without paid
inference:

```sh
FANISI_CLAUDE_PROTOCOL_TEST=1 go test -run TestInstalledClaudeRequestShape -v
```

## Receipts, review, and repairs

```sh
fanisi reconcile --key-file .env attempts/issue-123/fanisi/1
fanisi review --attempt attempts/issue-123/fanisi/1 \
  --decision reject --reviewer reviewer-name --kind agent \
  --notes-file findings.md
fanisi report attempts
```

Receipt reconciliation deduplicates visible generation IDs and fetches provider
receipts. The provider relay captures generation IDs from streamed message-start
events before Claude finishes an assistant message, retaining billing identities
across interrupted replies without storing response content. Missing relay IDs
and missing stream-stop events remain explicit coverage gaps. Skipped malformed
or oversized frames also leave coverage unknown, even if a later stop arrives. Failed CLI sessions can omit request IDs; their total remains unknown
while resolved spend is retained. Reasoning tokens are a subset of output tokens.
No command turns missing usage into zero or counts a CLI cost estimate as a bill.

Review events bind to the verified patch hash. Acceptance rechecks the current
workspace, configuration, protected inputs, and scope. Record `--kind human` only
for a human's decision. Omit `--seconds` when duration was not measured. Review
acceptance is distinct from a repository's merge approval or actual landing.

To repair a rejected candidate, create a new manifest with `repair_from` pointing
to its attempt directory, and use a new attempt number with the same arm and
base. The runner applies the parent's recorded patch to a fresh worktree and
protects that patch's hash. Provide the review findings and any new independent
checks in the repair task. Preserve the original attempt and explain when checks
were added. A repair must start red and produce a new scoped edit.

Reports count failed attempts and repairs in each arm's cumulative spend and
execution time, then divide by distinct accepted tasks. With no accepted task,
per-accepted metrics are null. Incomplete receipts also keep cost/token ratios
null. Human and agent acceptance are reported separately. Preparation time is
counted once per task per arm; null means unmeasured. Attempt wall times are
summed durations, not the elapsed duration of a parallel study. Coordinator
usage, CI, review, and landing delays need separate accounting before claiming
an end-to-end productivity improvement.

## Instrument a Kazi Claude dispatch

The experimental `claude-bridge` command accepts Kazi's `-p`, pinned `--model`,
and `--output-format json` arguments after `--`. A small executable wrapper can
supply its frozen task, private output directory, and explicit limits:

```sh
exec /absolute/path/fanisi claude-bridge \
  --config /absolute/path/task.json --output /private/dispatches \
  --max-output-tokens 8192 --max-estimated-cost 20 -- "$@"
```

Set that wrapper as the goal's `[harness] command`. The bridge verifies the
current working directory, rejects unsupported controller options, preserves
stricter controller limits, records each dispatch separately, and returns the
unique original Claude terminal JSON to Kazi. Its subprocess remains in Kazi's
process group so the controller can terminate descendants. Give the complete
controller run an external wall-clock deadline; bridge limits apply per dispatch.
Use an isolated Kazi read-model and a dedicated task worktree. Reconcile every
dispatch's receipts, including failures, and independently review the final diff.
The bridge does not itself create an evaluation attempt or an acceptance record.


### Verify early receipt capture against the provider

The default test suite is offline. This separate opt-in probe sends one paid
GLM request with a 64-token output cap, closes the reply after its generation ID
arrives, and retains metadata for reconciliation. It is a transport check, not a
coding benchmark. Choose a fresh output directory:

```sh
FANISI_LIVE_RELAY_TEST=1 FANISI_LIVE_RELAY_KEY_FILE=.env \
  FANISI_LIVE_RELAY_OUTPUT=.fanisi/probes/early-id-1 \
  go test -run TestLiveRelayCapturesEarlyGenerationID -v
fanisi reconcile --key-file .env .fanisi/probes/early-id-1
```

A missing receipt remains unknown even when the transport probe passes.

## Coordinator tokens

Worker receipts omit the coordinating session. For a Codex JSONL session with
cumulative `event_msg` / `token_count` observations, capture that usage separately:

```sh
fanisi coordinator-usage --from 2026-09-07T00:38:47Z \
  --to 2026-09-07T05:00:00Z /private/path/to/session.jsonl > coordinator-usage.json
```

The command subtracts the last observation strictly before `--from` from the last
observation within the requested window. It reports both observed timestamps;
boundary requests can overlap the window, and it infers no usage after the last
observation. Input includes cached tokens; reasoning is part of output. Total is
computed from input plus output, not the transcript's potentially stale total.
Dollar cost stays null. This supplements worker receipts; it is not included in
`report` automatically and does not measure other reviewing sessions.

Successful JSON output contains counters and a SHA-256 fingerprint of the
byte-bounded transcript snapshot, without message text or the file path. Missing boundaries/counters,
resets, out-of-order observations, invalid JSON, inconsistent counts and lines over
16 MiB fail instead of producing a plausible total. A partially written final
record also fails; retry after it has been written. The local Codex event shape
must match the supported fields; this command does not infer missing categories.

## Offline effort and verified landing

`fanisi import-effort STUDY_DIR RECORD.json` appends a version 1 effort record.
Required fields: `schema_version`, `id`, `study`, `role`, `source_fingerprint`,
RFC3339 `from` and `to`, `coverage` (`complete` or `partial`), and `allocation`.
`exclusive` allocation requires `task` and a study-relative `attempt` directory;
`study` allocation keeps shared `preparation` or `tooling` separately reported.
Other roles are `coordinator` and `reviewer`. Optional `cost_usd`, `active_seconds`
and `tokens` remain null when unknown. Token input includes cached input and
output includes reasoning; `total_tokens` must equal input plus output.
Repeated identical imports are idempotent. Conflicting identities and overlapping
intervals from the same source are rejected. Use one stable source fingerprint
for a source's attribution windows; snapshots from different source versions
cannot establish non-overlap automatically. Concurrent imports fail on a lock;
retry after the owner finishes. A crashed importer may leave the lock directory.

`fanisi import-coordinator STUDY_DIR ATTRIBUTION.json COORDINATOR.json` reads the
existing `coordinator-usage` output directly and fills its snapshot, effective observation window, tokens,
and nullable price into the attribution record. A stable `source_fingerprint` must be supplied in the attribution; snapshot hashes
are retained separately, so growing transcripts cannot bypass interval deduplication.
Cumulative observation coverage remains partial, even with a known price. No transcript parsing, provider calls or price inference occurs.

`fanisi import-landing ATTEMPT_DIR RECORD.json` checks a version 1 record with
`id`, `repository` (local Git repository), full `target_base` and `merge_commit`
SHAs, `target_ref` (full target branch ref containing the merge result), `review_record` filename, `patch_sha256`, `merge_evidence`, `independent_review: true` and optional `at`. The referenced
review must name its reviewer; independence is an explicit caller attestation.
The repository must be the evaluated Git repository. A temporary index rebuilds
the reviewed candidate and exact scoped blob/mode deltas must match the landing.
Equivalent squash/rebase content works; intervening edits to scoped baselines
require fresh verification and review. Open PRs and ancestry alone prove nothing.
This command records evidence; it never merges. A correction is a new record;
revoke old evidence by appending an `id`, `revokes`, `schema_version` and
`merge_evidence`. Revocations are permanent, preserving prior evidence.

`report` retains historical `arms` fields and adds `effort` and `delivery`.
Historical `tasks_accepted` means patch review, not landed benchmark acceptance.
Use `delivery.accepted_landed_tasks` for that denominator: task identity deduplicates
repairs and alternatives, and `repair_from` marks assisted acceptance. Every
attempt, including failures, contributes. Delivery receipts deduplicate provider
plus generation identity and reject inconsistent token subsets; historical
aggregate-only receipts retain their cost lower bound with incomplete coverage.
No harness estimate is added to settled receipts. Task-attributed effort costs join the
known lower bound; shared study preparation/tooling remain separate; missing coordination/review coverage means total cost and
cost per autonomous acceptance remain null. Review durations and imported reviewer
active time are separate measurements; do not add them together for the same work.
Elapsed request-to-landing spans use earliest request and landing timestamps,
never summed overlapping intervals. Missing timestamps omit that task's elapsed
value; CI wait remains explicitly null. Study tooling costs have no implicit
amortization. These records do not rank models or establish total-dollar savings.

## Compare direct Claude with Kazi Claude

The opt-in `kazi-claude` arm uses the same frozen base, task brief, baseline
verification, final verifier, filesystem audit, review and landing ledger as
`claude`. Add this object to the evaluation manifest:

```json
{
  "kazi": {
    "executable": "/isolated/bin/kazi",
    "executable_sha256": "<SHA-256 of the pinned Kazi executable>",
    "max_dispatches": 2
  }
}
```

Then run `fanisi eval --config evaluation.json --arm kazi-claude --attempt 1`.
The optional `fanisi_executable` selects an isolated bridge binary; otherwise the
running Fanisi executable is used. Both executables are hashed as protected
inputs. Provider pinning to `Z.AI` is required for this arm. The internal
`eval-bridge` command requires the running evaluation parent's authenticated
loopback admission service; a standalone bridge invocation cannot admit work.

The generated goal deliberately has no scope paths. Fanisi enforces the exact
shared task scope against actual filesystem bytes, types and permission modes,
including ignored files and files hidden by Git index flags. Kazi therefore does
not generate scope-tree `AGENTS.md` or `CLAUDE.md` files. Default orientation and
context-tier behavior remain enabled. The goal sets `scope.no_integration=true`,
`integration.mode="none"`, `conventions.process_contract=false`, and explicitly
pins `permission_mode="dontAsk"` with the same six tools used by direct Claude.
These are recorded trial configuration choices, not claims that all Kazi defaults
are identical to direct Claude. The shared brief supplies the allowed files.

The task's `max_seconds` is one cumulative worker deadline, including controller
startup, observations and repair. A direct session receives the total turn and
CLI estimated dollar allowance. Kazi can start up to two sessions, each reserving
an equal dollar share and an integer turn share (remainder on the last slot).
Reservations are recorded before launch and never refunded. Concurrent workers
and launches exceeding the declared dispatch allowance are rejected. This does
not equalize actual session counts: one session versus up to two is an explicit
treatment difference. Claude turns are not HTTP request counts; CLI dollar
estimates are not invoice caps, and the task's input-token allowance is not an
enforced Claude token limit. No pilot spending guarantee follows from these
settings. Provider receipt reconciliation remains necessary.

Workers own process groups and stop at the same deadline in both arms. Kazi has
up to five additional seconds for controller termination and receipt flushing;
no new worker admission is available during that grace. The parent also records
active worker process groups and kills them on controller termination. Ordinary
nonzero exits and timeouts remain in attempt/dispatch evidence but do not veto
an independently valid final candidate. Scope or trusted-input integrity errors
still fail closed. Final verification creates only `verified_pending_review`;
independent review and verified landing remain separate ledger events.

### Controller artifacts and receipt manifests

The parent captures exact controller artifacts before admitting each worker,
checks them after that worker and again before final grading, and protects the
manifest bytes with an in-memory digest. Default artifacts are only
`.kazi/context.md` with the generated orientation banner and the canonical
code-review-graph `.mcp.json`. Other regular artifacts require exact predeclared
SHA-256 values in `kazi.artifacts`. There is no blanket `.kazi` exception;
unexpected instructions, symlinks, additional files or mode changes are rejected.
The pinned controller is trusted to generate these files at the admission
boundary. This is an audit boundary, not an OS sandbox against hostile processes
sharing the same account.

The final verifier receives `FANISI_CONTROLLER_ARTIFACT_MANIFEST` only after the
parent validates its bytes against its own records. Its version 1 JSON schema is:

```json
{
  "schema_version": 1,
  "workspace_base": "<full frozen commit SHA>",
  "artifacts": {
    ".kazi/context.md": {
      "sha256": "<exact generated bytes SHA-256>",
      "type": "regular",
      "mode": 420,
      "baseline": null
    }
  }
}
```

Paths are relative to the candidate. Modes are decimal POSIX permission bits.
A modified baseline file has its prior `sha256`, `type`, `mode` and, for a
symlink, `link_target` recorded in `baseline`; a newly created file has `null`.
A verifier must match exact identities and restrict any exceptions to these
records. A worker's self-invoked verifier or forged environment cannot authorize
the final parent's acceptance. Candidate patches use raw Git objects with clean
filters and text conversion disabled, preserving the audited production bytes.

`dispatch-manifest.json` lists explicit `dispatch-0001`/`dispatch-0002` directories,
admission/completion times, reserved turns and estimated dollars, errors,
artifact identities, cumulative deadline and rejected admissions. Running
`fanisi reconcile ATTEMPT_DIR` reconciles every listed dispatch, including failed
ones, then writes one attempt receipt ledger. Duplicate provider/request IDs are
counted once; conflicting duplicates fail. Missing, empty or incomplete dispatch
receipts keep totals unknown while preserving known lower bounds.

### Offline installed integration check

The synthetic fixture has no private project content. With isolated binaries:

```sh
FANISI_E77_CANDIDATE_BINARY=/isolated/bin/fanisi \
FANISI_E77_KAZI_BINARY=/isolated/bin/kazi \
  go test -run '^TestInstalledKaziEvaluation$' -v .
```

This explicitly selected test executes both supplied binaries with a fake Claude
worker. It checks first-pass success, failed-first repair, rejected unexpected
instructions, and timeout during an active worker/relay request. The last case
uses a blocking localhost proxy, sends no provider request, checks worker and
child process death, and retains incomplete receipt evidence. Kazi database,
state, sinks and home directories are private to each temporary trial. The
ordinary offline suite skips this check unless both binary paths are supplied.
