# Fanisi agent plugin qualification — 2026-09-26

## Local results

- `gofmt -l .`: clean; `go vet ./...`: passed.
- `go test -race -count=1 -timeout 240s -json ./...`: 320 passing test/subtest
  records (171 top-level), zero failures and six opt-in skips. These are the
  installed admission, installed request-shape, installed admission run,
  optional reference catalog, installed evaluation and live relay tests.
- Build and all 18 offline release-smoke checks passed. Smoke-test costs are
  synthetic fixtures, not actual spending.
- The AMSL dependency implementation passed 348 race-enabled test/subtest records
  across its full module. A race in its test stderr capture was fixed and
  requalified; production runtime code was unchanged.

## Self-generation and protocol evidence

Fanisi actually dispatched the AMSL generator from clean pinned HEAD
`8e0afd75c9e3a1e0320d6b65fe9fda08e9247bc7`. The independent generator `check`
verifier passed. Result state: `verified_pending_review`, never human acceptance.
Result SHA-256: `b821445de885710345c916510313644d660a7b2f343026507e8f3cc5493a9e36`.
The 20 generated files include all three plugin packages and a local Codex
marketplace. They were produced by dispatch, not hand-authored after verification.
Provider usage remains unknown in the dispatch record; generation made no model
calls. The native model/provider pin and red nested demonstration are unchanged.

Independent stdio probes launched each generated MCP configuration with the
built binaries. All eight tools were listed; catalog calls succeeded and missing
catalog paths failed visibly. Missing arguments, unknown arguments and wrong
types were rejected. Shell-looking path values did not execute. The real local
AMSL catalog also loaded through MCP: 44 records, pin
`b8ded58d5173c797e4d1aed4776aa9a62423892ec63814fb5a27a60889b7383d`.

Claude Code's strict plugin validator passed without warnings. The real Codex
CLI registered and installed the generated marketplace in temporary settings
with network blocked, leaving the normal profile untouched during qualification.
No interactive agent/UI invocation in any host or remote CI run is claimed.

The generation journal, protocol responses and suite logs are retained under
ignored `.fanisi/implementation/`. The reproducible recipe and installation
commands are in [the guide](agent-plugins.md) and [plugin README](../plugins/README.md).
