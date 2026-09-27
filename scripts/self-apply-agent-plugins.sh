#!/bin/sh
# scripts/self-apply-agent-plugins.sh — generate Fanisi's own agent host plugins
# through a real `fanisi compose dispatch`, offline.
#
# Usage: sh scripts/self-apply-agent-plugins.sh RUN_DIR
#
# RUN_DIR must be absent or empty and outside this repository; it receives the
# composition artifacts, journal and delegate output. The only product writes
# are the files `amsl-agent-plugin files` lists under plugins/generated.
#
# Requires `fanisi`, `amsl-agent-plugin` and `python3` on PATH (see docs/agent-plugins.md).
# The workspace must be clean at a committed HEAD; this script never cleans,
# stashes or deletes anything. The catalog is an empty, explicitly labelled
# offline catalog and the evidence list is empty: the decision is product_local
# and makes no AMSL catalog, maturity or adoption claim. A passing run ends
# verified_pending_review; review and commit remain separate human steps.
set -eu

RUN=${1:-}
if [ -z "$RUN" ]; then
  echo "usage: $0 RUN_DIR" >&2
  exit 2
fi
fail() {
  echo "self-apply: $*" >&2
  exit 1
}

REPO=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd -P)
SPEC_REL=plugins/fanisi.spec.json
OUT_REL=plugins/generated

command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"
command -v fanisi >/dev/null 2>&1 || fail "fanisi is not on PATH"
command -v amsl-agent-plugin >/dev/null 2>&1 || fail "amsl-agent-plugin is not on PATH"

if [ -n "$(git -C "$REPO" status --porcelain=v1 -uall)" ]; then
  fail "workspace has uncommitted or untracked files; commit your own work first (nothing is cleaned for you)"
fi
[ ! -e "$REPO/$OUT_REL" ] || fail "$OUT_REL already exists; regenerate from a fresh checkout and compare instead of overwriting"
[ -f "$REPO/$SPEC_REL" ] || fail "$SPEC_REL is missing"

