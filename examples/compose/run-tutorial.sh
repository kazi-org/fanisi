#!/bin/sh
# examples/compose/run-tutorial.sh — offline composition tutorial on a throwaway Git workspace.
# Usage: sh examples/compose/run-tutorial.sh /path/to/absent-or-empty-dir
# Example/test scenario only — not a claim about live AMSL adoption or provider qualification.
# Synthetic evidence below is explicitly labelled as a test scenario.
set -eu

ROOT=${1:-}
if [ -z "$ROOT" ]; then
  echo "usage: $0 /path/to/absent-or-empty-dir" >&2
  exit 2
fi
REPO=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

# Refuse to overwrite: target must be absent or an empty directory before any writes.
if [ -e "$ROOT" ]; then
  if [ ! -d "$ROOT" ]; then
    echo "compose tutorial: target exists and is not a directory: $ROOT" >&2
    exit 1
  fi
  # ls -A lists non-dot entries; nonempty means we must not touch the tree.
  if [ -n "$(ls -A "$ROOT" 2>/dev/null)" ]; then
    echo "compose tutorial: target must be absent or empty (refusing to overwrite): $ROOT" >&2
    exit 1
  fi
fi
mkdir -p "$ROOT"
ROOT=$(CDPATH= cd -- "$ROOT" && pwd)
ART="$ROOT/artifacts"
PRODUCT="$ROOT/product"
OUT="$ROOT/output"
JOURNAL="$ROOT/journal"
mkdir -p "$ART/decisions" "$PRODUCT/protected" "$OUT"

