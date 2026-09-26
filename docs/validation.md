# Local validation

Validated on macOS with Go 1.26.2 on 2026-09-06 (native harness):

- `go vet ./...`: passed.
- `go test -race -json ./...`: 34 passing test/subtest records, no failures.
- `go build -o bin/fanisi .`: passed.
- Example dry run: passed; 1,668-byte packet, both source slices included, no API call.
- Missing-response accounting regression: observed red before the fix, green afterward.
- The nested demonstration's `go test ./...` fails its two lower-bound cases as
  intended. It is excluded from the root module's suite.

Integration tests use a local fake API while exercising real scoped edits and
verifier subprocesses. They do not measure current model quality or API latency.
No paid model calls were made to validate this extraction. CI is configured for
Linux and macOS; remote CI has not been run as part of this local validation.

## Composition qualification — 2026-09-26

G0 passed locally on macOS arm64, Go 1.27.1. The tested source is ready for user review; this does not constitute human acceptance of generated products.

| Check | Observed result |
|---|---|
| `gofmt -l .` | No output. |
| `go vet ./...` | Passed. |
| `go test -race -count=1 -timeout 180s -json ./...` | 227 passing test/subtest records: 140 top-level tests and 87 subtests; zero failures; two opt-in skips. |
| Composition coverage | 152 passing `TestCompose*` records; 75 other passing records. |
| `go build -o .fanisi/qualification/fanisi .` | Passed; built executable exercised directly. |
| Fresh tutorial | Passed in a new empty temporary directory; candidate changed from `old` to `new`; result/status `verified_pending_review`; cost/tokens remained unknown. |
| Real local AMSL catalog | 44 records loaded; source pin `b8ded58d5173c797e4d1aed4776aa9a62423892ec63814fb5a27a60889b7383d`. |
| Invalid CLI command | Exit 1. |
| Nonempty tutorial target | Exit 1; existing sentinel preserved. |
| Dependency/native changes | No module dependencies added; model/provider pin unchanged; intentional nested `examples/go-fix` unchanged. |

Skipped: `TestInstalledClaudeRequestShape` requires the opt-in installed-CLI protocol flag; `TestLiveRelayCapturesEarlyGenerationID` requires live relay configuration. Neither was enabled. No live provider/model quality, latency, deployment, or remote peer-adapter qualification is claimed. CI is configured for Linux and macOS; this session did not run remote CI or claim Linux execution.

The root contains one Go package (`go list ./...`); the nested intentionally red example is a separate module. Final checks ran serially below the shared machine load ceiling. The native harness baseline passed 75 records before these changes.

### Review and regression evidence

Headless Cursor authored the foundation, three parallel lanes, CLI integration and correction passes. Coordinator/read-only reviews found and resolved bounded input reads, duplicate JSON keys, symlink-parent escapes, unchecked catalog references, shadow decisions reaching execution, per-capability schema checks, cancellation/deadline coverage, terminal review reversals, workspace concurrency, verifier/post-review scope drift, verifier cache/environment usability, expired idempotent replay, process-group child cleanup, short journal-lock contention, opaque argv preservation, mandatory workspace validation and installed executable symlink compatibility.

Two regressions were observed red before correction: restoring the unbounded import read failed the oversized-import assertion; the first full race run exposed the absolute macOS `/var` workspace-executable regression. The corrected full suite is the result above. The Go executable compatibility test creates its own symlink and runs real `go env GOCACHE`, so it no longer silently skips when Go prepends a canonical binary directory to PATH.

Evidence logs/prompts remain locally under ignored `.fanisi/implementation/`: `final-race-fixed.jsonl`, `tutorial-smoke.txt`, `real-catalog-smoke.json`, `smoke-state.json`, and per-lane Cursor logs. Raw logs are not published. The plan and contract are the durable specification.

### Operational semantics and limits

- CLI artifacts resolve beside their primary configuration; workspace/output fields resolve beside their owning JSON and are canonicalized. Hash and execution use the same convention. Scope paths remain workspace-relative.
- Command arguments are preserved. Workspace executable paths reject symlinks below the canonical workspace root; explicitly selected external/PATH tools may resolve installation symlinks. Platform root aliases are mapped without following links below the workspace root.
- Composition executable JSON is capped at 256 KiB, catalogs at 8 MiB, finite results at 64 KiB and review notes at 16,000 bytes. Regular-file reads reject special files; JSON rejects unknown/duplicate keys and trailing values. Native reader behavior is unchanged.
- `env_names` explicitly opts trusted commands into named variables, including credentials. Fanisi does not serialize those values, but a selected child can print them. Verifiers receive controlled HOME/Go cache locations.
- The local journal is append-only and assumes trusted filesystem ownership. Reservations limit admission estimates, not independently verified provider spending. Unknown usage stays nil/false; failed/uncertain attempts retain reservations.
- Delegates/verifiers are trusted local processes, not an OS sandbox. Ignored files, detached sessions and writes outside the product repository require external isolation.
- Human review attribution is the local CLI invoker, not remote authentication. Agent reviews cannot accept. Successful verification is pending review, not acceptance. Crash/cancel/timeout recovery is explicit, with no automatic retry.
- Live Zatiti/Kazi/Foundry/Griffon adapters, AMSL publication, Jev activation and the iOS preview remain separately scoped work.
