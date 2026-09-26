# Compose tutorial fixtures (example/test scenario)

Creates a fresh throwaway Git product workspace and valid offline JSON for
`fanisi compose …`. Not a claim about live AMSL adoption, provider qualification,
or peer-product adapters. Synthetic evidence is labelled `TEST_SCENARIO`.

The target directory must be **absent or empty**. The script refuses to run if
the path already contains files (existing contents are left untouched).

```sh
# From the Fanisi repo root:
sh examples/compose/run-tutorial.sh /tmp/fanisi-compose-demo
```

Exact commands emitted by the script (after fixture generation):

```text
go run . compose catalog --index <dir>/artifacts/catalog.json --family compose
go run . compose hash --file <dir>/artifacts/request.json --kind request
go run . compose manifest --request … --decisions … --draft … --catalog … --evidence … --out …
go run . compose validate --request … --decisions … --manifest … --catalog … --evidence …
go run . compose dispatch --request … --catalog … --evidence … --decisions … --manifest … --impl … --journal …
```

The script checks for `verified_pending_review` and that `candidate.txt` became
`new`. Human review, agent-accept rejection, cancel uncertainty, and duplicate
no-reexec are covered by `compose_cli_test.go` rather than this shell demo.

Use a relative catalog path from your chosen directory (the script copies
`testdata/compose/catalog/index.json`). Do not paste expired absolute HEADs or
hashes into committed docs — regenerate in a clean temp directory each time.
