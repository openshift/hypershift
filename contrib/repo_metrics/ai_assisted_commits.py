#!/usr/bin/env python3
"""
Generate a report of AI-assisted commits in the repository.

This script analyzes git commit messages to identify commits that were
assisted by AI tools (Claude, GPT, etc.) and generates statistics.
"""

import re
import subprocess
import sys
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import List, Set, Tuple


@dataclass
class CommitStats:
    """Statistics about commits in the repository."""

    total_commits: int
    merge_commits: int
    non_merge_commits: int
    ai_assisted_commits: int
    ai_assisted_non_merge: int
    ai_assisted_merge: int
    ai_commits: List[str]


AI_PATTERNS = [
    re.compile(r'^\s*Assisted-by:', re.MULTILINE | re.IGNORECASE),
    re.compile(r'^\s*Co-authored-by:\s+Claude', re.MULTILINE | re.IGNORECASE),
    re.compile(r'🤖\s*Generated', re.IGNORECASE),
    re.compile(r'^\s*Commit-Message-Assisted-by:', re.MULTILINE | re.IGNORECASE),
]


class GitCommandRunner:
    """Interface for running git commands."""

    def run(self, args: List[str]) -> str:
        """Run a git command and return its output."""
        raise NotImplementedError


class RealGitCommandRunner(GitCommandRunner):
    """Real implementation that executes git commands."""

    def run(self, args: List[str]) -> str:
        """Run a git command and return its output."""
        try:
            result = subprocess.run(
                ['git'] + args,
                capture_output=True,
                text=True,
                check=True
            )
            return result.stdout.strip()
        except subprocess.CalledProcessError as e:
            print(f"Error running git command: {e}", file=sys.stderr)
            sys.exit(1)


def get_commit_hashes(git_runner: GitCommandRunner, since: str = '', until: str = '', max_count: int = 0) -> List[str]:
    """Get all commit hashes since a given date or up to max_count commits."""
    args = ['log']
    if max_count > 0:
        args.extend([f'-{max_count}'])
    elif since or until:
        if since:
            args.extend([f'--since={since}'])
        if until:
            args.extend([f'--until={until}'])
    args.append('--pretty=format:%H')
    output = git_runner.run(args)
    return output.split('\n') if output else []


def get_commit_body(git_runner: GitCommandRunner, commit_hash: str) -> str:
    """Get the full commit message body for a given commit hash."""
    return git_runner.run(['show', commit_hash, '--quiet', '--format=%B'])


def is_merge_commit(git_runner: GitCommandRunner, commit_hash: str) -> bool:
    """Check if a commit is a merge commit."""
    output = git_runner.run(['rev-list', '--parents', '-n', '1', commit_hash])
    parents = output.split()
    return len(parents) > 2


def is_ai_assisted(commit_body: str) -> bool:
    """Check if a commit body contains AI assistance markers."""
    return any(pattern.search(commit_body) for pattern in AI_PATTERNS)


def get_commit_oneline(git_runner: GitCommandRunner, commit_hash: str) -> str:
    """Get the one-line summary of a commit."""
    return git_runner.run(['log', '--oneline', '-1', commit_hash])


def analyze_commits(git_runner: GitCommandRunner, since: str = '2 weeks ago', until: str = '', max_count: int = 0) -> CommitStats:
    """Analyze commits and return statistics."""
    commit_hashes = get_commit_hashes(git_runner, since=since, until=until, max_count=max_count)
    total_commits = len(commit_hashes)

    merge_commits = 0
    non_merge_commits = 0
    ai_assisted_hashes: Set[str] = set()
    ai_assisted_merge = 0
    ai_assisted_non_merge = 0
    ai_commit_details: List[Tuple[str, str]] = []

    for commit_hash in commit_hashes:
        body = get_commit_body(git_runner, commit_hash)
        is_merge = is_merge_commit(git_runner, commit_hash)
        is_ai = is_ai_assisted(body)

        if is_merge:
            merge_commits += 1
            if is_ai:
                ai_assisted_merge += 1
        else:
            non_merge_commits += 1
            if is_ai:
                ai_assisted_non_merge += 1

        if is_ai:
            ai_assisted_hashes.add(commit_hash)
            oneline = get_commit_oneline(git_runner, commit_hash)
            ai_commit_details.append((commit_hash, oneline))

    return CommitStats(
        total_commits=total_commits,
        merge_commits=merge_commits,
        non_merge_commits=non_merge_commits,
        ai_assisted_commits=len(ai_assisted_hashes),
        ai_assisted_non_merge=ai_assisted_non_merge,
        ai_assisted_merge=ai_assisted_merge,
        ai_commits=[detail[1] for detail in ai_commit_details]
    )


def print_report(stats: CommitStats, period: str = "last 2 weeks") -> None:
    """Print the statistics report."""
    print(f"=== AI-Assisted Commits Report ({period}) ===\n")

    print("Absolute Numbers:")
    print(f"  Total commits: {stats.total_commits}")
    print(f"    - Merge commits: {stats.merge_commits}")
    print(f"    - Non-merge commits: {stats.non_merge_commits}")
    print(f"  AI-assisted commits: {stats.ai_assisted_commits}")
    print(f"    - AI-assisted non-merge: {stats.ai_assisted_non_merge}")
    print(f"    - AI-assisted merge: {stats.ai_assisted_merge}")

    print("\nPercentages:")
    if stats.total_commits > 0:
        overall_pct = (stats.ai_assisted_commits / stats.total_commits) * 100
        print(f"  Overall AI-assisted: {overall_pct:.1f}% ({stats.ai_assisted_commits}/{stats.total_commits})")

    if stats.non_merge_commits > 0:
        non_merge_pct = (stats.ai_assisted_non_merge / stats.non_merge_commits) * 100
        print(f"  Non-merge AI-assisted: {non_merge_pct:.1f}% ({stats.ai_assisted_non_merge}/{stats.non_merge_commits})")

    if stats.ai_commits:
        print("\nAI-Assisted Commits:")
        for commit in stats.ai_commits:
            print(f"  {commit}")


def main() -> None:
    """Main entry point."""
    import argparse

    parser = argparse.ArgumentParser(
        description='Analyze AI-assisted commits in the repository'
    )
    parser.add_argument(
        '--since',
        help='Analyze commits since this date (e.g., "2 weeks ago", "2025-01-01")'
    )
    parser.add_argument(
        '--until',
        help='Analyze commits until this date (e.g., "1 week ago", "2025-01-15")'
    )
    parser.add_argument(
        '-n', '--max-count',
        type=int,
        help='Analyze last N commits'
    )

    args = parser.parse_args()

    # Validate arguments
    if args.max_count and (args.since or args.until):
        parser.error("Cannot specify --max-count with --since or --until")

    if not args.since and not args.max_count and not args.until:
        args.since = '2 weeks ago'

    git_runner = RealGitCommandRunner()
    stats = analyze_commits(git_runner, since=args.since or '', until=args.until or '', max_count=args.max_count or 0)

    if args.max_count:
        period = f"last {args.max_count} commits"
    elif args.since and args.until:
        period = f"{args.since} to {args.until}"
    elif args.since:
        period = f"since {args.since}"
    elif args.until:
        period = f"until {args.until}"
    else:
        period = "last 2 weeks"
    print_report(stats, period=period)


if __name__ == '__main__':
    main()
