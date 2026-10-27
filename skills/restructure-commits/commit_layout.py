#!/usr/bin/env python3
"""Plan, stage, and verify HyperShift component-based commit layouts.

The ownership rules live in ownership.json next to this script; generate_ownership.py
derives them from the repository (make update), so never edit them by hand.
Only the Python 3 standard library and the git CLI are required.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
RULES_FILE = SCRIPT_DIR / "ownership.json"

EXIT_OK = 0
EXIT_VIOLATION = 1
EXIT_USAGE = 2


class UsageError(Exception):
    pass


@dataclass(frozen=True)
class Group:
    order: int
    name: str
    prefix: str


@dataclass(frozen=True)
class Rule:
    pattern: str
    group: str


@dataclass(frozen=True)
class Change:
    status: str
    path: str


def match_path(pattern: str, path: str) -> bool:
    """Match a root-anchored glob where '**' spans zero or more whole segments."""
    return _match_segments(pattern.split("/"), path.split("/"))


def _match_segments(pattern: list[str], path: list[str]) -> bool:
    if not pattern:
        return not path
    head, rest = pattern[0], pattern[1:]
    if head == "**":
        return any(_match_segments(rest, path[i:]) for i in range(len(path) + 1))
    if not path:
        return False
    return fnmatch.fnmatchcase(path[0], head) and _match_segments(rest, path[1:])


class Ownership:
    def __init__(self, groups: list[Group], rules: list[Rule]):
        self.groups = groups
        self.rules = rules
        self._by_name = {g.name: g for g in groups}
        for rule in rules:
            if rule.group not in self._by_name:
                raise UsageError(f"rule {rule.pattern!r} references unknown group {rule.group!r}")

    @classmethod
    def load(cls, path: Path = RULES_FILE) -> "Ownership":
        data = json.loads(Path(path).read_text())
        groups = [Group(order=i + 1, name=g["name"], prefix=g["prefix"]) for i, g in enumerate(data["groups"])]
        return cls(groups, [Rule(r["pattern"], r["group"]) for r in data["rules"]])

    def group(self, name: str) -> Group:
        try:
            return self._by_name[name]
        except KeyError:
            raise UsageError(f"unknown group {name!r}; expected one of: {', '.join(self._by_name)}") from None

    def classify(self, path: str) -> Group | None:
        """Return the group of the first matching rule, or None when no rule matches."""
        for rule in self.rules:
            if match_path(rule.pattern, path):
                return self._by_name[rule.group]
        return None


def git(*args: str, stdin: bytes | None = None, env: dict[str, str] | None = None) -> bytes:
    proc = subprocess.run(
        ["git", "-c", "core.quotepath=off", *args],
        input=stdin,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        raise UsageError(f"git {' '.join(args)} failed: {proc.stderr.decode(errors='replace').strip()}")
    return proc.stdout


def resolve(rev: str, kind: str = "commit") -> str:
    return git("rev-parse", "--verify", "--end-of-options", f"{rev}^{{{kind}}}").decode().strip()


def decode_paths(raw: bytes) -> list[str]:
    return [p.decode("utf-8", errors="surrogateescape") for p in raw.split(b"\0") if p]


def diff_changes(old: str, new: str) -> list[Change]:
    """List changed paths between two commits. Renames are reported as delete plus add."""
    fields = decode_paths(git("diff", "--no-renames", "--no-ext-diff", "--name-status", "-z", old, new, "--"))
    return [Change(fields[i], fields[i + 1]) for i in range(0, len(fields), 2)]


def build_plan(ownership: Ownership, changes: list[Change]) -> tuple[dict[str, list[Change]], list[Change]]:
    by_group: dict[str, list[Change]] = {g.name: [] for g in ownership.groups}
    unclassified = []
    for change in sorted(changes, key=lambda c: c.path):
        group = ownership.classify(change.path)
        if group is None:
            unclassified.append(change)
        else:
            by_group[group.name].append(change)
    return by_group, unclassified


def resolve_range(base: str, head: str) -> tuple[str, str]:
    base_sha, head_sha = resolve(base), resolve(head)
    try:
        git("merge-base", "--is-ancestor", base_sha, head_sha)
    except UsageError:
        raise UsageError(f"base {base} is not an ancestor of head {head}; pass the merge base with the PR target") from None
    return base_sha, head_sha


def load_plan(args: argparse.Namespace) -> tuple[Ownership, str, str, dict[str, list[Change]], list[Change]]:
    ownership = Ownership.load()
    base, head = resolve_range(args.base, args.head)
    by_group, unclassified = build_plan(ownership, diff_changes(base, head))
    return ownership, base, head, by_group, unclassified


def cmd_plan(args: argparse.Namespace) -> int:
    ownership, base, head, by_group, unclassified = load_plan(args)
    total = sum(len(v) for v in by_group.values()) + len(unclassified)
    print(f"Commit layout plan for {base[:12]}..{head[:12]} ({total} changed paths)")
    commit_no = 0
    empty = []
    for g in ownership.groups:
        changes = by_group[g.name]
        if not changes:
            empty.append(g.name)
            continue
        commit_no += 1
        print(f"\nCommit {commit_no}: {g.name} ({g.prefix}) - {len(changes)} path(s)")
        for c in changes:
            print(f"  {c.status:<2} {c.path}")
    if empty:
        print(f"\nSkipped empty groups: {', '.join(empty)}")
    if unclassified:
        print("\nUNCLASSIFIED paths - add a rule to ownership.json before rewriting history:")
        for c in unclassified:
            print(f"  {c.status:<2} {c.path}")
        return EXIT_VIOLATION
    return EXIT_OK


def cmd_stage(args: argparse.Namespace) -> int:
    """Stage exactly one group's paths with their content from --head (the recorded original)."""
    ownership, _, head, by_group, unclassified = load_plan(args)
    target = ownership.group(args.group)
    if unclassified:
        print(f"error: {len(unclassified)} unclassified path(s); run plan and resolve them first", file=sys.stderr)
        return EXIT_VIOLATION
    if git("diff", "--cached", "--name-only", "-z"):
        print("error: the index already has staged changes; commit or unstage them before staging a group", file=sys.stderr)
        return EXIT_VIOLATION
    paths = [c.path for c in by_group[target.name]]
    if not paths:
        print(f"{target.name}: no changes; skip this group")
        return EXIT_OK

    git(
        "restore", f"--source={head}", "--staged", "--pathspec-from-file=-", "--pathspec-file-nul",
        stdin="".join(p + "\0" for p in paths).encode("utf-8", errors="surrogateescape"),
        env=dict(os.environ, GIT_LITERAL_PATHSPECS="1"),
    )

    staged = set(decode_paths(git("diff", "--cached", "--no-renames", "--name-only", "-z")))
    expected = set(paths)
    if staged != expected:
        print(f"error: staged paths do not match the {target.name} plan", file=sys.stderr)
        for p in sorted(expected - staged):
            print(f"  missing: {p}", file=sys.stderr)
        for p in sorted(staged - expected):
            print(f"  unexpected: {p}", file=sys.stderr)
        return EXIT_VIOLATION
    print(f"Staged {len(paths)} path(s) for {target.name}; commit with prefix {target.prefix}")
    return EXIT_OK


