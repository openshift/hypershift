#!/usr/bin/env python3
"""Generate ownership.json and the SKILL.md group table from facts in the repository.

Run from the repository root; `make update` runs it. Only the POLICY section is written
by hand. Every path rule is derived from:

- binaries: the Makefile `build` target and each binary's `-o <out> <dir>` recipe
- components: `go list -deps` of each binary
- module definitions and vendor trees: go.mod, go.work, and vendor/modules.txt locations
- generated files: Go "Code generated ... DO NOT EDIT." headers, the controller-gen CRD
  annotation, and the directories that Makefile recipes write generated output to
- envtest suites: directories named tests that contain *.testsuite.yaml files
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from commit_layout import RULES_FILE, match_path  # noqa: E402

SKILL_FILE = SCRIPT_DIR / "SKILL.md"
BEGIN_MARKER = "<!-- BEGIN GENERATED: python3 skills/restructure-commits/generate_ownership.py -->"
END_MARKER = "<!-- END GENERATED -->"
GO_MODULE = "github.com/openshift/hypershift/"

# ---- POLICY (hand-written) -------------------------------------------------------------

GROUPS = [
    {"name": "API", "prefix": "<type>(api)", "types": ["feat", "fix", "build", "refactor"],
     "owns": "API types, markers, and the api/ module definition (api/go.mod and api/go.sum stay together)"},
    {"name": "Dependencies", "prefix": "build(deps)", "types": ["build"],
     "owns": "Root, tools, and workspace module definitions; never mixed with generated vendor trees"},
    {"name": "Vendor", "prefix": "chore(api)", "types": ["chore"],
     "owns": "Generated output: vendor trees, clients, zz_generated files, generated CRD manifests, and other files marked as generated"},
    {"name": "CLI", "prefix": "<type>(cli)", "types": ["feat", "fix", "refactor", "test"],
     "owns": "hypershift and hcp CLIs, with their tests, testdata, and fixtures"},
    {"name": "HO", "prefix": "<type>(hypershift-operator)", "types": ["feat", "fix", "refactor", "perf", "test"],
     "owns": "hypershift-operator binaries and Go code shared by binaries of more than one group"},
    {"name": "CPO", "prefix": "<type>(control-plane-operator)", "types": ["feat", "fix", "refactor", "perf", "test"],
     "owns": "control-plane-operator binaries and the Go code only they use, such as control plane sidecars"},
    {"name": "E2E", "prefix": "test(e2e)", "types": ["test"],
     "owns": "E2E, integration, and envtest suites, including API validation tests"},
    {"name": "Docs", "prefix": "docs", "types": ["docs"],
     "owns": "User and contributor documentation and agent guidance files"},
    {"name": "Tooling", "prefix": "<type>(<scope>)", "types": ["build", "ci", "chore", "docs"],
     "owns": "Everything else: build system, CI configuration, scripts, container builds, helper modules, AI agent tooling"},
]

# Group of each binary built by `make build`. Generation fails for a binary missing here.
BINARY_GROUPS = {
    "hypershift": "CLI",
    "product-cli": "CLI",
    "hypershift-operator": "HO",
    "karpenter-operator": "HO",
    "control-plane-operator": "CPO",
    "control-plane-pki-operator": "CPO",
}
# The hypershift CLI is built from the repository root; its commands live in these directories.
ROOT_BINARY_SOURCES = ("cmd",)
# Group for top-level Go code that binaries of more than one group import.
SHARED_GROUP = "HO"
# Module definitions by module directory ("" is the repository root). Other modules follow
# the group of the directory that contains them.
MODULE_GROUPS = {"api": "API", "": "Dependencies", "hack/tools": "Dependencies"}
WORKSPACE_GROUP = "Dependencies"
DOC_DIRS = ("docs", "examples")
# Conventions that win over generated-file and directory rules, in order.
CONVENTIONS = [
    ("**/AGENTS.md", "Docs", "agent guidance"),
    ("**/CLAUDE.md", "Docs", "agent guidance"),
    ("test/**", "E2E", "e2e, integration, and envtest suites"),
    ("api/**/*_test.go", "E2E", "API validation tests"),
]
# Path conventions for generated output, applied after per-file exceptions.
GENERATED_PATTERNS = ["**/zz_generated*", "**/zz_generated*/**"]

# ---- FACTS -------------------------------------------------------------------------------

GENERATED_GO = re.compile(rb"^// Code generated .* DO NOT EDIT\.$", re.M)
CRD_MARKER = b"controller-gen.kubebuilder.io/version"
MAKE_OUTPUT_DIRS = [
    re.compile(r"output:crd:artifacts:config=(\S+)"),
    re.compile(r"^\tcp \S+ (\S+?)/?$", re.M),
    re.compile(r"^\tmv \S+ (\S+?)/?$", re.M),
]


@dataclass
class Facts:
    paths: list[str]
    binaries: dict[str, tuple[str, str]]  # make target -> (group, package directory, "" for the root)
    importers: dict[str, set[str]] = field(default_factory=dict)  # top-level directory -> importing groups
    generated: set[str] = field(default_factory=set)  # paths whose content is marked as generated
    output_dirs: set[str] = field(default_factory=set)  # directories Makefile recipes write generated files to


def git(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, stdout=subprocess.PIPE, text=True).stdout


def read_head(path: str, size: int = 4096) -> bytes:
    try:
        with open(path, "rb") as f:
            return f.read(size)
    except OSError:
        return b""


def parse_binaries(makefile: str) -> dict[str, tuple[str, str]]:
    build = re.search(r"^build:(.*)$", makefile, re.M)
    if not build:
        raise SystemExit("error: Makefile has no build target")
    binaries = {}
    for target in build.group(1).split():
        recipe = re.search(rf"^{re.escape(target)}:.*\n((?:\t.*\n)+)", makefile, re.M)
        out = recipe and re.search(r"-o \S+ (\.\S*)\s*$", recipe.group(1), re.M)
        if not out:
            raise SystemExit(f"error: cannot find the package built by make {target}")
        if target not in BINARY_GROUPS:
            raise SystemExit(f"error: add make {target} to BINARY_GROUPS in {Path(__file__).name}")
        binaries[target] = (BINARY_GROUPS[target], out.group(1).removeprefix(".").strip("/"))
    return binaries


def parse_output_dirs(makefile: str) -> set[str]:
    dirs = set()
    for pattern in MAKE_OUTPUT_DIRS:
        for d in pattern.findall(makefile):
            d = d.removeprefix("./").rstrip("/")
            if "$" not in d and "*" not in d and not d.startswith(("/", ".")) and Path(d).is_dir():
                dirs.add(d)
    return dirs


def is_generated(path: str) -> bool:
    if path.endswith(".go"):
        return bool(GENERATED_GO.search(read_head(path, 2048)))
    if path.endswith((".yaml", ".yml")):
        return CRD_MARKER in read_head(path)
    return False


def collect_facts() -> Facts:
    raw = git("ls-files", "-z", "--cached", "--others", "--exclude-standard")
    paths = sorted({p for p in raw.split("\0") if p and Path(p).exists()})
    makefile = Path("Makefile").read_text()
    facts = Facts(paths=paths, binaries=parse_binaries(makefile), output_dirs=parse_output_dirs(makefile))
    importers: dict[str, set[str]] = defaultdict(set)
    for group, pkg_dir in facts.binaries.values():
        for pkg in go_deps(pkg_dir):
            if pkg.startswith(GO_MODULE):
                importers[pkg[len(GO_MODULE):].split("/")[0]].add(group)
    facts.importers = dict(importers)
    facts.generated = {p for p in paths if "/vendor/" not in f"/{p}" and is_generated(p)}
    return facts


def go_deps(pkg_dir: str) -> list[str]:
    env = {**os.environ, "GO111MODULE": "on", "GOWORK": "off", "GOFLAGS": "-mod=vendor"}  # as the Makefile's GO
    out = subprocess.run(["go", "list", "-deps", f"./{pkg_dir}"], check=True, stdout=subprocess.PIPE, text=True,
                         env=env)
    return out.stdout.split()


# ---- RULES -------------------------------------------------------------------------------


def rule(pattern: str, group: str, why: str) -> dict[str, str]:
    return {"pattern": pattern, "group": group, "why": why}


def top_level_group(top: str, is_dir: bool, facts: Facts) -> tuple[str, str]:
    """Return the structural group of a top-level entry and the reason."""
    if not is_dir:
        if top.endswith(".md"):
            return "Docs", "root documentation"
        if top.endswith(".go") and any(d == "" for _, d in facts.binaries.values()):
            group = next(g for g, d in facts.binaries.values() if d == "")
            return group, "source of the binary built from the repository root"
        return "Tooling", "root file"
    if top in DOC_DIRS:
        return "Docs", "documentation"
    if top in MODULE_GROUPS:
        return MODULE_GROUPS[top], f"module {top}/"
    for target, (group, pkg_dir) in facts.binaries.items():
        if pkg_dir == top:
            return group, f"package of make {target}"
        if pkg_dir == "" and top in ROOT_BINARY_SOURCES:
            return group, f"commands of make {target}"
    groups = facts.importers.get(top, set())
    if len(groups) == 1:
        group = next(iter(groups))
        return group, f"imported only by {group} binaries"
    if groups:
        return SHARED_GROUP, f"shared by {', '.join(sorted(groups))} binaries"
    return "Tooling", "not part of any binary"


def collapse(paths: set[str], universe: list[str]) -> list[str]:
    """Express paths as the fewest dir/** patterns and file paths that match nothing else in universe."""
    children: dict[str, set[str]] = defaultdict(set)
    for p in universe:
        parts = p.split("/")
        for i in range(1, len(parts)):
            children["/".join(parts[:i])].add(p)
    full = {d for d, members in children.items() if members <= paths}
    patterns = [f"{d}/**" for d in sorted(full) if not any(d.startswith(f"{o}/") for o in full)]
    covered = {p for d in patterns for p in children[d[:-3]]}
    return patterns + sorted(paths - covered)


def build_rules(facts: Facts) -> list[dict[str, str]]:
    paths = facts.paths
    rules: list[dict[str, str]] = []

    def first_match(path: str) -> dict[str, str] | None:
        return next((r for r in rules if match_path(r["pattern"], path)), None)

    vendored = sorted(p.removesuffix("vendor/modules.txt") for p in paths if p.endswith("vendor/modules.txt")
                      and p.count("vendor/") == 1)
    modules = sorted(p.removesuffix("go.mod").rstrip("/") for p in paths if p.endswith("go.mod")
                     and "vendor/" not in p)
    testsuite_dirs = sorted({p.rsplit("/tests/", 1)[0] + "/tests" for p in paths
                             if p.endswith(".testsuite.yaml") and "/tests/" in p})
    tops = {p.split("/")[0]: "/" in p for p in paths}

    for v in vendored:
        rules.append(rule(f"{v}vendor/**", "Vendor", f"vendor tree of module {v.rstrip('/') or 'root'}"))
    # Hand-written files inside generated-output paths keep their directory's group.
    for p in paths:
        if (p.endswith(".go") and p not in facts.generated and any(match_path(g, p) for g in GENERATED_PATTERNS)
                and not first_match(p)):
            group, why = top_level_group(p.split("/")[0], True, facts)
            rules.append(rule(p, group, f"hand-written file in generated output; {why}"))
    rules.extend(rule(p, g, why) for p, g, why in CONVENTIONS)
    rules.extend(rule(f"{d}/**", "E2E", "envtest suites") for d in testsuite_dirs)
    rules.extend(rule(p, "Vendor", "zz_generated output") for p in GENERATED_PATTERNS)

    out_dirs = sorted(d for d in facts.output_dirs if not first_match(f"{d}/x")
                      and not any(d.startswith(f"{o}/") for o in facts.output_dirs))
    for d in out_dirs:
        for p in paths:
            if p.startswith(f"{d}/") and p.endswith(".go") and p not in facts.generated and not first_match(p):
                group, why = top_level_group(p.split("/")[0], True, facts)
                rules.append(rule(p, group, f"hand-written file in generated output; {why}"))
    rules.extend(rule(f"{d}/**", "Vendor", "generated by a Makefile recipe") for d in out_dirs)

    marked = {p for p in facts.generated if not first_match(p)}
    rules.extend(rule(p, "Vendor", "marked as generated") for p in collapse(marked, paths))

    for m in modules:
        group = MODULE_GROUPS.get(m)
        if group:
            for name in ("go.mod", "go.sum"):
                rules.append(rule(f"{m}/{name}" if m else name, group, f"definition of module {m or 'root'}"))
    for p in paths:
        if p.rsplit("/", 1)[-1] in ("go.work", "go.work.sum") and not first_match(p):
            rules.append(rule(p, WORKSPACE_GROUP, "Go workspace definition"))

    for top, is_dir in sorted(tops.items()):
        pattern = f"{top}/**" if is_dir else top
        if any(r["pattern"] == pattern for r in rules):
            continue
        group, why = top_level_group(top, is_dir, facts)
        rules.append(rule(pattern, group, why))
    return rules


def ownership_document(facts: Facts) -> dict:
    return {
        "description": "GENERATED by skills/restructure-commits/generate_ownership.py (make update); do not edit. "
                       "Groups are listed in commit order. Rules are evaluated top to bottom and the first matching "
                       "pattern wins. Patterns are anchored at the repository root; '**' matches zero or more path "
                       "segments and other wildcards never cross '/'.",
        "groups": GROUPS,
        "rules": build_rules(facts),
    }


def to_json(document: dict) -> str:
    """Serialize with one group or rule per line so regenerated diffs stay reviewable."""
    def items(key: str) -> str:
        return ",\n".join(f"    {json.dumps(item)}" for item in document[key])
    return (f'{{\n  "description": {json.dumps(document["description"])},\n'
            f'  "groups": [\n{items("groups")}\n  ],\n  "rules": [\n{items("rules")}\n  ]\n}}\n')


def render_table() -> str:
    lines = [
        BEGIN_MARKER,
        "| Order | Group | Commit prefix | Allowed `<type>` | Owns |",
        "|-------|-------|---------------|------------------|------|",
    ]
    for i, g in enumerate(GROUPS, 1):
        types = ", ".join(f"`{t}`" for t in g["types"]) if "<type>" in g["prefix"] else "fixed"
        lines.append(f"| {i} | {g['name']} | `{g['prefix']}` | {types} | {g['owns']} |")
    lines.append(END_MARKER)
    return "\n".join(lines)


def rendered_skill(text: str) -> str:
    """Return SKILL.md text with the generated table replaced."""
    start = text.find(BEGIN_MARKER)
    end = text.find(END_MARKER, start + len(BEGIN_MARKER)) if start >= 0 else -1
    if start < 0 or end < 0:
        raise SystemExit(f"error: generated table markers not found in {SKILL_FILE}")
    return text[:start] + render_table() + text[end + len(END_MARKER):]


def main() -> int:
    if not Path("Makefile").is_file() or not Path("go.mod").is_file():
        print("error: run from the repository root", file=sys.stderr)
        return 2
    RULES_FILE.write_text(to_json(ownership_document(collect_facts())))
    SKILL_FILE.write_text(rendered_skill(SKILL_FILE.read_text()))
    return 0


if __name__ == "__main__":
    sys.exit(main())
