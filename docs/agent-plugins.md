# Agent host plugins for `fanisi compose`

Status: implemented and reviewed. Generated output is committed only after coordinator review.

## What this is

Fanisi exposes eight `fanisi compose` subcommands to Codex, Claude Code and
Cursor as MCP tools plus a user-invoked skill. The plugins are generated from
[`plugins/fanisi.spec.json`](../plugins/fanisi.spec.json) by the AMSL
`amsl-agent-plugin` command (package `cliagent` in the AMSL Go module). That
generator is a proposed AMSL component, not a catalog-approved or released one.

Fanisi takes no Go dependency on it. The generated `.mcp.json` runs the
installed `amsl-agent-plugin serve`, which runs the installed `fanisi` from
`PATH` without a shell. Fanisi's own `run`, `compose` and model pin
(`z-ai/glm-5.3-flash` through Z.AI) are unchanged.

## Exposed tools

`catalog`, `hash`, `validate`, `decide` (`--import` only), `manifest`,
`dispatch`, `status` and `cancel`. Every flag in the spec is one that
`fanisi compose <subcommand>` accepts; `plugins_spec_test.go` runs each tool's
flags through the real compose parser.

Not exposed, deliberately:

- `fanisi run` and `packet`: they call the default paid model.
- `fanisi compose review`: human or agent acceptance stays a separate action by
  the person responsible; the plugin never records review.
- `decide --command`: it would let a tool call choose an executable.
- Any generic shell, executable or configuration editor.

`dispatch` is honest about its power: it runs the delegate named in the
approved ImplementationRequest, an arbitrary trusted local program that can
write the declared paths, use the network and incur provider charges. The MCP
wrapper is not a sandbox or approval engine; host tool approval still applies.
`dispatch` blocks for at most 600 seconds. On timeout the server sends SIGTERM
to the fanisi process group (SIGKILL after a grace period), and Fanisi records
`blocked_uncertain`. `status` and `cancel` can run in parallel tool calls
(up to four concurrent calls). There is no asynchronous completion.

`status` and `cancel` create the empty journal layout if the journal directory
does not exist, so they are declared as writing, not read-only.

## Setup

No public release of either binary is claimed. Build both from source
checkouts and put them on `PATH`:

```sh
# In a Fanisi checkout
go build -o "$HOME/.local/bin/fanisi" .
# In an AMSL Go checkout (github.com/ajent-social/go) that contains cmd/amsl-agent-plugin
go build -o "$HOME/.local/bin/amsl-agent-plugin" ./cmd/amsl-agent-plugin
```

Both `fanisi` and `amsl-agent-plugin` must be on the `PATH` of the host
process, not only your interactive shell. The MCP server resolves `fanisi`
once at startup and requires `fanisi compose --help` to contain the documented
`dispatch` usage line; otherwise it exits with the error on stderr.

Install one generated folder per host. From the repository root:

```sh
# Codex: plugins/generated is a local marketplace named fanisi-compose
codex plugin marketplace add plugins/generated
codex plugin add fanisi-compose@fanisi-compose

# Claude Code, for one session
claude --plugin-dir plugins/generated/claude-code

# Cursor: copy, then reload Cursor
cp -R plugins/generated/cursor ~/.cursor/plugins/local/fanisi-compose
```

The Codex commands change your user Codex configuration and copy the plugin
into Codex's cache; re-run `codex plugin add` after regenerating. Remove with
`codex plugin remove fanisi-compose@fanisi-compose` and
`codex plugin marketplace remove fanisi-compose`.

## Self-application recipe

`scripts/self-apply-agent-plugins.sh RUN_DIR` generates `plugins/generated`
through a real `fanisi compose dispatch`. The recipe also requires `python3`
to encode filesystem paths safely in JSON:

1. Refuses unless the Git workspace is clean and `plugins/generated` is absent.
   It never cleans, stashes or deletes anything; commit authored work first.
2. Writes artifacts, journal and delegate output under `RUN_DIR`, which must be
   absent or empty and outside the repository.
3. Uses an empty normalized catalog labelled as an offline recipe and an empty
   evidence list. The single decision is `product_local` by the operator. No
   catalog status, maturity or adoption is claimed.
4. Pins `product_revision` to the current `HEAD`. Write paths are exactly the
   files `amsl-agent-plugin files` lists, including the Codex marketplace
   catalog; the spec, this script, `plugins/README.md`, this document and
   [the plan](plans/agent-host-plugins.md) are protected inputs.
5. Dispatches `amsl-agent-plugin generate` as the delegate. Fanisi's
   independent verifier runs `amsl-agent-plugin check`, which compares every
   byte and rejects missing, extra or symlinked files.
6. Requires `verified_pending_review`, prints the artifact and result hashes,
   and re-runs `check` read-only. Acceptance and committing the generated
   output remain separate, reviewed steps.

To regenerate after a spec change, first commit the spec. The generated files
are tracked, so a fresh checkout still contains them and an uncommitted removal
fails the clean-workspace guard. Prepare a clean revision without the old output
in an **isolated worktree**:

```sh
git worktree add -b regenerate-agent-plugins ../fanisi-agent-plugin-regeneration HEAD
cd ../fanisi-agent-plugin-regeneration
git rm -r plugins/generated
git commit -m "Prepare clean revision for plugin regeneration"
sh scripts/self-apply-agent-plugins.sh ../fanisi-agent-plugin-run
# Inspect the generated files and verification result before committing.
git add plugins/generated
git diff --cached
git commit -m "Regenerate agent host plugins through composition"
```

Choose unused worktree, branch and run-directory names. Integrate the completed
regeneration, including its final output; do not merge the intermediate removal
commit alone. The script never overwrites existing output. The opt-in
`python3 scripts/test-self-apply-agent-plugins.py` exercises this clean-revision
workflow with real installed binaries in a temporary repository.

## Evidence

Recorded per run in the result summary for the change that introduced this
file: the dispatch journal state and result hash, the independent `check`, and
MCP calls made through the official Go SDK client against the real `fanisi`
binary. Provider usage for these runs is unknown (`usage_complete: false`)
because the delegate makes no provider calls and reports none.

Host qualification gates, completed and remaining, are tracked in
[plans/agent-host-plugins.md](plans/agent-host-plugins.md). No generated
plugin has yet been invoked from a live Codex, Claude Code or Cursor session.
