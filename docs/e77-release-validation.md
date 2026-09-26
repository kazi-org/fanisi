# E77 bounded evaluation runner release validation

Date: 2026-09-07. Source PR [#8](https://github.com/kazi-org/fanisi/pull/8)
merged as `7bfe862087344c37a5ff1fc599fb84500dfb74df` after independent read-only
review and Linux/macOS CI. The immutable
[v0.1.1 release](https://github.com/kazi-org/fanisi/releases/tag/v0.1.1) contains
that source commit. The merged commit's
[CI run](https://github.com/kazi-org/fanisi/actions/runs/34128897218) passed.

## Executed checks

- Local `go test -race -json ./...`: **116 test cases passed**, no failures.
  Three opt-in tests were skipped: `TestInstalledClaudeRequestShape`,
  `TestLiveRelayCapturesEarlyGenerationID`, and the separately selected
  `TestInstalledKaziEvaluation`. `go vet ./...` and isolated build passed.
- Genuine red against downloaded v0.1.0: the old binary admitted a protected-file
  modification hidden by `git update-index --assume-unchanged`. The new audit
  rejects it. Additional regressions cover skip-worktree, ignored files, modes,
  symlinks, artifact drift, clean-filter transformation and early cancellation.
- The packaged and subsequently downloaded native v0.1.1 binary each passed
  **18 offline accepted-change ledger checks**. The synthetic ledger retains
  $0.85 known variable cost separately from $7 study/tooling investment.
- The packaged and downloaded native binary each passed **four installed Kazi
  integration cases** against downloaded Kazi v1.297.0: first-pass success,
  failed-first repair, rejected unexpected instructions, and active-worker
  timeout after an independently valid fix. The timeout checks worker and child
  PID death, failed dispatch retention and incomplete relay metadata. Its
  authenticated relay request uses a blocking localhost proxy; no provider
  inference request is sent. No scope-tree AGENTS.md or CLAUDE.md was generated.

All four published archives were downloaded and checked against their published
SHA256SUMS. The native macOS arm64 archive digest is:

```text
4965fe0c58df46e2f3a378fb1295fede06931039a8c81b2dd37c76d1a80fc8f8
```

BUILD-INFO.json reports version `0.1.1`, the source commit above, Go `1.27.1`,
`darwin/arm64` and CGO disabled. The native binary reports `fanisi 0.1.1`;
its embedded Go module version is `v0.1.1` and its VCS revision matches the
reviewed source. The version tag was created before packaging. Linux arm64,
Linux amd64 and macOS amd64 archives were cross-built and checksum-verified;
this record does not claim those three binaries were executed locally.

A later documentation CI run exposed a cancellation-fixture readiness race:
the worker could be cancelled before writing its child PID. The fixture now
waits for that PID before cancelling. This test-only correction does not change
the released binary or its published assets.

## Scope of the result

The runner now produces ordinary independently graded attempts for the
`kazi-claude` arm and uses the existing review/landing ledger. It reserves one
cumulative deadline and turn/estimated-dollar allowance across at most two
Kazi sessions, with exact trusted controller-artifact auditing. Direct Claude
uses one session; equal actual dispatch counts are not claimed. Ordinary failed
executions can preserve independently valid candidates in both arms.

This is an offline runner qualification, not a paid study or a savings result.
Claude turns and CLI dollar estimates do not cap provider requests or invoice
spend. Provider request/output/price admission remains a separate qualification
before any paid pilot. No private project fixtures were published and no active
installation was replaced. Reproduce the fixture using the isolated-binary
command in [evaluation.md](evaluation.md#offline-installed-integration-check).
