"""Regression tests for commit_layout.py using temporary Git repositories."""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))

import commit_layout  # noqa: E402
import sample_branch  # noqa: E402
from sample_branch import commit, write  # noqa: E402

SKILL_DIR = Path(__file__).resolve().parent


def git(repo: Path, *args: str) -> str:
    return sample_branch.run(repo, "git", *args).strip()


def layout(repo: Path, *args: str) -> tuple[int, str, str]:
    proc = subprocess.run(
        [sys.executable, str(SKILL_DIR / "commit_layout.py"), *args],
        cwd=repo,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return proc.returncode, proc.stdout, proc.stderr


def plan_paths(repo: Path, base: str, head: str, monkeypatch: pytest.MonkeyPatch) -> dict[str, list[tuple[str, str]]]:
    monkeypatch.chdir(repo)
    by_group, unclassified = commit_layout.build_plan(commit_layout.Ownership.load(), commit_layout.diff_changes(base, head))
    assert not unclassified
    return {name: [(c.status, c.path) for c in changes] for name, changes in by_group.items() if changes}


@pytest.fixture(autouse=True)
def isolated_git_env(monkeypatch: pytest.MonkeyPatch) -> None:
    """Keep git in tests on the temporary repositories even when run from a Git hook."""
    for key in [k for k in os.environ if k.startswith("GIT_")]:
        monkeypatch.delenv(key)


def test_git_ignores_inherited_repository_overrides(repo: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GIT_DIR", str(tmp_path / "caller.git"))
    monkeypatch.setenv("GIT_INDEX_FILE", str(tmp_path / "caller.index"))
    assert git(repo, "rev-parse", "--absolute-git-dir") == str((repo / ".git").resolve())


@pytest.fixture
def repo(tmp_path: Path) -> Path:
    sample_branch.init_repo(tmp_path)
    commit(tmp_path, "chore: base", {"README.md": "base\n", "go.mod": "module x\n", "api/go.mod": "module x/api\n"})
    return tmp_path


@pytest.mark.parametrize(
    "pattern,path,expected",
    [
        ("api/**", "api/go.mod", True),
        ("api/**/zz_generated*", "api/zz_generated.deepcopy.go", True),
        ("api/**/zz_generated*", "api/hypershift/v1beta1/zz_generated.deepcopy.go", True),
        ("*.md", "README.md", True),
        ("*.md", "docs/README.md", False),
        ("**/AGENTS.md", "AGENTS.md", True),
        ("cmd/install/assets/crds/*.go", "cmd/install/assets/crds/sub/x.go", False),
        (".*", ".gitignore", True),
        (".*/**", ".github/workflows/a b.yaml", True),
    ],
)
def test_match_path_semantics(pattern: str, path: str, expected: bool) -> None:
    assert commit_layout.match_path(pattern, path) is expected


@pytest.mark.parametrize(
    "path,group",
    [
        ("go.mod", "Dependencies"),
        ("go.sum", "Dependencies"),
        ("hack/tools/go.mod", "Dependencies"),
        ("hack/workspace/go.work", "Dependencies"),
        ("api/go.mod", "API"),
        ("api/go.sum", "API"),
        ("api/.golangci.yml", "API"),
        ("vendor/modules.txt", "Vendor"),
        ("api/vendor/modules.txt", "Vendor"),
        ("api/vendor/k8s.io/api/core/v1/x_test.go", "Vendor"),
        ("hack/tools/vendor/modules.txt", "Vendor"),
        ("client/clientset/clientset/clientset.go", "Vendor"),
        ("api/hypershift/v1beta1/zz_generated.deepcopy.go", "Vendor"),
        ("api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests/hostedclusters/AAA.yaml", "Vendor"),
        ("api/hypershift/v1beta1/hostedcluster_types_test.go", "E2E"),
        ("api/AGENTS.md", "Docs"),
        ("cmd/install/assets/crds/hypershift-operator/zz_generated.crd-manifests/hostedclusters.crd.yaml", "Vendor"),
        ("cmd/install/assets/crds/hypershift-operator/zz_generated.crd-manifests/doc.go", "CLI"),
        ("cmd/install/assets/crds/assets.go", "CLI"),
        ("cmd/install/assets/crds/hypershift-operator/tests/nodepools.hypershift.openshift.io/a.yaml", "E2E"),
        ("cmd/infra/aws/delegating_client.go", "Vendor"),
        ("cmd/cluster/aws/testdata/zz_fixture_TestCreate.yaml", "CLI"),
        ("karpenter-operator/controllers/karpenter/assets/karpenter.sh_nodepools.yaml", "Vendor"),
        ("karpenter-operator/controllers/karpenter/assets/assets.go", "HO"),
        ("karpenter-operator/controllers/karpenter/assets/tests/x/stable.testsuite.yaml", "E2E"),
        ("shared-ingress/Containerfile", "Tooling"),
        ("ignition-server/main.go", "HO"),
        ("etcd-recovery/etcdrecovery.go", "HO"),
        ("support/awsapi/ec2.go", "Vendor"),
        ("sharedingress-config-generator/config.go", "HO"),
        ("etcd-upload/uploader.go", "CPO"),
        ("control-plane-operator/controllers/x/testdata/zz_fixture_TestX.yaml", "CPO"),
        ("docs/content/reference/api.md", "Docs"),
        ("DEVELOPMENT.md", "Docs"),
        (".claude/skills/restructure-commits/SKILL.md", "Tooling"),
        ("skills/restructure-commits/commit_layout.py", "Tooling"),
        ("Makefile", "Tooling"),
        (".github/workflows/gitlint.yaml", "Tooling"),
        ("contrib/cleanroles/go.mod", "Tooling"),
    ],
)
def test_classification_specific_rules_win(path: str, group: str) -> None:
    ownership = commit_layout.Ownership.load()
    assert ownership.classify(path).name == group


def test_unknown_path_is_not_defaulted() -> None:
    assert commit_layout.Ownership.load().classify("brand-new-component/main.go") is None


def test_rules_must_reference_known_groups() -> None:
    with pytest.raises(commit_layout.UsageError):
        commit_layout.Ownership([], [commit_layout.Rule("x/**", "NotAGroup")])


def test_verify_rejects_module_definitions_mixed_with_vendor(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    sha = commit(repo, "chore(api): regenerate", {"go.mod": "module x\n// bump\n", "vendor/modules.txt": "# bump\n"})
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 1
    assert sha[:12] in out
    assert "Dependencies, Vendor" in out
    assert "module definitions must not share a commit with generated vendor trees" in out
    assert "go.mod" in out and "vendor/modules.txt" in out


def test_verify_accepts_api_modules_dependencies_and_vendor_in_separate_commits(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    commit(repo, "build(api): bump", {"api/go.mod": "module x/api\n// bump\n", "api/go.sum": "sum\n"})
    commit(repo, "build(deps): bump", {"go.mod": "module x\n// bump\n", "go.sum": "sum\n"})
    commit(repo, "chore(api): vendor", {"vendor/modules.txt": "# bump\n"})
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 0, out


def test_verify_rejects_root_and_api_modules_in_one_commit(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    commit(repo, "build: bump both", {"api/go.mod": "module x/api\n// again\n", "go.mod": "module x\n// again\n"})
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 1
    assert "mixes groups API, Dependencies" in out


def test_verify_rejects_out_of_order_groups(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    commit(repo, "chore(api): vendor", {"vendor/modules.txt": "x\n"})
    late = commit(repo, "feat(api): types", {"api/types.go": "package api\n"})
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 1
    assert late[:12] in out and "API (order 1) comes after" in out


def test_verify_allows_consecutive_commits_in_one_group(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    commit(repo, "feat(hypershift-operator): a", {"hypershift-operator/a.go": "package a\n"})
    commit(repo, "feat(hypershift-operator): b", {"support/b.go": "package b\n"})
    assert layout(repo, "verify", "--base", base)[0] == 0


def test_verify_reports_unclassified_paths(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    sha = commit(repo, "feat: new thing", {"brand-new/main.go": "package main\n"})
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 1
    assert f"{sha[:12]}" in out and "unclassified path 'brand-new/main.go'" in out


def test_plan_blocks_on_unknown_paths_and_skips_empty_groups(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    commit(repo, "wip", {"api/types.go": "package api\n", "mystery/file.txt": "?\n"})
    code, out, _ = layout(repo, "plan", "--base", base)
    assert code == 1
    assert "Commit 1: API (<type>(api))" in out
    assert "UNCLASSIFIED" in out and "mystery/file.txt" in out
    assert "Skipped empty groups: Dependencies, Vendor, CLI, HO, CPO, E2E, Docs, Tooling" in out


def test_plan_accounts_for_renames_deletions_and_spaces_once(repo: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    commit(repo, "seed", {"docs/old name.md": "doc\n", "hypershift-operator/gone.go": "package x\n"})
    base = git(repo, "rev-parse", "HEAD")
    head = commit(repo, "wip", {"cmd/new name.md": "doc\n"}, delete=("docs/old name.md", "hypershift-operator/gone.go"))
    assert plan_paths(repo, base, head, monkeypatch) == {
        "CLI": [("A", "cmd/new name.md")],
        "HO": [("D", "hypershift-operator/gone.go")],
        "Docs": [("D", "docs/old name.md")],
    }
    code, out, _ = layout(repo, "plan", "--base", base)
    assert code == 0, out
    assert "A  cmd/new name.md" in out and "D  docs/old name.md" in out


def test_verify_detects_tree_mismatch(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    original = commit(repo, "wip", {"hypershift-operator/a.go": "package a\n"})
    git(repo, "reset", "-q", "--hard", base)
    commit(repo, "feat(hypershift-operator): a", {"hypershift-operator/a.go": "package a // changed\n"})
    code, out, _ = layout(repo, "verify", "--base", base, "--expect-tree", original)
    assert code == 1 and "tree mismatch" in out and "hypershift-operator/a.go" in out


def test_verify_rejects_merge_commits(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    git(repo, "checkout", "-q", "-b", "side")
    commit(repo, "feat(hypershift-operator): side", {"hypershift-operator/side.go": "package s\n"})
    git(repo, "checkout", "-q", "main")
    commit(repo, "feat(hypershift-operator): main", {"hypershift-operator/main.go": "package m\n"})
    git(repo, "merge", "-q", "--no-edit", "side")
    code, out, _ = layout(repo, "verify", "--base", base)
    assert code == 1 and "merge commits are not allowed" in out


def test_stage_refuses_preexisting_staged_changes(repo: Path) -> None:
    base = git(repo, "rev-parse", "HEAD")
    original = commit(repo, "wip", {"hypershift-operator/a.go": "package a\n"})
    git(repo, "reset", "-q", base)
    write(repo, {"unrelated.txt": "user work\n"})
    git(repo, "add", "unrelated.txt")
    code, _, err = layout(repo, "stage", "--base", base, "--head", original, "--group", "HO")
    assert code == 1 and "already has staged changes" in err


def test_sample_branch_reproduces_pr_9568_and_restructures_cleanly(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tmp_path)
    work = sample_branch.create_sample(Path("sample"))  # relative, as on the command line
    base = git(work, "merge-base", "origin/main", "HEAD")
    original = git(work, "rev-parse", "HEAD")

    code, out, _ = layout(work, "verify", "--base", base)
    assert code == 1
    assert "module definitions must not share a commit with generated vendor trees" in out

    plan = {name: sorted(p for _, p in changes) for name, changes in plan_paths(work, base, original, monkeypatch).items()}
    assert plan == {
        "API": ["api/go.mod", "api/go.sum"],
        "Dependencies": ["go.mod", "go.sum"],
        "Vendor": [
            "api/hypershift/v1beta1/zz_generated.deepcopy.go",
            "api/vendor/k8s.io/api/core/v1/types.go",
            "api/vendor/modules.txt",
            "cmd/install/assets/crds/hypershift-operator/zz_generated.crd-manifests/hostedclusters.crd.yaml",
            "vendor/k8s.io/api/core/v1/types.go",
            "vendor/modules.txt",
        ],
        "HO": ["hypershift-operator/controllers/legacy/legacy.go"],
        "CPO": [
            "control-plane-operator/controllers/hostedcontrolplane/v2/kube_scheduler/testdata/"
            "zz_fixture_TestKubeScheduler.yaml"
        ],
        "E2E": [
            "cmd/install/assets/crds/hypershift-operator/tests/hostedclusters.hypershift.openshift.io/"
            "k8s-1.37.testsuite.yaml",
            "test/envtest/README.md",
        ],
        "Docs": ["AGENTS.md", "docs/content/how-to/k8s new guide.md", "docs/content/how-to/k8s old guide.md"],
        "Tooling": [".github/workflows/envtest-kube-reusable.yaml", "Makefile"],
    }

    _rewrite_all_groups(work, base, original)
    code, out, _ = layout(work, "verify", "--base", base, "--expect-tree", original)
    assert code == 0, out
    assert git(work, "status", "--porcelain") == ""


def _rewrite_all_groups(repo: Path, base: str, original: str) -> None:
    git(repo, "reset", "-q", "--soft", base)
    git(repo, "reset", "-q")
    for group in commit_layout.Ownership.load().groups:
        code, out, err = layout(repo, "stage", "--base", base, "--head", original, "--group", group.name)
        assert code == 0, err
        if "no changes" not in out:
            git(repo, "commit", "-q", "-m", group.name)
