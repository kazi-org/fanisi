# E77 provider admission release validation

Date: 2026-09-07. Source [PR #10](https://github.com/kazi-org/fanisi/pull/10)
merged as `cbcd838872b2a43d9316eafac70a0027217e5919` after independent
read-only review and Linux/macOS CI. The merged source
[CI run](https://github.com/kazi-org/fanisi/actions/runs/34133465427) passed.
The [v0.1.2 release](https://github.com/kazi-org/fanisi/releases/tag/v0.1.2)
contains that commit; its tag was fixed before packaging.

## Executed checks

- `go test -race -json ./...`: **136 named test pass outcomes**, zero failures.
  Five opt-in tests were skipped: `TestInstalledAdmissionBudget`,
  `TestInstalledClaudeRequestShape`, `TestInstalledClaudeAdmissionRun`,
  `TestInstalledKaziEvaluation`, and `TestLiveRelayCapturesEarlyGenerationID`.
  `go vet ./...` and the isolated build passed.
- Four separately selected installed Claude protocol cases passed against
  Claude 2.1.263 and a rejecting localhost endpoint: default request shape,
  bounded shape, admission shape, and the actual admission-enabled runner.
- The packaged and downloaded native binaries each passed **18 offline ledger
  checks** ($0.85 known variable cost, separately retained $7 tooling cost)
  and **seven installed integration cases**: direct request admission, Kazi
  cumulative admission, old-bridge rejection, first-pass fix, failed-first
  repair, unexpected-instruction rejection, and active-worker timeout.
- Direct admission forwarded exactly 24 transport attempts; Kazi reserved
  12 + 12 despite first-dispatch failure. Retries and failures consumed slots.
  Localhost proxy rejection prevented provider traffic. The old v0.1.1 bridge
  was refused before worker launch. A preserved pre-handshake candidate
  genuinely failed this regression by forwarding 26 attempts; the fixed
  candidate forwarded zero through that incompatible bridge.

All four published archives were downloaded and verified against SHA256SUMS.
The native macOS arm64 archive SHA256 is:

```text
b2e67ba638a8ad3e0aff87135af7aa82918b330839b6f19f7b56b3c727510f6a
```

The downloaded native executable SHA256 is:

```text
f76fd9f42fd111f893feade7ca82c22fdebb3f757ac12307915e664fbaf6ac0e
```

BUILD-INFO.json records version `0.1.2`, the source commit above, Go `1.27.1`,
`darwin/arm64`, and CGO disabled. The binary reports `fanisi 0.1.2`.
Linux amd64, Linux arm64, and macOS amd64 were cross-built and checksum-verified;
only the native macOS arm64 binary was executed in this qualification.

## Contract and limits

The optional admission block caps forwarded requests and output tokens and
pins Z.AI routing without fallback, forwarding prompt/completion `max_price`
limits. Admission counters and refusals remain separate from actual receipt
cost. A safe pre-forward refusal is an execution event: an independently valid
candidate can still await review. Controller-integrity violations fail the
candidate. Unresolved forwarded-request receipts cannot establish full cost.

Both arms disable prompt caching when admission is enabled because no
cache-write pricing contract is established. Cache directives and unsupported
billable extensions are rejected. The installed CLI's known keep-all context
no-op is normalized away, and optional outbound beta headers are stripped.
Behavior without the block remains unchanged. These common admission settings
are explicit experimental configuration, not a claim that all CLI defaults
are preserved.

These offline checks establish local enforcement and routing payloads, not
live provider enforcement, invoice guarantees, paid-pilot readiness, or a
savings result. Private evaluator qualification and reference controls remain
separate. No paid calls, private project fixtures, or active installation
changes were used. Reproduction commands and the exact configuration schema
are in [evaluation.md](evaluation.md#optional-claude-provider-request-admission).
