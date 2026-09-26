# Fanisi agent host plugins

`fanisi.spec.json` is the reviewed description of the `fanisi compose` commands
exposed to coding-agent hosts. The AMSL `amsl-agent-plugin` generator renders
it into three independent plugin folders under `generated/`:

- `generated/codex` (`.codex-plugin/plugin.json`)
- `generated/claude-code` (`.claude-plugin/plugin.json`)
- `generated/cursor` (`.cursor-plugin/plugin.json`)

Each folder has a user-invoked `fanisi-compose` skill, a `.mcp.json` that starts
`amsl-agent-plugin serve --spec-json ...`, a bundled copy of the spec, a README
with installation steps and `amsl-provenance.json` with file digests.

Do not edit `generated/` by hand. It is produced by a real `fanisi compose
dispatch` through `scripts/self-apply-agent-plugins.sh`; see
[docs/agent-plugins.md](../docs/agent-plugins.md) for setup, the exact recipe,
what was verified and what was not.
