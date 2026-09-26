#!/usr/bin/env python3
"""Exercise one supplied Fanisi binary against synthetic offline study artifacts."""
import argparse
import hashlib
import json
import os
import pathlib
import subprocess
import tempfile


def write(path, value):
    path.write_text(json.dumps(value, sort_keys=True) + "\n")


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--expect-version", required=True)
    args = parser.parse_args()
    binary = str(args.binary.resolve())
    checks = 0

    def cli(*argv, reject=False):
        nonlocal checks
        result = subprocess.run([binary, *map(str, argv)], capture_output=True, text=True, timeout=60)
        if reject:
            if result.returncode == 0:
                raise AssertionError(f"command accepted invalid evidence: {argv[0]}")
            checks += 1
        elif result.returncode != 0:
            raise AssertionError(f"{argv[0]} failed: {result.stderr}")
        return result.stdout

    actual = cli("version").strip()
    if actual != "fanisi " + args.expect_version:
        raise AssertionError(f"unexpected version: {actual}")
    checks += 1
    with tempfile.TemporaryDirectory(prefix="fanisi-release-smoke-") as temporary:
        root = pathlib.Path(temporary)
        repo, study, inputs = root / "repo", root / "study", root / "inputs"
        for path in (repo, study, inputs):
            path.mkdir()

        def git(*argv):
            env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
            env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
            return subprocess.check_output(["git", "-c", "core.hooksPath=" + os.devnull,
                                            "-c", "commit.gpgsign=false", "-C", str(repo), *argv],
                                           stderr=subprocess.PIPE, env=env, timeout=30)

        git("init", "--initial-branch=main")
        git("config", "user.name", "Synthetic reviewer")
        git("config", "user.email", "fixture@example.invalid")
        code = repo / "code.txt"
        code.write_text("before\n")
        git("add", ".")
        git("commit", "-m", "synthetic base")
        base = git("rev-parse", "HEAD").decode().strip()
        code.write_text("after\n")
        patch = git("diff", "--binary", "HEAD")
        git("add", ".")
        git("commit", "-m", "synthetic accepted change")
        merge = git("rev-parse", "HEAD").decode().strip()
        config = {"Evaluation": {"repository": str(repo)}, "Task": {"write_paths": ["code.txt"]}}
        attempt = study / "repair"
        attempt.mkdir()
        write(attempt / "configuration.json", config)
        (attempt / "candidate.patch").write_bytes(patch)
        record = {"schema_version": 1, "task_id": "task", "arm": "fanisi",
                  "repair_from": "initial", "base": base, "started_at": "2026-09-07T00:00:00Z",
                  "patch_sha256": sha(patch), "configuration_sha256": sha((attempt / "configuration.json").read_bytes()),
                  "scope_ok": True, "verification_passed": True, "status": "verified_pending_review"}
        write(attempt / "attempt.json", record)
        write(attempt / "review-one.json", {"schema_version": 1, "decision": "accept", "kind": "agent",
              "reviewer": "synthetic-independent", "patch_sha256": sha(patch), "seconds": 12})
        receipt = {"id": "request", "provider_name": "synthetic", "total_cost": 0.25,
                   "native_tokens_prompt": 100, "native_tokens_completion": 10,
                   "native_tokens_cached": 80, "native_tokens_reasoning": 4}
        write(attempt / "provider-ledger.json", {"schema_version": 1, "complete": True, "generations": [receipt, receipt]})
        failed = study / "failed"
        failed.mkdir()
        write(failed / "attempt.json", {"schema_version": 1, "task_id": "task", "arm": "fanisi", "status": "failed", "started_at": "2026-09-07T00:00:00Z"})
        write(failed / "provider-ledger.json", {"schema_version": 1, "complete": False, "known_cost_usd": 0.5})
        attribution = {"schema_version": 1, "id": "coord", "study": "synthetic", "task": "task",
                       "attempt": "repair", "source_fingerprint": "stable-session", "allocation": "exclusive"}
        write(inputs / "attribution.json", attribution)
        coordinator = {"schema_version": 1, "snapshot_sha256": "snapshot-1",
                       "observed_before": "2026-09-07T00:00:00Z", "observed_end": "2026-09-07T00:01:00Z",
                       "tokens": {"input_tokens": 1000, "cached_input_tokens": 800, "output_tokens": 20,
                                  "reasoning_output_tokens": 5, "total_tokens": 1020}, "cost_usd": 0.1}
        write(inputs / "coordinator.json", coordinator)
        for _ in range(2):
            cli("import-coordinator", study, inputs / "attribution.json", inputs / "coordinator.json")
        attribution["id"] = "overlap"
        coordinator["snapshot_sha256"] = "snapshot-2"
        write(inputs / "attribution.json", attribution)
        write(inputs / "coordinator.json", coordinator)
        cli("import-coordinator", study, inputs / "attribution.json", inputs / "coordinator.json", reject=True)
        effort = {"schema_version": 1, "id": "review", "study": "synthetic", "task": "task", "attempt": "repair",
                  "role": "reviewer", "source_fingerprint": "review-session", "allocation": "exclusive",
                  "coverage": "complete", "from": "2026-09-07T00:00:00Z", "to": "2026-09-07T00:01:00Z",
                  "active_seconds": 12, "cost_usd": None}
        write(inputs / "effort.json", effort)
        for _ in range(2):
            cli("import-effort", study, inputs / "effort.json")
        effort.update(id="tooling", role="tooling", source_fingerprint="tooling-source", allocation="study", task="", attempt="", cost_usd=7)
        write(inputs / "effort.json", effort)
        cli("import-effort", study, inputs / "effort.json")
        landing = {"schema_version": 1, "id": "accepted", "repository": str(repo), "target_base": base,
                   "target_ref": "refs/heads/main", "merge_commit": merge, "review_record": "review-one.json",
                   "merge_evidence": "synthetic local merge fixture", "independent_review": True,
                   "patch_sha256": sha(patch), "at": "2026-09-07T00:02:00Z"}
        write(inputs / "landing.json", landing)
        for _ in range(2):
            cli("import-landing", attempt, inputs / "landing.json")
        report = json.loads(cli("report", study))
        delivery, actors = report["delivery"], report["effort"]
        expected = {"attempts": 2, "accepted_landed_tasks": 1, "assisted_accepted_tasks": 1,
                    "autonomous_accepted_tasks": 0, "worker_input_plus_output_tokens": 110,
                    "total_cost_usd": None, "cost_per_autonomous_accepted_usd": None}
        for key, value in expected.items():
            if delivery[key] != value:
                raise AssertionError(f"{key}: {delivery[key]} != {value}")
            checks += 1
        assert abs(delivery["known_cost_lower_bound_usd"] - 0.85) < 1e-12
        assert delivery["request_to_landing_seconds"]["task"] == 120
        assert actors["coordinator"]["records"] == 1 and actors["coordinator"]["total_cost_usd"] is None
        assert actors["reviewer"]["known_active_seconds"] == 12 and actors["reviewer"]["total_cost_usd"] is None
        assert actors["study_tooling"]["known_cost_usd"] == 7
        checks += 5
        failed_record = json.loads((failed / "attempt.json").read_text())
        del failed_record["started_at"]
        write(failed / "attempt.json", failed_record)
        uncertain = json.loads(cli("report", study))["delivery"]["request_to_landing_seconds"]
        assert "task" not in uncertain, "missing failed-attempt start became known elapsed"
        checks += 1
        # A wrong implementation on its target branch must fail content identity.
        code.write_text("wrong\n")
        git("add", ".")
        git("commit", "-m", "synthetic wrong landing")
        landing.update(id="wrong", merge_commit=git("rev-parse", "HEAD").decode().strip())
        write(inputs / "landing.json", landing)
        cli("import-landing", attempt, inputs / "landing.json", reject=True)
        # Same reviewed content, different commit, but absent from target branch.
        git("checkout", "-b", "unmerged", base)
        code.write_text("after\n")
        git("add", ".")
        git("commit", "-m", "synthetic unmerged equivalent")
        landing.update(id="unmerged", merge_commit=git("rev-parse", "HEAD").decode().strip())
        write(inputs / "landing.json", landing)
        cli("import-landing", attempt, inputs / "landing.json", reject=True)
        write(inputs / "landing.json", {"schema_version": 1, "id": "revoked", "revokes": "accepted", "merge_evidence": "synthetic regression"})
        cli("import-landing", attempt, inputs / "landing.json")
        assert json.loads(cli("report", study))["delivery"]["accepted_landed_tasks"] == 0
        checks += 1
    print(json.dumps({"version": actual, "checks_passed": checks, "offline": True,
                      "known_variable_cost_usd": 0.85, "separate_tooling_usd": 7}))


if __name__ == "__main__":
    main()
