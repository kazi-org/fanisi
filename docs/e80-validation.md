# E80 offline accounting validation

Source validation on 2026-09-07 at commit `61fc721`:

- `go test -race -json ./...`: 90 passing test cases/subcases, zero failures;
  root package passed. The intentionally red nested example module is excluded
  by Go's module boundary, unchanged.
- `go vet ./...`, `gofmt -l .`, `git diff --check`: passed/empty output.
- Temporary standalone `go build` installation reports `fanisi 0.1.0-dev`.
  Repeated CLI effort import followed by a separate report process returned one
  reviewer record, 20 measured seconds and null total dollars. No active binary
  was replaced. A committed subprocess regression repeats the import/reload path.
- Temporary Git fixtures cover equivalent squash, changed landed content,
  unmerged valid commit, missing merge identity, wrong repository, amended patch,
  intervening scoped baseline, duplicate import and report-visible revocation.
- Synthetic arithmetic: two attempts including a failure, $0.75 known variable
  cost, duplicate provider request counted once, 110 input-plus-output tokens,
  one assisted landed task, zero autonomous tasks, null autonomous ratio and
  30 seconds request-to-landing elapsed.

Four deliberate mutations were applied separately and restored. Each produced
an actual failing test: bypassing scoped content identity accepted changed code;
counting a duplicate receipt doubled request tokens and increased known cost;
adding reasoning again changed 110 tokens to 114; omitting the failed attempt
reduced attempts to one and known cost to $0.25. The identity mutation was rerun
after target-branch containment was added, with the changed commit present on
its declared target branch so that the content guard was independently tested.

Independent review requested changes to target-branch evidence, priced partial
coverage, shared study cost separation and stable coordinator source identity;
those changes and regression tests are included. Linux/macOS CI and final
independent review remain the PR landing gates. This validation establishes
accounting behavior, not savings or general model capability. No paid inference
or service deployment was used.
