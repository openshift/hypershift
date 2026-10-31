"""Test the launcher against a recording runtime without pulling images."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
FAKE_RUNTIME = """#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
with open(os.environ['ENGINE_LOG'], 'a') as log:
    log.write(json.dumps(args) + '\\n')
if args[0] == 'info':
    sys.exit(int(os.environ.get('INFO_STATUS', '0')))
if args[:2] == ['image', 'inspect']:
    if os.environ.get('NEEDS_PULL') and not Path(os.environ['PULLED']).exists():
        sys.exit(1)
    print('sha256:' + os.environ.get('IMAGE_ID', 'image-arm64'))
if args[0] == 'pull':
    Path(os.environ['PULLED']).write_text(args[-1])
    sys.exit(int(os.environ.get('PULL_STATUS', '0')))
if args[0] == 'run':
    if os.environ.get('MOUNT_CHECK_REPO'):
        repo = Path(os.environ['MOUNT_CHECK_REPO'])
        for mount in (repo / 'hack/tools/bin', repo / 'bin'):
            if not mount.is_dir() or mount.stat().st_uid != os.getuid():
                sys.exit(31)
    if args[args.index('--entrypoint') + 1] == '/bin/sh':
        sys.exit(int(os.environ.get('INIT_STATUS', '0')))
    sys.exit(int(os.environ.get('RUN_STATUS', '0')))
