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
variable. Record this as a treatment when comparing results. Both arms inherit
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
receipts. Failed CLI sessions can omit request IDs; their total remains unknown
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
