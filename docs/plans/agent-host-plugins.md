# Plan: agent host plugins for `fanisi compose`

Status: proposed. Nothing here accepts generated output, promotes the AMSL
generator or claims catalog status. Setup, exposed tools and the
self-application recipe are in [../agent-plugins.md](../agent-plugins.md).

## Decision

Describe the eight safe `fanisi compose` subcommands in a reviewed spec
(`plugins/fanisi.spec.json`) and let the external AMSL `amsl-agent-plugin`
generator render Codex, Claude Code and Cursor plugins plus a local Codex
marketplace. Generate them only through a real `fanisi compose dispatch`
(`scripts/self-apply-agent-plugins.sh`), with `amsl-agent-plugin check` as the
independent verifier. The success state is `verified_pending_review`.

Constraints kept:

- No Go dependency on AMSL; both binaries are installed on `PATH`.
- `run`, `packet`, `review` and `decide --command` stay unexposed.
- Native model pin (`z-ai/glm-5.3-flash` through Z.AI) and existing `run` and
  `compose` behaviour are unchanged.
- `plugins/generated/` is never edited by hand and is committed only by the
  coordinator after review.

## Steps

1. Done: spec and `plugins_spec_test.go` (every flag accepted by the real
   compose parser; probe text matches `fanisi compose --help`).
2. Done: offline self-application script with an empty labelled catalog, a
   `product_local` decision, exact write paths from `amsl-agent-plugin files`
   and protected inputs.
3. Done: self-application runs reached `verified_pending_review` with the
   independent check passing, first for the three host folders and again after
   the generator added `plugins/generated/.agents/plugins/marketplace.json`.
   Hashes, journal state and source commits for each run are recorded in the
   coordinator's result summary, not in this file (it is a protected input of
   the run).
4. Done: coordinator inspected and committed `plugins/generated/`.
   Subsequent independent headless review and merge qualification are recorded
   in [the validation record](../agent-plugins-validation.md).

## Host qualification gates

| Gate | State |
| --- | --- |
| MCP calls through the official Go SDK client against the real `fanisi` (catalog, hash, validate, status, errors, concurrent dispatch with status and cancel) | complete |
| Independent stdio probe of generated `codex/.mcp.json` (eight tools, catalog success, missing-path tool error) | complete (coordinator) |
| `claude plugin validate --json` on `generated/claude-code` | complete (coordinator) |
| Codex CLI 0.157.1 `plugin marketplace add`, `plugin add fanisi-compose@fanisi-compose`, `plugin list`, `mcp list` in an isolated Codex home with outbound network denied | complete |
| Codex live session: skill listed and user-invoked only, tools list, `catalog` succeeds, a failing call shows stderr | remaining |
| Claude Code live session via `claude --plugin-dir`: `/mcp` connected, same calls | remaining |
| Cursor local plugin load: plugin, skill and MCP server appear, same calls | remaining |
| Full repository vet and race tests under the build lease | remaining (coordinator) |
| Coordinator review and commit of `plugins/generated/` | remaining (coordinator) |

Each remaining host gate needs the maintainer's own authenticated host and
changes that host's user configuration, so it is run by hand, recorded with
the host version and date, and undone afterwards (for Codex:
`codex plugin remove fanisi-compose@fanisi-compose` and
`codex plugin marketplace remove fanisi-compose`).