case "$RUN" in
  /*) RUN_ABS=$RUN ;;
  *) RUN_ABS="$(pwd -P)/$RUN" ;;
esac
case "$RUN_ABS/" in
  "$REPO/"*) fail "RUN_DIR must be outside the repository" ;;
esac
if [ -e "$RUN" ]; then
  [ -d "$RUN" ] || fail "RUN_DIR exists and is not a directory: $RUN"
  [ -z "$(ls -A "$RUN")" ] || fail "RUN_DIR must be absent or empty: $RUN"
fi
mkdir -p "$RUN"
RUN=$(CDPATH= cd -- "$RUN" && pwd -P)
case "$RUN/" in
  "$REPO/"*) fail "RUN_DIR must be outside the repository" ;;
esac

ART="$RUN/artifacts"
JOURNAL="$RUN/journal"
OUT="$RUN/output"
mkdir -p "$ART/decisions" "$OUT"

# Paths are JSON values, not shell-escaped strings. Preserve quotes, backslashes
# and other valid filesystem characters when constructing composition records.
REPO_JSON=$(python3 -c 'import json, sys; print(json.dumps(sys.argv[1]))' "$REPO")
OUT_JSON=$(python3 -c 'import json, sys; print(json.dumps(sys.argv[1]))' "$OUT")

HEAD=$(git -C "$REPO" rev-parse HEAD)
DEADLINE=$(date -u -v+30M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+30 minutes' +%Y-%m-%dT%H:%M:%SZ)

# Exact write paths come from the generator itself, as a JSON array.
WRITES=$(amsl-agent-plugin files --spec "$REPO/$SPEC_REL" --out "$OUT_REL" |
  awk 'BEGIN { printf "[" } { printf "%s\"%s\"", (NR > 1 ? ", " : ""), $0 } END { printf "]" }')
[ "$WRITES" != "[]" ] || fail "generator listed no files"
PROTECTED='["plugins/fanisi.spec.json", "plugins/README.md", "scripts/self-apply-agent-plugins.sh", "docs/agent-plugins.md", "docs/plans/agent-host-plugins.md"]'
for p in plugins/fanisi.spec.json plugins/README.md scripts/self-apply-agent-plugins.sh docs/agent-plugins.md docs/plans/agent-host-plugins.md; do
  [ -f "$REPO/$p" ] || fail "protected input $p is missing"
done

cat > "$ART/catalog.json" <<'EOF'
{
  "schema_version": 1,
  "source_label": "offline self-application recipe: empty catalog; no AMSL catalog, maturity or reuse claims",
  "records": []
}
EOF
printf '[]\n' > "$ART/evidence.json"
PIN=$(shasum -a 256 "$ART/catalog.json" | awk '{print $1}')

cat > "$ART/request.json" <<EOF
{
  "schema_version": 1,
  "request_id": "req-agent-plugins-self-apply",
  "parent_id": "parent-agent-plugins-self-apply",
  "product_revision": "$HEAD",
  "catalog_pin": "$PIN",
  "requirements": [
    "Generate Codex, Claude Code and Cursor plugin folders for the fanisi compose CLI from plugins/fanisi.spec.json with the installed amsl-agent-plugin generator.",
    "Write only the enumerated plugins/generated files; the spec, this script and its documentation are protected.",
    "The independent verifier runs amsl-agent-plugin check against the same spec; success is verified_pending_review, not acceptance."
  ],
  "behaviors": [
    {"behavior_id": "agent-host-plugins", "required": true, "contract": "plugins/fanisi.spec.json"}
  ],
  "workspace": $REPO_JSON,
  "write_paths": $WRITES,
  "protected_paths": $PROTECTED,
  "authority_ref": "operator:agent-plugins-self-application",
  "deadline": "$DEADLINE",
  "budget": {
    "max_attempts": 1,
    "max_seconds": 300,
    "max_estimated_usd": 0.01,
    "reserved_usd": 0,
    "currency_note": "admission ceiling only; offline local generation makes no provider calls"
  }
}
EOF

fanisi compose catalog --index "$ART/catalog.json" >/dev/null
REQ_HASH=$(fanisi compose hash --file "$ART/request.json" --kind request)

cat > "$ART/decisions/agent-host-plugins.json" <<EOF
{
  "schema_version": 1,
  "decision_id": "dec-agent-host-plugins",
  "request_hash": "$REQ_HASH",
  "behavior_id": "agent-host-plugins",
  "verdict": "product_local",
  "owner": "operator",
  "non_goals": ["AMSL catalog admission", "maturity promotion", "host installation evidence"],
  "compat_notes": "Uses the proposed (not catalog-approved) amsl-agent-plugin generator as an external executable; Fanisi takes no Go dependency on it.",
  "consumer_checks": ["amsl-agent-plugin check --spec plugins/fanisi.spec.json --out plugins/generated"],
  "provenance": {"source": "operator", "author": "agent-plugins-self-application"}
}
EOF
DEC_HASH=$(fanisi compose hash --file "$ART/decisions/agent-host-plugins.json" --kind decision)

cat > "$ART/draft.json" <<EOF
{
  "schema_version": 1,
  "manifest_id": "man-agent-host-plugins",
  "bindings": [{
    "behavior_id": "agent-host-plugins",
    "decision_hash": "$DEC_HASH",
    "kind": "local_planned",
    "target_ref": "$OUT_REL"
  }],
  "dependency_edges": [],
  "local_policies": ["generated output is committed only after coordinator review"]
}
EOF

fanisi compose manifest \
  --request "$ART/request.json" \
  --decisions "$ART/decisions" \
  --draft "$ART/draft.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json" \
  --out "$ART/manifest.json"
MAN_HASH=$(fanisi compose hash --file "$ART/manifest.json" --kind manifest)

cat > "$ART/impl.json" <<EOF
{
  "schema_version": 1,
  "attempt_id": "att-agent-plugins-self-apply",
  "parent_id": "parent-agent-plugins-self-apply",
  "manifest_hash": "$MAN_HASH",
  "request_hash": "$REQ_HASH",
  "fence_token": "fence-agent-plugins-self-apply",
  "owner": "operator",
  "write_paths": $WRITES,
  "protected_paths": $PROTECTED,
  "verify_command": ["amsl-agent-plugin", "check", "--spec", "$SPEC_REL", "--out", "$OUT_REL"],
  "delegate_argv": ["amsl-agent-plugin", "generate", "--spec", "$SPEC_REL", "--out", "$OUT_REL"],
  "workspace": $REPO_JSON,
  "output_dir": $OUT_JSON,
  "reserved_usd": 0,
  "max_seconds": 120,
  "deadline": "$DEADLINE",
  "idempotency_key": "parent-agent-plugins-self-apply|$REQ_HASH|generate-agent-host-plugins"
}
EOF
IMPL_HASH=$(fanisi compose hash --file "$ART/impl.json" --kind impl)

fanisi compose validate \
  --request "$ART/request.json" \
  --decisions "$ART/decisions" \
  --manifest "$ART/manifest.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json"

set +e
fanisi compose dispatch \
  --request "$ART/request.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json" \
  --decisions "$ART/decisions" \
  --manifest "$ART/manifest.json" \
  --impl "$ART/impl.json" \
  --journal "$JOURNAL" > "$RUN/dispatch-result.json"
DISPATCH_EXIT=$?
set -e
cat "$RUN/dispatch-result.json"

RESULT="$JOURNAL/attempts/att-agent-plugins-self-apply/result.json"
[ -f "$RESULT" ] || fail "dispatch exited $DISPATCH_EXIT without a journaled result"
RESULT_HASH=$(fanisi compose hash --file "$RESULT" --kind result)
fanisi compose status --journal "$JOURNAL" --attempt att-agent-plugins-self-apply > "$RUN/status.json"

echo "head=$HEAD"
echo "request_sha256=$REQ_HASH"
echo "decision_sha256=$DEC_HASH"
echo "manifest_sha256=$MAN_HASH"
echo "impl_sha256=$IMPL_HASH"
echo "result_sha256=$RESULT_HASH"
echo "journal=$JOURNAL"
[ "$DISPATCH_EXIT" -eq 0 ] || fail "dispatch exited $DISPATCH_EXIT"
grep -q '"state": "verified_pending_review"' "$RUN/dispatch-result.json" || fail "attempt did not reach verified_pending_review"

# Independent re-check outside the harness, read-only.
(cd "$REPO" && amsl-agent-plugin check --spec "$SPEC_REL" --out "$OUT_REL")
echo "self-apply ok: state=verified_pending_review (not accepted; review before committing $OUT_REL)"
