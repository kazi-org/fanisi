# Fanisi

Fanisi is a small Go coding harness for reducing tokens, cost, and elapsed time
per accepted software change. It gives a model a precise task and bounded tools,
records what it spends, and runs a trusted verifier before returning a candidate
for review.

It is a standalone project: no Kazi installation, Claude Code subscription,
Python wrapper, database, or graph service is required. The current backend is
OpenRouter **`z-ai/glm-5.3-flash`**, pinned to Z.AI with no model/provider fallback.

## Build and try it

Requires Go 1.26 and macOS or Linux. No third-party Go dependencies.

```sh
go build -o bin/fanisi .
bin/fanisi --help
bin/fanisi run --config examples/clamp.task.json --dry-run
```

The dry run validates the config and prepares its context without a key, model
call, verifier execution, or run artifacts. All config file paths resolve relative
to the config file; scope paths resolve inside its workspace.

The example is a deliberately broken `Clamp` function with six independent test
cases in a read-only file. It is a nested Go module, separate from Fanisi's tests.
Its lower-bound cases fail before repair. To run the real model:

1. Put your OpenRouter key in `.env` using the format in `.env.example`, or point
   `key_file` in the task config to an existing key file. Alternatively, omit
   `key_file` and set `OPENROUTER_API_KEY` in the environment.
2. Review the task, fixed verifier and admission-price snapshot in
   [examples/clamp.task.json](examples/clamp.task.json). The recorded price is an
   estimate from 2026-09-06; refresh it before relying on its admission limits.
3. Run `bin/fanisi run --config examples/clamp.task.json`.
4. Review the resulting diff and test evidence. Successful runs return
   **`verified_pending_review`**, not automatic acceptance, commits or publication.

The example edits `examples/go-fix/clamp.go` directly. For your real repository,
prepare a dedicated task worktree and point `workspace` at it. Give every attempt
its own output directory; Fanisi refuses to overwrite an existing run. To repeat
an experiment, use a fresh workspace at the same base and a new output path.

## Task configuration

Start from the example. Required fields are `schema_version: 1`, `workspace`,
`output`, `prompt_file`, `write_paths`, `verify_command`, and explicit `pricing`
for admission estimates. Unknown fields are errors, so misspelled budgets fail
before spending. Defaults and bounds are in [config.go](config.go).

- `read_paths` adds read-only context. `write_paths` is the exact editable file
  list; these files are also readable. Paths may name new files for the create tool.
- `context` selects source slices with `path`, one-based `start`, and `lines`
  (1..120). Each slice carries a source hash. Oversized optional slices are omitted
  and reported; the mandatory brief, scope, and acceptance command are never
  silently shortened.
- `format_command` is an optional argv array; `verify_command` is a required argv
  array. Fanisi does not choose a build system or interpolate model text into
  commands. If a shell is necessary, explicitly configure it as the executable.
- `max_calls`, `max_seconds`, `max_tokens_total`, `max_cost_usd`,
  `max_output_tokens`, `max_context_bytes`, and `packet_bytes` bound work.
  `reasoning` accepts `medium` or `low`; model and provider remain pinned.
- `pricing` declares input/output USD per million tokens plus its source. It is
  only a conservative admission estimate. Actual request spend comes from
  provider usage, with independent receipt lookup available afterward.

Keep the verifier outside write scope and write the acceptance requirements
before execution. An always-green verifier cannot establish correctness. Human
review and tests that detect the original defect remain part of acceptance.

## Tools and completion

The model can search scoped files, read bounded slices, replace one exact unique anchor, create an
explicitly scoped new file, run the configured formatter, and run the configured
verifier. No general model-authored shell tool is exposed.

Search finds a literal string in one readable file and returns bounded matching
lines with a continuation cursor. It reads at most 2 MiB and does not execute
regexes or shell commands. Use it to locate symbols before reading a block.

Reads return at most 120 lines / 12,000 bytes with a continuation cursor. Edits
reject path traversal and symlinks; creates never overwrite. Full command logs
stream to disk while only bounded excerpts return to the model. A workspace lock
rejects a second Fanisi writer. If a process is killed, inspect `.fanisi-lock`
and its owner record before manually removing a stale lock; Fanisi never guesses.

A successful verifier must correspond to the final scoped file hashes and an
actual change from the run's starting state. A later edit invalidates verification.
All tool calls in a response are processed before deciding to finish. Model prose
alone does not establish completion. Run failures retain their evidence.

These are tool boundaries, not an operating-system sandbox. The trusted verifier
executes repository code locally. Other editors do not honor Fanisi's lock, and
The evaluation runner detects tracked and untracked changes outside scope after execution; the standalone run command does not provide that repository-wide check.

## Inspect usage and evidence

```sh
bin/fanisi packet --config examples/clamp.task.json --out /tmp/fanisi-packet.md
bin/fanisi profile .fanisi/runs/clamp
bin/fanisi billing --key-file .env .fanisi/runs/clamp
bin/fanisi analyze path/to/claude-stream.jsonl path/to/provider-ledger.json
```

Runs emit JSONL generation events and a terminal result. Artifacts include the
packet and section hashes, initial/verified file hashes, API request/response
bodies, per-call metrics, tool receipts, full command logs, and `result.json`.
Auth headers are never recorded. Key files are parsed as literals, never sourced;
key/token environment variables are removed from verifier subprocesses. Raw
artifacts contain source and model output and should remain private.

`profile.json` reports native token/cost subtotals, coverage, deduplicated
requests, and exact cumulative JSON bytes by role and tool schema. Bytes and
cached input are not interchangeable with new billed tokens. Unknown aggregate
usage makes the run's total tokens/cost `null` while retaining known subtotals.
Missing optional cached/reasoning counts remain unknown. Reasoning is a subset of
output, never an additional token charge in the total.

Admission conservatively uses request bytes as a token upper estimate. It can
stop before the provider-native token limit. Price estimates can age, receipts
can arrive late, and failures can be billed: limits are not a provider-enforced
dollar ceiling. No automatic network retries hide additional spend. The profiler
marks missing responses explicitly rather than treating them as free calls.

## Compare real changes

The [evaluation runner and acceptance ledger](docs/evaluation.md) create frozen
worktree trials, reconcile provider receipts, record reviews and repairs, and
report cumulative cost per accepted change across Fanisi and Claude Code.

## Development

```sh
gofmt -w *.go
go vet ./...
go test -race ./...
go build -o bin/fanisi .
```

Default tests are offline. They exercise the full run with a local fake API,
actual file edits and verifier processes, unknown usage, failed checks, late
edits, scoped creates, locking, config/packet limits, and credential isolation.
CI is configured for macOS and Linux. The nested demonstration module intentionally
starts red and is not part of the root test suite.

The [design](docs/design.md) describes the current boundaries. The
[initial experiment](docs/experiment-001.md) records why this project exists and
why its early savings are not yet a general productivity claim.
