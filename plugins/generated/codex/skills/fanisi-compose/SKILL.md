---
name: fanisi-compose
description: "Drive Fanisi's offline composition workflow (catalog, hash, validate, decide, manifest, dispatch, status, cancel) with the installed fanisi CLI."
---

# Compose with Fanisi

Use this workflow when the user asks to inspect a composition catalog, hash or validate composition artifacts, import a decision, build an application manifest, dispatch an approved implementation attempt, or check or cancel an attempt. Fanisi is the canonical engine: do not imitate its validation, dispatch or verification in the host model.

## Before calling tools

1. Work in the directory where the host started the MCP server. The primary path of each call (`request`, `index`, `file`, `journal`) resolves against that directory; other artifact paths resolve beside the request file, following Fanisi's own rule. Prefer paths the user gave you.
2. Use only artifacts the user supplied or approved. Do not author or edit `delegate_argv`, `verify_command`, `env_names`, workspace or scope paths in an ImplementationRequest yourself: those are operator trust decisions.
3. Never read, print or pass secret values. Fanisi and its delegates use their own configuration.

## Workflow

1. `catalog` lists the offline catalog. `hash` prints the canonical digest of a request, decision, manifest, implementation request or result without modifying files.
2. `decide` imports a finite-choice result file authored elsewhere (`--import`). The command backend is not exposed.
3. `manifest` builds an application manifest from an explicit bindings draft and writes it with exclusive create. `validate` checks the whole bundle and prints `ok`.
4. `dispatch` runs one attempt. Call it only after the user has explicitly approved the exact reviewed request, decisions, manifest and implementation request in this conversation. It executes the delegate named in `delegate_argv`, an arbitrary trusted local program chosen by the operator, which can write the declared write paths, use the network and incur provider charges; then Fanisi runs the independent verifier. Fanisi requires a clean Git workspace whose HEAD equals `product_revision`.
5. `dispatch` blocks until the attempt finishes or the tool timeout expires. On timeout the MCP server terminates the fanisi process group (SIGTERM first) and Fanisi records `blocked_uncertain`; use `status` to read the recorded state. While a dispatch runs, `status` and `cancel` can be called from separate tool calls.
6. `cancel` needs the attempt id and fence token from the approved implementation request. A canceled attempt ends `blocked_uncertain` with no automatic retry.

## Results

A successful attempt is `verified_pending_review`, which is not acceptance. Human or agent review (`fanisi compose review`) is intentionally not exposed; ask the user to run it themselves after inspecting the attempt. Report usage and cost only as Fanisi records them; unknown usage stays unknown. Native `fanisi run` (which calls the default paid model) is not available through this plugin.

## MCP tools

The `fanisi-compose` MCP server (`amsl-agent-plugin serve`) exposes only these declared commands of the installed `fanisi` CLI. Each call runs `fanisi` directly, without a shell, in the directory where the host started the server. Every argument value becomes exactly one command-line argument.

- `catalog`: runs `fanisi compose catalog` with `index` (required string, --index), `family` (optional string, --family), `id` (optional string, --id), `revision` (optional string, --revision), `json` (optional boolean, --json). Run `fanisi compose catalog` on an offline catalog index. Optionally filter by family, or look up one capability by id and revision (supply both).
  Effects: read-only, idempotent, no network declared, no cost declared. Timeout 60s. Reads the catalog file only.
- `hash`: runs `fanisi compose hash` with `file` (required string, --file), `kind` (required string, --kind). Run `fanisi compose hash` to print the canonical SHA-256 of one composition record.
  Effects: read-only, idempotent, no network declared, no cost declared. Timeout 60s. Reads the record only; files are not modified.
- `validate`: runs `fanisi compose validate` with `request` (required string, --request), `decisions` (required string, --decisions), `manifest` (required string, --manifest), `catalog` (required string, --catalog), `evidence` (required string, --evidence). Run `fanisi compose validate` on a request, decisions directory, manifest, catalog and evidence. Prints ok or the first validation error.
  Effects: read-only, idempotent, no network declared, no cost declared. Timeout 60s. Reads the bundle only.
- `decide`: runs `fanisi compose decide` with `request` (required string, --request), `import` (required string, --import), `out` (optional string, --out). Run `fanisi compose decide --import` to validate a finite-choice result authored elsewhere against its request. Prints the result, or writes it to out.
  Effects: writes, no network declared, no cost declared. Timeout 60s. Writes a new file only when out is given; never overwrites.
- `manifest`: runs `fanisi compose manifest` with `request` (required string, --request), `decisions` (required string, --decisions), `draft` (required string, --draft), `out` (required string, --out), `catalog` (required string, --catalog), `evidence` (required string, --evidence). Run `fanisi compose manifest` to build and validate an application manifest from an explicit bindings draft, written with exclusive create.
  Effects: writes, no network declared, no cost declared. Timeout 60s. Writes one new manifest file; never overwrites.
- `dispatch`: runs `fanisi compose dispatch` with `request` (required string, --request), `catalog` (required string, --catalog), `evidence` (required string, --evidence), `decisions` (required string, --decisions), `manifest` (required string, --manifest), `impl` (required string, --impl), `journal` (required string, --journal). Run `fanisi compose dispatch` for one user-approved implementation request. Executes the operator-chosen delegate and verifier, journals every transition, and prints the attempt result. Blocks until the attempt ends or the tool times out.
  Effects: writes, destructive, may use the network, may incur cost. Timeout 600s. Runs an arbitrary trusted delegate named by the approved artifacts. It can modify the declared write paths, use the network and incur provider charges. Fanisi's scope checks are not an OS sandbox, and this wrapper is not an approval engine.
- `status`: runs `fanisi compose status` with `journal` (required string, --journal), `attempt` (optional string, --attempt). Run `fanisi compose status` to print recorded attempt results from a journal, optionally for one attempt.
  Effects: writes, idempotent, no network declared, no cost declared. Timeout 60s. Reads journal records; creates the empty journal directory layout if it does not exist.
- `cancel`: runs `fanisi compose cancel` with `journal` (required string, --journal), `attempt` (required string, --attempt), `fence` (required string, --fence). Run `fanisi compose cancel` to request cancellation of one attempt using its fence token. The attempt ends blocked_uncertain with no automatic retry.
  Effects: writes, destructive, no network declared, no cost declared. Timeout 60s. Writes a cancel request that stops in-flight delegate or verifier work, leaving the attempt blocked_uncertain.

## Boundaries

- Tool schemas and annotations describe declared effects. They are not authorization, a sandbox or an approval step. Keep the host's normal tool-approval settings.
- `fanisi` runs with this session's environment and configuration, exactly as if invoked directly. Its stdout and stderr are returned verbatim up to 1048576 bytes combined (truncation is flagged) and may contain anything it prints, including secrets. Never place secret values in arguments.
- If the server fails to start because `fanisi` is missing or incompatible, or a call fails, report the error. Do not substitute another tool or simulate a result.
- Calls stop after at most 600 seconds (the process group is terminated). At most 4 calls run at once; extra calls are rejected, not queued.
