"""Tests for generate_ownership.py; run with `make test-commit-layout`."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))

import commit_layout  # noqa: E402
import generate_ownership  # noqa: E402
from generate_ownership import Facts  # noqa: E402

BINARIES = {
    "hypershift": ("CLI", ""),
    "hypershift-operator": ("HO", "hypershift-operator"),
    "control-plane-operator": ("CPO", "control-plane-operator"),
}
PATHS = [
    "main.go", "go.mod", "go.sum", "Makefile", "README.md",
    "api/go.mod", "api/go.sum", "api/v1/types.go", "api/v1/types_test.go", "api/v1/zz_generated.deepcopy.go",
    "api/vendor/modules.txt", "api/vendor/k8s.io/api/zz_generated.deepcopy.go",
    "vendor/modules.txt", "vendor/k8s.io/api/x.go",
    "client/clientset.go", "client/informers.go",
    "cmd/install/install.go", "cmd/infra/client.go", "cmd/infra/create.go",
    "cmd/crds/doc.go", "cmd/crds/hostedclusters.yaml", "cmd/crds/tests/hc/stable.testsuite.yaml",
    "control-plane-operator/main.go", "hypershift-operator/main.go",
    "sidecar/main.go", "support/util.go", "brand-new-dir/main.go",
    "docs/index.md", "pkg/AGENTS.md", "test/e2e/e2e_test.go",
]


def classify(facts: Facts) -> dict[str, str]:
    groups = [commit_layout.Group(i + 1, g["name"], g["prefix"]) for i, g in enumerate(generate_ownership.GROUPS)]
    rules = [commit_layout.Rule(r["pattern"], r["group"]) for r in generate_ownership.build_rules(facts)]
    ownership = commit_layout.Ownership(groups, rules)
    return {p: ownership.classify(p).name for p in facts.paths}


@pytest.fixture
def facts() -> Facts:
    return Facts(
        paths=PATHS,
        binaries=BINARIES,
        importers={"sidecar": {"CPO"}, "support": {"CPO", "HO"}, "cmd": {"CLI", "HO"}},
        generated={"api/v1/zz_generated.deepcopy.go", "client/clientset.go", "client/informers.go",
                   "cmd/infra/client.go", "cmd/crds/hostedclusters.yaml"},
        output_dirs={"cmd/crds"},
    )


def test_components_follow_binaries_and_their_imports(facts: Facts) -> None:
    got = classify(facts)
    assert got["main.go"] == "CLI"
    assert got["cmd/install/install.go"] == "CLI"
    assert got["hypershift-operator/main.go"] == "HO"
    assert got["control-plane-operator/main.go"] == "CPO"
    assert got["sidecar/main.go"] == "CPO", "imported only by CPO binaries"
    assert got["support/util.go"] == "HO", "shared by several groups"
    assert got["brand-new-dir/main.go"] == "Tooling", "not part of any binary"


def test_generated_output_is_vendor_but_hand_written_files_keep_their_group(facts: Facts) -> None:
    got = classify(facts)
    assert got["client/clientset.go"] == "Vendor"
    assert got["cmd/infra/client.go"] == "Vendor"
    assert got["cmd/infra/create.go"] == "CLI"
    assert got["api/v1/zz_generated.deepcopy.go"] == "Vendor"
    assert got["cmd/crds/hostedclusters.yaml"] == "Vendor"
    assert got["cmd/crds/doc.go"] == "CLI"
    assert got["cmd/crds/tests/hc/stable.testsuite.yaml"] == "E2E"
    assert got["api/vendor/k8s.io/api/zz_generated.deepcopy.go"] == "Vendor"


def test_modules_conventions_and_documentation(facts: Facts) -> None:
    got = classify(facts)
    assert (got["go.mod"], got["go.sum"], got["api/go.mod"], got["api/go.sum"]) == (
        "Dependencies", "Dependencies", "API", "API")
    assert (got["vendor/modules.txt"], got["api/vendor/modules.txt"]) == ("Vendor", "Vendor")
    assert got["api/v1/types.go"] == "API"
    assert got["api/v1/types_test.go"] == "E2E"
    assert got["test/e2e/e2e_test.go"] == "E2E"
    assert (got["README.md"], got["docs/index.md"], got["pkg/AGENTS.md"]) == ("Docs", "Docs", "Docs")
    assert got["Makefile"] == "Tooling"


def test_skill_table_replaces_only_the_generated_block() -> None:
    begin, end = generate_ownership.BEGIN_MARKER, generate_ownership.END_MARKER
    text = f"intro {end}\n{begin}\nstale\n{end}\noutro\n"
    assert generate_ownership.rendered_skill(text) == f"intro {end}\n{generate_ownership.render_table()}\noutro\n"


def test_new_binaries_need_a_group(monkeypatch: pytest.MonkeyPatch) -> None:
    makefile = "build: new-operator\n\nnew-operator:\n\t$(GO) build -o bin/new-operator ./new-operator\n"
    with pytest.raises(SystemExit, match="BINARY_GROUPS"):
        generate_ownership.parse_binaries(makefile)
    monkeypatch.setitem(generate_ownership.BINARY_GROUPS, "new-operator", "HO")
    assert generate_ownership.parse_binaries(makefile) == {"new-operator": ("HO", "new-operator")}