"""


class VerificationContainerTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="verify launcher ")
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name).resolve()
        self.repo = self.path / "checkout"
        (self.repo / "hack").mkdir(parents=True)
        for name in ("hack/verify-container.sh", "hack/verify-in-container.sh", "hack/verify-image.sh", "hack/verify-image",
                     "hack/utils.sh", "Makefile"):
            shutil.copy2(ROOT / name, self.repo / name)
        self.git("init", "-b", "main")
        self.git("add", ".")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.com",
                 "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "-m", "fixture")
        self.runtime = self.path / "podman"
        self.runtime.write_text(FAKE_RUNTIME)
        self.runtime.chmod(0o755)
        self.log = self.path / "engine.jsonl"
        self.env = {k: v for k, v in os.environ.items() if k not in {
            "MAKEFLAGS", "MAKELEVEL", "MAKEFILES", "MFLAGS", "VERIFY_CONTAINER",
            "PULL_BASE_SHA", "PULL_PULL_SHA", "OPENSHIFT_CI", "GITHUB_ACTIONS", "VERIFY_CACHE_DIR",
        }}
        self.env.update(RUNTIME=str(self.runtime), VERIFY_CACHE_KEY="fixture",
                        VERIFY_CACHE_DIR=str(self.path / "cache"),
                        ENGINE_LOG=str(self.log), PULLED=str(self.path / "pulled"))

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.repo, text=True).strip()

    def launch(self, *args, **env):
        # Exercise macOS's system Bash too, rather than relying on a newer PATH entry.
        return subprocess.run(["/bin/bash", str(self.repo / "hack/verify-container.sh"), *args],
                              env=dict(self.env, **env), capture_output=True, text=True)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_default_command_uid_caches_and_refs(self):
        result = self.launch(PULL_BASE_SHA="base", PULL_PULL_SHA="head")
        self.assertEqual(result.returncode, 0, result.stderr)
        run = self.calls()[-1]
        self.assertEqual(run[-2:], ["make", "verify"])
        self.assertEqual(run[run.index("--user") + 1], f"{os.getuid()}:{os.getgid()}")
        self.assertIn("--userns=keep-id", run)
        self.assertIn(f"{self.repo}:{self.repo}:z", run)
        self.assertIn("PULL_BASE_SHA", run)
        self.assertIn("PULL_PULL_SHA", run)
        self.assertIn("TMPDIR=/verify-cache/tmp", run)
        self.assertIn("VERIFY_CACHE_DIR=/verify-cache", run)
        self.assertIn("VERIFY_CONTAINER=0", run)
        self.assertIn(str(self.repo / "hack/verify-in-container.sh"), run)
        volumes = [run[i + 1] for i, arg in enumerate(run) if arg == "--volume"]
        for suffix in ("/verify-cache:z", f"{self.repo}/hack/tools/bin:z", f"{self.repo}/bin:z"):
            self.assertTrue(any(v.endswith(":" + suffix) for v in volumes), volumes)
        self.assertTrue(all(v.startswith("hypershift-verify-") for v in volumes[1:]))
        checkout_id = hashlib.sha256(str(self.repo).encode()).hexdigest()
        cache_id = hashlib.sha256(
            f"image-arm64\n{checkout_id}\n{os.getuid()}:{os.getgid()}\nfixture\n".encode()
        ).hexdigest()
        self.assertIn(f"hypershift-verify-{cache_id}-cache:/verify-cache:z", volumes)
        initialize = [c for c in self.calls() if "--entrypoint" in c][0]
        self.assertEqual(initialize[initialize.index("--user") + 1], "0:0")
        self.assertNotIn(f"{self.repo}:{self.repo}:z", initialize)

    def test_linked_worktree_mounts_shared_metadata(self):
        worktree = self.path / "linked worktree"
        self.git("worktree", "add", "-b", "linked", str(worktree))
        common = self.repo / ".git"
        self.repo = worktree
        result = self.launch()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"{common}:{common}:z", self.calls()[-1])
        self.assertEqual(self.git("rev-parse", "--git-common-dir"), str(common))

    def test_pull_uses_the_shared_pin_and_stops_on_failure(self):
        result = self.launch(NEEDS_PULL="1", PULL_STATUS="9")
        self.assertEqual(result.returncode, 9, result.stderr)
        pin = (ROOT / "hack/verify-image").read_text().strip()
        self.assertEqual((self.path / "pulled").read_text(), pin)
        self.assertEqual(self.calls()[-1], ["pull", pin])
        self.assertFalse(any(c[0] == "run" for c in self.calls()))

    def test_floating_image_is_rejected(self):
        (self.repo / "hack/verify-image").write_text("quay.io/example/verify:latest\n")
        result = self.launch()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must pin", result.stderr)
        self.assertEqual(self.calls(), [["info"]])

    def test_image_resolver_returns_the_shared_pin(self):
        pin = (ROOT / "hack/verify-image").read_text().strip()
        self.assertRegex(pin, r"@sha256:[0-9a-f]{64}$")
        result = subprocess.run(["/bin/bash", str(self.repo / "hack/verify-image.sh")],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, pin + "\n")

    def test_image_resolver_rejects_invalid_and_multiline_references(self):
        digest = "a" * 64
        for pin in ("", "quay.io/example/verify:latest", f"quay.io/example/verify@sha256:{digest}\nextra",
                    f"quay.io/example/verify@sha256:{digest};echo unexpected",
                    f"quay.io/example/verify@sha256:{digest[:-1]}"):
            with self.subTest(pin=pin):
                (self.repo / "hack/verify-image").write_text(pin + "\n")
                result = subprocess.run(["/bin/bash", str(self.repo / "hack/verify-image.sh")],
                                        capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertIn("must pin", result.stderr)

    def test_container_environment_initializes_a_writable_cache(self):
        program = "import json, os; print(json.dumps(dict(os.environ)))"
        environment = dict(self.env, GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="test.existing",
                           GIT_CONFIG_VALUE_0="preserved")
        result = subprocess.run(["/bin/bash", str(self.repo / "hack/verify-in-container.sh"),
                                 "python3", "-c", program], cwd=self.repo, env=environment,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        env = json.loads(result.stdout)
        for variable in ("HOME", "GOPATH", "XDG_CACHE_HOME", "TMPDIR"):
            self.assertTrue(Path(env[variable]).is_dir())
            self.assertTrue(env[variable].startswith(str(self.path / "cache")))
        self.assertEqual(env["VERIFY_CONTAINER"], "0")
        self.assertEqual(env["GIT_CONFIG_COUNT"], "2")
        self.assertEqual(env["GIT_CONFIG_KEY_0"], "test.existing")
        self.assertEqual(env["GIT_CONFIG_VALUE_0"], "preserved")
        self.assertEqual(env["GIT_CONFIG_KEY_1"], "safe.directory")
        self.assertEqual(env["GIT_CONFIG_VALUE_1"], str(self.repo))

    def test_default_cache_is_private_and_reused(self):
        env = dict(self.env, TMPDIR=str(self.path))
        del env["VERIFY_CACHE_DIR"]
        cache = self.path / f"hypershift-verify-{os.getuid()}"
        command = ["/bin/bash", str(self.repo / "hack/verify-in-container.sh"), "true"]
        result = subprocess.run(command, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(cache.stat().st_mode & 0o777, 0o700)
        marker = cache / "retained"
        marker.touch()
        result = subprocess.run(command, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(marker.exists())

    def test_unsafe_cache_is_rejected_before_executing_commands(self):
        target = self.path / "symlink-target"
        target.mkdir(mode=0o700)
        for kind in ("symlink", "shared"):
            for default in (False, True):
                with self.subTest(kind=kind, default=default):
                    parent = self.path / f"{kind}-{default}"
                    parent.mkdir()
                    cache = parent / f"hypershift-verify-{os.getuid()}"
                    if kind == "symlink":
                        cache.symlink_to(target, target_is_directory=True)
                    else:
                        cache.mkdir()
                        cache.chmod(0o777)
                    marker = parent / "executed"
                    env = dict(self.env, TMPDIR=str(parent), VERIFY_CACHE_DIR=str(cache))
                    if default:
                        del env["VERIFY_CACHE_DIR"]
                    result = subprocess.run(
                        ["/bin/bash", str(self.repo / "hack/verify-in-container.sh"), "touch", str(marker)],
                        env=env, capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("VERIFY_CACHE_DIR", result.stderr)
                    self.assertFalse(marker.exists())

    def test_docker_mountpoints_exist_and_belong_to_caller_before_container_start(self):
        docker = self.path / "docker"
        shutil.copy2(self.runtime, docker)
        self.assertFalse((self.repo / "bin").exists())
        self.assertFalse((self.repo / "hack/tools/bin").exists())
        result = self.launch(RUNTIME=str(docker), MOUNT_CHECK_REPO=str(self.repo))
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_docker_argument_boundaries_and_exit_status(self):
        docker = self.path / "docker"
        shutil.copy2(self.runtime, docker)
        result = self.launch("sh", "-c", "exit 23", RUNTIME=str(docker), RUN_STATUS="23")
        self.assertEqual(result.returncode, 23, result.stderr)
        run = self.calls()[-1]
        self.assertNotIn("--userns=keep-id", run)
        self.assertEqual(run[-3:], ["sh", "-c", "exit 23"])

    def test_stopped_runtime_reports_actionable_error(self):
        result = self.launch(INFO_STATUS="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("start its container service", result.stderr)
        self.assertEqual(len(self.calls()), 1)

    def test_volume_initialization_failure_prevents_verification(self):
        result = self.launch(INIT_STATUS="19")
        self.assertEqual(result.returncode, 19, result.stderr)
        self.assertIn("--entrypoint", self.calls()[-1])

    def test_image_changes_isolate_tools_and_compiler_caches(self):
        self.assertEqual(self.launch().returncode, 0)
        first = self.calls()[-1]
        self.assertEqual(self.launch(IMAGE_ID="image-amd64").returncode, 0)
        second = self.calls()[-1]
        first_cache = [first[i + 1] for i, arg in enumerate(first) if arg == "--volume"][1:]
        second_cache = [second[i + 1] for i, arg in enumerate(second) if arg == "--volume"][1:]
        self.assertTrue(all(a != b for a, b in zip(first_cache, second_cache)))
        self.assertEqual(self.launch(VERIFY_CACHE_KEY="fresh").returncode, 0)
        self.assertNotEqual(first, self.calls()[-1])

    def test_make_verify_dispatches_without_host_go(self):
        go = self.path / "go"
        go.write_text("#!/bin/sh\necho 'host Go must not run' >&2\nexit 99\n")
        go.chmod(0o755)
        env = dict(self.env, PATH=str(self.path) + os.pathsep + os.environ["PATH"])
        result = subprocess.run(["make", "verify"], cwd=self.repo, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("host Go must not run", result.stderr)
        self.assertEqual(self.calls()[-1][-2:], ["make", "verify"])

    def test_existing_ci_containers_run_the_same_complete_inner_target(self):
        # Override only the expensive inner workload; keep the real entry point.
        makefile = self.repo / "Makefile"
        makefile.write_text(re.sub(r"(?m)^_verify:\n(?:\t[^\n]*\n)*",
                                  "_verify:\n\t@echo complete-workload\n", makefile.read_text()))
        for flag in ({"VERIFY_CONTAINER": "0"}, {"OPENSHIFT_CI": "true"}, {"GITHUB_ACTIONS": "true"}):
            with self.subTest(flag=flag):
                result = subprocess.run(["make", "verify"],
                                        cwd=self.repo, env=dict(self.env, **flag), capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn("complete-workload", result.stdout)
        self.assertFalse(self.log.exists(), "existing CI containers must not start nested containers")


if __name__ == "__main__":
    unittest.main()