def cmd_verify(args: argparse.Namespace) -> int:
    ownership = Ownership.load()
    base, head = resolve_range(args.base, args.head)
    errors: list[str] = []

    log = git("log", "--no-show-signature", "--reverse", "--topo-order", "--format=%H %P%x00%s", f"{base}..{head}").decode()
    commits = [line.split("\0", 1) for line in log.split("\n") if line]
    if not commits:
        errors.append(f"no commits in {base[:12]}..{head[:12]}")

    last_group: Group | None = None
    last_commit = ""
    for shas, subject in commits:
        sha, *parents = shas.split()
        label = f"{sha[:12]} {subject!r}"
        if len(parents) != 1:
            errors.append(f"{label}: merge commits are not allowed in a restructured branch")
            continue
        changes = diff_changes(parents[0], sha)
        if not changes:
            errors.append(f"{label}: commit changes no files")
            continue
        by_group, unclassified = build_plan(ownership, changes)
        present = [g for g in ownership.groups if by_group[g.name]]
        for c in unclassified:
            errors.append(f"{label}: unclassified path {c.path!r}")
        if len(present) > 1:
            detail = "; ".join(f"{g.name}: {', '.join(c.path for c in by_group[g.name][:5])}"
                               + (" ..." if len(by_group[g.name]) > 5 else "") for g in present)
            names = {g.name for g in present}
            hint = ""
            if "Vendor" in names and names & {"API", "Dependencies"}:
                hint = " (module definitions must not share a commit with generated vendor trees)"
            errors.append(f"{label}: mixes groups {', '.join(g.name for g in present)}{hint}: {detail}")
        if len(present) == 1:
            group = present[0]
            if last_group is not None and group.order < last_group.order:
                errors.append(
                    f"{label}: {group.name} (order {group.order}) comes after {last_commit} "
                    f"{last_group.name} (order {last_group.order}); expected order "
                    f"{' -> '.join(g.name for g in ownership.groups)}"
                )
            last_group, last_commit = group, sha[:12]

    if args.expect_tree:
        expected, actual = resolve(args.expect_tree, "tree"), resolve(head, "tree")
        if expected != actual:
            changed = diff_changes(args.expect_tree, head)
            listing = ", ".join(f"{c.status} {c.path}" for c in changed[:10]) + (" ..." if len(changed) > 10 else "")
            errors.append(f"tree mismatch: head tree {actual[:12]} != expected {expected[:12]}: {listing}")

    if errors:
        print(f"Commit layout verification FAILED for {base[:12]}..{head[:12]}:")
        for e in errors:
            print(f"  - {e}")
        return EXIT_VIOLATION
    print(f"Commit layout OK: {len(commits)} commit(s) in {base[:12]}..{head[:12]}"
          + (", tree matches expected snapshot" if args.expect_tree else ""))
    return EXIT_OK


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)

    def add_range(p: argparse.ArgumentParser) -> None:
        p.add_argument("--base", required=True, help="merge base with the PR target branch")
        p.add_argument("--head", default="HEAD", help="last commit of the range (default: HEAD)")

    p = sub.add_parser("plan", help="show the file-to-commit plan for base..head")
    add_range(p)
    p.set_defaults(func=cmd_plan)

    p = sub.add_parser("stage", help="stage one group's paths with content from --head")
    add_range(p)
    p.add_argument("--group", required=True)
    p.set_defaults(func=cmd_stage)

    p = sub.add_parser("verify", help="verify file membership and group order of each commit in base..head")
    add_range(p)
    p.add_argument("--expect-tree", help="commit or tree that HEAD's tree must equal (the pre-rewrite snapshot)")
    p.set_defaults(func=cmd_verify)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        return args.func(args)
    except UsageError as e:
        print(f"error: {e}", file=sys.stderr)
        return EXIT_USAGE


if __name__ == "__main__":
    sys.exit(main())
