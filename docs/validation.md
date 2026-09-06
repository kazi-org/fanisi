# Local validation

Validated on macOS with Go 1.26.2 on 2026-09-06:

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
