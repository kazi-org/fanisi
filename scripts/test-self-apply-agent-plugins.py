#!/usr/bin/env python3
"""Opt-in real-binary regression for the plugin self-application recipe.

Run after committing the source changes, with fanisi and amsl-agent-plugin on
PATH. No builds, network calls or model calls are made. All generated writes
and Git commits stay inside a temporary repository created by this test.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class SelfApplyPathsTest(unittest.TestCase):
    def test_quoted_paths_through_real_dispatch(self):
        source = Path(__file__).resolve().parents[1]
        for name in ("git", "tar", "fanisi", "amsl-agent-plugin"):
            self.assertIsNotNone(shutil.which(name), f"install {name} on PATH")
        archive = subprocess.check_output(["git", "archive", "HEAD"], cwd=source)
        with tempfile.TemporaryDirectory(prefix="fanisi-self-apply-test-") as root:
            repo = Path(root) / 'checkout"with\\chars'
            repo.mkdir()
            subprocess.run(["tar", "-x", "-C", str(repo)], input=archive, check=True)
            generated = repo / "plugins/generated"
            expected = {p.relative_to(generated): p.read_bytes()
                        for p in generated.rglob("*") if p.is_file()}
            # These files were just extracted by this test into its own temp repo.
            shutil.rmtree(generated)
            env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
            for args in (["init", "-q"], ["add", "."],
                         ["-c", "user.name=Plugin test", "-c", "user.email=test@example.invalid",
                          "commit", "-qm", "Prepare isolated self-application fixture"]):
                subprocess.run(["git", *args], cwd=repo, env=env, check=True,
                               capture_output=True, text=True)
            run = Path(root) / 'run"with\\chars'
            result = subprocess.run(
                ["sh", "scripts/self-apply-agent-plugins.sh", str(run)],
                cwd=repo, env=env, capture_output=True, text=True, timeout=150)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            record = json.loads((run / "dispatch-result.json").read_text())
            self.assertEqual(record["state"], "verified_pending_review")
            actual = {p.relative_to(generated): p.read_bytes()
                      for p in generated.rglob("*") if p.is_file()}
            self.assertEqual(actual, expected)
            subprocess.run(["amsl-agent-plugin", "check", "--spec", "plugins/fanisi.spec.json",
                            "--out", "plugins/generated"], cwd=repo, env=env,
                           check=True, capture_output=True, text=True)


if __name__ == "__main__":
    unittest.main()
