#!/usr/bin/env python3
"""Create a reproducible sample repository that reproduces the PR #9568 layout mistake.

The feature branch mixes module definitions with generated vendor trees, the same way
openshift/hypershift#9568 did, and adds a rename with spaces, a deletion, a component
fixture, envtest suites, CI/Makefile edits, and agent docs. The repository also contains
a copy of this skill so agents can follow SKILL.md with repository-relative paths.

Usage: python3 sample_branch.py <empty-or-missing-directory>
Result: <dir>/origin.git (bare remote) and <dir>/work (clone on branch k8s-1.37-bump).
"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
from pathlib import Path

SKILL_DIR = Path(__file__).resolve().parent
REPO_ROOT = SKILL_DIR.parents[1]
FEATURE_BRANCH = "k8s-1.37-bump"

BASE_FILES = {
    "go.mod": "module github.com/openshift/hypershift\n\nrequire k8s.io/api v0.36.0\n",
    "go.sum": "k8s.io/api v0.36.0 h1:base\n",
    "vendor/modules.txt": "# k8s.io/api v0.36.0\n",
    "vendor/k8s.io/api/core/v1/types.go": "package v1 // v0.36.0\n",
    "api/go.mod": "module github.com/openshift/hypershift/api\n\nrequire k8s.io/api v0.36.0\n",
    "api/go.sum": "k8s.io/api v0.36.0 h1:base\n",
    "api/vendor/modules.txt": "# k8s.io/api v0.36.0\n",
    "api/vendor/k8s.io/api/core/v1/types.go": "package v1 // v0.36.0\n",
    "api/hypershift/v1beta1/hostedcluster_types.go": "package v1beta1\n",
    "api/hypershift/v1beta1/zz_generated.deepcopy.go": "package v1beta1 // generated\n",
    "cmd/install/assets/crds/hypershift-operator/zz_generated.crd-manifests/hostedclusters.crd.yaml": "kind: CRD # 1.36\n",
    "control-plane-operator/controllers/hostedcontrolplane/v2/kube_scheduler/testdata/zz_fixture_TestKubeScheduler.yaml":
        "version: 1.36\n",
    "hypershift-operator/controllers/legacy/legacy.go": "package legacy\n",
    "docs/content/how-to/k8s old guide.md": "# Kubernetes guide\n",
    "test/envtest/README.md": "# envtest\n",
    ".github/workflows/envtest-kube-reusable.yaml": "matrix: [1.36]\n",
    "Makefile": "KUBE_VERSIONS := 1.36\n",
    "AGENTS.md": "# Agents\n",
}

FEATURE_COMMITS = [
    (
        "build(api): bump Kubernetes dependencies to 1.37",
        {
            "api/go.mod": "module github.com/openshift/hypershift/api\n\nrequire k8s.io/api v0.37.0\n",
            "api/go.sum": "k8s.io/api v0.37.0 h1:new\n",
            "api/vendor/modules.txt": "# k8s.io/api v0.37.0\n",
            "api/vendor/k8s.io/api/core/v1/types.go": "package v1 // v0.37.0\n",
        },
        [],
    ),
    (
        "chore(api): regenerate Kubernetes 1.37 dependencies and CRDs",
        {
            "go.mod": "module github.com/openshift/hypershift\n\nrequire k8s.io/api v0.37.0\n",
            "go.sum": "k8s.io/api v0.37.0 h1:new\n",
            "vendor/modules.txt": "# k8s.io/api v0.37.0\n",
            "vendor/k8s.io/api/core/v1/types.go": "package v1 // v0.37.0\n",
            "api/hypershift/v1beta1/zz_generated.deepcopy.go": "package v1beta1 // generated 1.37\n",
            "cmd/install/assets/crds/hypershift-operator/zz_generated.crd-manifests/hostedclusters.crd.yaml":
                "kind: CRD # 1.37\n",
        },
        [],
    ),
    (
        "test(control-plane-operator): refresh Kubernetes 1.37 scheduler fixtures",
        {
            "control-plane-operator/controllers/hostedcontrolplane/v2/kube_scheduler/testdata/zz_fixture_TestKubeScheduler.yaml":
                "version: 1.37\n",
            "docs/content/how-to/k8s new guide.md": "# Kubernetes guide\n",
        },
        ["hypershift-operator/controllers/legacy/legacy.go", "docs/content/how-to/k8s old guide.md"],
    ),
    (
        "test(e2e): add Kubernetes 1.37 envtest coverage",
        {
            "cmd/install/assets/crds/hypershift-operator/tests/hostedclusters.hypershift.openshift.io/k8s-1.37.testsuite.yaml":
                "name: k8s 1.37\n",
            "test/envtest/README.md": "# envtest\n\nCovers 1.30-1.37.\n",
            ".github/workflows/envtest-kube-reusable.yaml": "matrix: [1.36, 1.37]\n",
            "Makefile": "KUBE_VERSIONS := 1.36 1.37\n",
            "AGENTS.md": "# Agents\n\nEnvtest covers Kubernetes 1.37.\n",
        },
        [],
    ),
]

SUPPORT_FILES = [
    "skills/restructure-commits/SKILL.md",
    "skills/restructure-commits/ownership.json",
    "skills/restructure-commits/commit_layout.py",
    "skills/git-commit-format/SKILL.md",
    ".gitlint",
]


def clean_env() -> dict[str, str]:
    """Return the environment without GIT_* overrides, such as the GIT_DIR and GIT_INDEX_FILE set by Git hooks."""
    return {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def run(cwd: Path, *args: str) -> str:
    return subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.PIPE, text=True, env=clean_env()).stdout


def write(root: Path, files: dict[str, str]) -> None:
    for rel, content in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)


def init_repo(repo: Path) -> None:
    """Create a repository on main with a fixed identity, no signing, and no hooks."""
    repo.mkdir(parents=True, exist_ok=True)
    run(repo, "git", "init", "-q", "-b", "main")
    for key, value in (
        ("user.name", "Sample Author"),
        ("user.email", "sample@example.com"),
        ("commit.gpgsign", "false"),
        ("core.hooksPath", os.devnull),
    ):
        run(repo, "git", "config", key, value)


def commit(repo: Path, message: str, files: dict[str, str] | None = None, delete: tuple[str, ...] = ()) -> str:
    """Write files, delete paths, commit everything, and return the new commit SHA."""
    write(repo, files or {})
    for rel in delete:
        (repo / rel).unlink()
    run(repo, "git", "add", "-A")
    run(repo, "git", "commit", "-q", "-s", "-m", message)
    return run(repo, "git", "rev-parse", "HEAD").strip()


def create_sample(target: Path) -> Path:
    target = Path(target).resolve()
    origin, work = target / "origin.git", target / "work"
    origin.mkdir(parents=True, exist_ok=True)
    run(origin, "git", "init", "-q", "--bare", "-b", "main")
    init_repo(work)
    run(work, "git", "remote", "add", "origin", str(origin))

    write(work, BASE_FILES)
    for rel in SUPPORT_FILES:
        dst = work / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(REPO_ROOT / rel, dst)
    commit(work, "chore: sample base")
    run(work, "git", "push", "-q", "origin", "main")

    run(work, "git", "checkout", "-q", "-b", FEATURE_BRANCH)
    for message, files, deletions in FEATURE_COMMITS:
        commit(work, message, files, tuple(deletions))
    run(work, "git", "push", "-q", "-u", "origin", FEATURE_BRANCH)
    return work


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    dest = Path(sys.argv[1])
    if dest.exists() and any(dest.iterdir()):
        print(f"error: {dest} is not empty", file=sys.stderr)
        sys.exit(2)
    print(create_sample(dest))
