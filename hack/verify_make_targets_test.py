"""Exercise the production Make orchestration with inexpensive leaf checks."""

import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
MAKEFILE = (ROOT / "Makefile").read_text()
REPOSITORY_CHECKS = {
    "verify-codespell", "verify-codecov", "verify-api-deps", "verify-crd-schema",
    "cpo-container-sync", "run-gitlint", "verify-docs-nav",
    "verify-tekton-pipeline-pairs", "verify-make-targets",
}
ANALYSIS_CHECKS = {"staticcheck", "verify-fmt", "vet", "api-lint", "main-lint"}
ORCHESTRATORS = {
    "verify", "_verify", "lint", "_verify-repository", "verify-ci", "verify-quick", "verify-generated",
    "verify-serial-targets", "verify-parallel-targets",
}


def rules(targets):
    # Use the real recipes and dependencies; replace only the expensive leaves.
    return "\n".join(
        re.search(rf"(?m)^{re.escape(target)}:[^\n]*\n(?:\t[^\n]*\n|\n)*", MAKEFILE)[0]
        for target in sorted(targets)
    )


class VerificationTargetsTest(unittest.TestCase):
    def run_target(self, target, failure=""):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory).resolve()
            leaves = REPOSITORY_CHECKS | ANALYSIS_CHECKS | {
                "generate", "update", "verify-capi-sync", "verify-capi-decoupling",
                "verify-git-clean",
            }
            fixture = "VERIFY_CONTAINER = 0\n" + rules(ORCHESTRATORS) + "\n.PHONY: " + " ".join(leaves) + "\n"
            for leaf in sorted(leaves):
                fixture += f"{leaf}:\n\t@echo {leaf} >> events; test '{leaf}' != '{failure}' && test ! -e writing"
                if leaf in {"generate", "update", "verify-capi-sync"}:
                    fixture += " && touch writing && sleep 0.02 && rm writing"
                fixture += "\n"
            (path / "Makefile").write_text(fixture)
            (path / "hack").mkdir()
            shutil.copy2(ROOT / "hack/verify-in-container.sh", path / "hack/verify-in-container.sh")
            env = {k: v for k, v in os.environ.items() if k not in {"MAKEFLAGS", "MAKELEVEL", "MFLAGS", "MAKEFILES"}}
            env["VERIFY_CACHE_DIR"] = str(path / "cache")
            result = subprocess.run(["make", "--no-print-directory", "-j8", target],
                                    cwd=path, env=env, capture_output=True, text=True)
            events = (path / "events").read_text().splitlines()
            return result, events

    def test_verify_owns_repository_checks_and_orders_writers_before_readers(self):
        result, events = self.run_target("_verify-repository")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(set(events), REPOSITORY_CHECKS | {
            "generate", "update", "verify-capi-sync", "verify-capi-decoupling", "verify-git-clean",
        })
        self.assertLess(events.index("generate"), events.index("update"))
        self.assertLess(events.index("update"), events.index("verify-capi-sync"))
        self.assertLess(events.index("verify-capi-sync"), events.index("verify-capi-decoupling"))
        self.assertTrue(all(events.index("verify-capi-decoupling") < events.index(c) for c in REPOSITORY_CHECKS))
        self.assertEqual(events[-1], "verify-git-clean")

    def test_lint_only_prepares_mocks_before_analysis(self):
        result, events = self.run_target("lint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(set(events), ANALYSIS_CHECKS | {"generate"})
        self.assertEqual(events[0], "generate")

    def test_verify_and_ci_preserve_complete_coverage(self):
        for target in ("verify", "verify-ci"):
            with self.subTest(target=target):
                result, events = self.run_target(target)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(set(events), REPOSITORY_CHECKS | ANALYSIS_CHECKS | {
                    "generate", "update", "verify-capi-sync", "verify-capi-decoupling", "verify-git-clean",
                })
                self.assertLess(max(events.index(c) for c in REPOSITORY_CHECKS),
                                min(events.index(c) for c in ANALYSIS_CHECKS))
                self.assertEqual(events[-1], "verify-git-clean")

    def test_quick_remains_a_generation_check(self):
        result, events = self.run_target("verify-quick")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(events, ["generate", "update", "verify-git-clean"])

    def test_preparation_failure_prevents_analysis(self):
        result, events = self.run_target("verify", failure="update")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(ANALYSIS_CHECKS.isdisjoint(events))
        self.assertNotIn("verify-capi-sync", events)

    def test_repository_failures_prevent_analysis(self):
        for failure in REPOSITORY_CHECKS | {"verify-git-clean", "verify-capi-decoupling"}:
            with self.subTest(failure=failure):
                result, events = self.run_target("verify", failure=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(ANALYSIS_CHECKS.isdisjoint(events))

    def test_analysis_failures_propagate_and_other_analyzers_still_run(self):
        for failure in ANALYSIS_CHECKS:
            with self.subTest(failure=failure):
                result, events = self.run_target("lint", failure=failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(ANALYSIS_CHECKS.issubset(events))


class FormattingCheckTest(unittest.TestCase):
    def test_reports_all_root_module_files_without_modifying_them(self):
        with tempfile.TemporaryDirectory(prefix="format check ") as directory:
            path = Path(directory).resolve()
            (path / "Makefile").write_text("GO = go\n" + rules({"verify-fmt"}))
            (path / "go.mod").write_text("module example.com/formatcheck\n\ngo 1.26.0\n")
            sources = {
                "main.go": "package example\nvar X=1\n",
                "main_test.go": "package example\nvar Y=2\n",
                "external_test.go": "package example_test\nvar Z=3\n",
                "excluded.go": "//go:build excluded\n\npackage example\nvar W=4\n",
                "nested/go.mod": "module example.com/nested\n\ngo 1.26.0\n",
                "nested/ignored.go": "package nested\nvar N=5\n",
                "vendor/example.com/ignored/ignored.go": "package ignored\nvar V=6\n",
            }
            for name, content in sources.items():
                (path / name).parent.mkdir(parents=True, exist_ok=True)
                (path / name).write_text(content)
            env = dict(os.environ, GOWORK="off", GOFLAGS="-mod=mod")
            result = subprocess.run(["make", "verify-fmt"], cwd=path, env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            for name in ("main.go", "main_test.go", "external_test.go", "excluded.go"):
                self.assertIn(str(path / name), result.stdout, result.stderr)
            self.assertNotIn("ignored.go", result.stdout)
            self.assertEqual({name: (path / name).read_text() for name in sources}, sources)
            subprocess.run(["gofmt", "-w", "main.go", "main_test.go", "external_test.go", "excluded.go"],
                           cwd=path, check=True)
            result = subprocess.run(["make", "verify-fmt"], cwd=path, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            (path / "go.mod").unlink()
            result = subprocess.run(["make", "verify-fmt"], cwd=path, env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, "package-loading failures must fail the check")


if __name__ == "__main__":
    unittest.main()
