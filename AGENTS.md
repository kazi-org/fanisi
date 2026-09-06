# Working on Fanisi

Fanisi is a small Go coding harness for measured changes to existing repositories.
Prefer the standard library and direct code over new frameworks. Use the graph
when it covers this repository; fall back to targeted source reads when it does not.

- Run gofmt, go vet ./..., and go test -race ./... after relevant code changes.
- Keep the default test suite offline. Fake API handlers belong only in tests.
- Keep model/provider selection pinned unless the user explicitly changes it.
- Never copy credentials or raw authenticated traffic into fixtures or commits.
- A passing verifier is verified_pending_review, never automatic human acceptance.
- Preserve complete task requirements; trim optional context with explicit omissions.
- Keep provider token/cost totals separate from byte-based admission estimates.
- Test unknown usage, tool failures, path boundaries, and terminal verdicts through
  the real run boundary. Do not replace those tests with implementation snapshots.
- The nested examples/go-fix module intentionally starts red. It is demonstration
  input, not an incomplete production implementation; don't silently fix the fixture.