printf 'old' > "$PRODUCT/candidate.txt"
printf 'frozen-source' > "$PRODUCT/readonly.txt"
printf 'acceptance-input' > "$PRODUCT/protected/accept.md"
printf '%s\n' '#!/bin/sh' 'test "$(cat candidate.txt)" = new' > "$PRODUCT/verify.sh"
chmod 0700 "$PRODUCT/verify.sh"
(
  cd "$PRODUCT"
  git init -q
  git add -A
  GIT_AUTHOR_NAME=fanisi GIT_AUTHOR_EMAIL=fanisi@example.com \
  GIT_COMMITTER_NAME=fanisi GIT_COMMITTER_EMAIL=fanisi@example.com \
    git commit -q -m "compose tutorial base"
)
HEAD=$(cd "$PRODUCT" && git rev-parse HEAD)
# Future deadline (BSD date on macOS; GNU date elsewhere).
DEADLINE=$(date -u -v+30M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+30 minutes' +%Y-%m-%dT%H:%M:%SZ)

cp "$REPO/testdata/compose/catalog/index.json" "$ART/catalog.json"
PIN=$(shasum -a 256 "$ART/catalog.json" | awk '{print $1}')
PRODUCT_ABS=$(CDPATH= cd -- "$PRODUCT" && pwd -P)
OUT_ABS=$(CDPATH= cd -- "$OUT" && pwd -P)
ART_ABS=$(CDPATH= cd -- "$ART" && pwd -P)

cat > "$ART/evidence.json" <<'EOF'
[
  {
    "id": "ev-b-thread",
    "kind": "B",
    "product_ids": ["product.voice", "product.notes"],
    "implementation_ids": ["impl.thread.v1"],
    "product_kinds": {"product.voice": "real", "product.notes": "real"},
    "source_refs": ["src/voice", "src/notes"],
    "notes_ref": "TEST_SCENARIO: synthetic fixture evidence for compose tutorial only; not live AM adoption"
  }
]
EOF

cat > "$ART/request.json" <<EOF
{
  "schema_version": 1,
  "request_id": "req-tutorial-1",
  "parent_id": "parent-tutorial-1",
  "product_revision": "$HEAD",
  "catalog_pin": "$PIN",
  "requirements": ["Edit candidate.txt to new under verification"],
  "behaviors": [{"behavior_id": "edit-candidate", "required": true, "contract": "contracts/edit.md"}],
  "workspace": "$PRODUCT_ABS",
  "read_paths": ["readonly.txt", "candidate.txt"],
  "write_paths": ["candidate.txt"],
  "protected_paths": ["protected/accept.md", "verify.sh"],
  "authority_ref": "operator:compose-tutorial",
  "deadline": "$DEADLINE",
  "budget": {
    "max_attempts": 2,
    "max_seconds": 60,
    "max_estimated_usd": 1.0,
    "reserved_usd": 0.1,
    "currency_note": "admission estimate; provider receipts separate"
  }
}
EOF

cd "$REPO"
go run . compose catalog --index "$ART/catalog.json" --family compose >/dev/null
REQ_HASH=$(go run . compose hash --file "$ART/request.json" --kind request | tr -d '\n')

cat > "$ART/decisions/dec-1.json" <<EOF
{
  "schema_version": 1,
  "decision_id": "dec-tutorial-1",
  "request_hash": "$REQ_HASH",
  "behavior_id": "edit-candidate",
  "verdict": "product_local",
  "owner": "operator",
  "provenance": {"source": "operator", "author": "tutorial"}
}
EOF
DEC_HASH=$(go run . compose hash --file "$ART/decisions/dec-1.json" --kind decision | tr -d '\n')

cat > "$ART/draft.json" <<EOF
{
  "schema_version": 1,
  "manifest_id": "man-tutorial-1",
  "bindings": [{
    "behavior_id": "edit-candidate",
    "decision_hash": "$DEC_HASH",
    "kind": "local_planned",
    "target_ref": "candidate.txt"
  }],
  "dependency_edges": []
}
EOF

printf '%s\n' '#!/bin/sh' 'printf new > candidate.txt' > "$ART/ok.sh"
chmod 0700 "$ART/ok.sh"

go run . compose manifest \
  --request "$ART/request.json" \
  --decisions "$ART/decisions" \
  --draft "$ART/draft.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json" \
  --out "$ART/manifest.json"
MAN_HASH=$(go run . compose hash --file "$ART/manifest.json" --kind manifest | tr -d '\n')

cat > "$ART/impl.json" <<EOF
{
  "schema_version": 1,
  "attempt_id": "att-tutorial-1",
  "parent_id": "parent-tutorial-1",
  "manifest_hash": "$MAN_HASH",
  "request_hash": "$REQ_HASH",
  "fence_token": "fence-tutorial-1",
  "owner": "operator",
  "read_paths": ["readonly.txt", "candidate.txt"],
  "write_paths": ["candidate.txt"],
  "protected_paths": ["protected/accept.md", "verify.sh"],
  "verify_command": ["$PRODUCT_ABS/verify.sh"],
  "delegate_argv": ["$ART_ABS/ok.sh"],
  "workspace": "$PRODUCT_ABS",
  "output_dir": "$OUT_ABS",
  "reserved_usd": 0.1,
  "max_seconds": 30,
  "deadline": "$DEADLINE",
  "idempotency_key": "parent-tutorial-1|$REQ_HASH|intent-edit"
}
EOF

go run . compose validate \
  --request "$ART/request.json" \
  --decisions "$ART/decisions" \
  --manifest "$ART/manifest.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json"
RESULT=$(go run . compose dispatch \
  --request "$ART/request.json" \
  --catalog "$ART/catalog.json" \
  --evidence "$ART/evidence.json" \
  --decisions "$ART/decisions" \
  --manifest "$ART/manifest.json" \
  --impl "$ART/impl.json" \
  --journal "$JOURNAL")
printf '%s\n' "$RESULT"
printf '%s\n' "$RESULT" | grep -q verified_pending_review
test "$(cat "$PRODUCT/candidate.txt")" = "new"
echo "tutorial ok: journal=$JOURNAL state=verified_pending_review"
