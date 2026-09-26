# Agent host plugins for `fanisi compose`

Status: proposed. Generated output is committed only after coordinator review.

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

Then install one generated folder per host as its README describes. The MCP
server resolves `fanisi` once at startup and requires `fanisi compose --help`
to contain the documented `dispatch` usage line; otherwise it exits with the
error on stderr.

## Self-application recipe

`scripts/self-apply-agent-plugins.sh RUN_DIR` generates `plugins/generated`
through a real `fanisi compose dispatch`:

1. Refuses unless the Git workspace is clean and `plugins/generated` is absent.
   It never cleans, stashes or deletes anything; commit authored work first.
2. Writes artifacts, journal and delegate output under `RUN_DIR`, which must be
   absent or empty and outside the repository.
3. Uses an empty normalized catalog labelled as an offline recipe and an empty
   evidence list. The single decision is `product_local` by the operator. No
   catalog status, maturity or adoption is claimed.
4. Pins `product_revision` to the current `HEAD`. Write paths are exactly the
   files `amsl-agent-plugin files` lists; the spec, this script,
   `plugins/README.md` and this document are protected inputs.
5. Dispatches `amsl-agent-plugin generate` as the delegate. Fanisi's
   independent verifier runs `amsl-agent-plugin check`, which compares every
   byte and rejects missing, extra or symlinked files.
6. Requires `verified_pending_review`, prints the artifact and result hashes,
   and re-runs `check` read-only. Acceptance and committing the generated
   output remain separate, reviewed steps.

To regenerate after a spec change, commit the spec, then run the script in a
fresh checkout (or after removing `plugins/generated` yourself) and review the
diff. The script does not overwrite existing output.

## Evidence

Recorded per run in the result summary for the change that introduced this
file: the dispatch journal state and result hash, the independent `check`, and
MCP calls made through the official Go SDK client against the real `fanisi`
binary. Provider usage for these runs is unknown (`usage_complete: false`)
because the delegate makes no provider calls and reports none.

Not yet verified: installing any generated folder in Codex, Claude Code or
Cursor and invoking it from the host UI. Packaging checks are not host
installation evidence.
