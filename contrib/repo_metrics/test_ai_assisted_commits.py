#!/usr/bin/env python3
"""
Unit tests for ai_assisted_commits.py

Uses real git log data from test_data.txt to verify functionality.
"""

import unittest
from typing import Dict, List
from ai_assisted_commits import (
    AI_PATTERNS,
    CommitStats,
    GitCommandRunner,
    analyze_commits,
    get_commit_body,
    get_commit_hashes,
    get_commit_oneline,
    is_ai_assisted,
    is_merge_commit,
)


class MockGitCommandRunner(GitCommandRunner):
    """Mock implementation for testing."""

    def __init__(self, test_data_file: str = 'test_data.txt'):
        """Initialize with test data from file."""
        self.commits: Dict[str, Dict[str, str]] = {}
        self._load_test_data(test_data_file)

    def _load_test_data(self, filename: str) -> None:
        """Load test data from file."""
        try:
            with open(filename, 'r') as f:
                content = f.read()
        except FileNotFoundError:
            import os
            script_dir = os.path.dirname(os.path.abspath(__file__))
            with open(os.path.join(script_dir, filename), 'r') as f:
                content = f.read()

        current_commit = None
        current_body_lines = []
        in_body = False

        for line in content.split('\n'):
            if line.startswith('COMMIT_START'):
                current_commit = {}
                current_body_lines = []
                in_body = False
            elif line.startswith('HASH: '):
                commit_hash = line.replace('HASH: ', '')
                current_commit['hash'] = commit_hash
            elif line.startswith('PARENTS: '):
                current_commit['parents'] = line.replace('PARENTS: ', '')
            elif line.startswith('SUBJECT: '):
                current_commit['subject'] = line.replace('SUBJECT: ', '')
            elif line.startswith('BODY:'):
                in_body = True
            elif line.startswith('COMMIT_END'):
                if current_commit:
                    commit_hash = current_commit['hash']
                    current_commit['body'] = '\n'.join(current_body_lines)
                    full_message = current_commit['subject']
                    if current_commit['body'].strip():
                        full_message += '\n\n' + current_commit['body']
                    current_commit['full_message'] = full_message
                    self.commits[commit_hash] = current_commit
                current_commit = None
                current_body_lines = []
                in_body = False
            elif in_body:
                current_body_lines.append(line)

    def run(self, args: List[str]) -> str:
        """Mock git command execution."""
        if args[0] == 'log' and '--since=' in args[1]:
            return '\n'.join(self.commits.keys())
        elif args[0] == 'show' and args[1] in self.commits:
            return self.commits[args[1]]['full_message']
        elif args[0] == 'rev-list' and len(args) > 4 and args[4] in self.commits:
            commit_hash = args[4]
            parents = self.commits[commit_hash]['parents'].split()
            return f"{commit_hash} {' '.join(parents)}"
        elif args[0] == 'log' and '--oneline' in args and len(args) > 3:
            commit_hash = args[3]
            if commit_hash in self.commits:
                short_hash = commit_hash[:9]
                subject = self.commits[commit_hash]['subject']
                return f"{short_hash} {subject}"
        return ""


class TestAIAssistedCommits(unittest.TestCase):
    """Test cases for AI-assisted commit analysis."""

    def setUp(self):
        """Set up test fixtures."""
        self.git_runner = MockGitCommandRunner()

    def test_is_ai_assisted_with_claude_marker(self):
        """Test detection of Claude co-authorship."""
        body = "Some commit\n\nCo-Authored-By: Claude <noreply@anthropic.com>"
        self.assertTrue(is_ai_assisted(body))

    def test_is_ai_assisted_with_assisted_by(self):
        """Test detection of Assisted-by marker."""
        body = "Some commit\n\nAssisted-by: GPT-5 (via Cursor)"
        self.assertTrue(is_ai_assisted(body))

    def test_is_ai_assisted_with_emoji(self):
        """Test detection of robot emoji with Generated marker."""
        body = "Some commit\n\n🤖 Generated with Claude Code"
        self.assertTrue(is_ai_assisted(body))

    def test_is_ai_assisted_with_commit_message_assisted(self):
        """Test detection of commit message assistance."""
        body = "Some commit\n\nCommit-Message-Assisted-by: Claude-3.5-Sonnet (via Cursor)"
        self.assertTrue(is_ai_assisted(body))

    def test_is_ai_assisted_negative(self):
        """Test that regular commits are not flagged."""
        body = "Regular commit\n\nSigned-off-by: Developer <dev@example.com>"
        self.assertFalse(is_ai_assisted(body))

    def test_analyze_commits(self):
        """Test full commit analysis with expected counts from test data."""
        stats = analyze_commits(self.git_runner, '2 weeks ago')

        self.assertIsInstance(stats, CommitStats)

        # Validate total counts
        self.assertEqual(stats.total_commits, 50)
        self.assertEqual(
            stats.merge_commits + stats.non_merge_commits,
            stats.total_commits
        )

        # Validate AI-assisted counts
        self.assertEqual(
            stats.ai_assisted_merge + stats.ai_assisted_non_merge,
            stats.ai_assisted_commits
        )
        self.assertGreaterEqual(stats.ai_assisted_commits, 0)
        self.assertLessEqual(stats.ai_assisted_commits, stats.total_commits)

        # Expected values from our test data (last 50 commits)
        # 26 merge commits (verified with: grep -i "Merge pull request" test_data.txt | wc -l)
        # These counts come from running analyze_commits on the test data
        self.assertEqual(stats.merge_commits, 26)
        self.assertEqual(stats.non_merge_commits, 24)
        self.assertEqual(stats.ai_assisted_commits, 11)
        self.assertEqual(stats.ai_assisted_non_merge, 11)
        self.assertEqual(stats.ai_assisted_merge, 0)

    def test_ai_patterns_present(self):
        """Test that AI patterns are properly defined."""
        self.assertGreater(len(AI_PATTERNS), 0)

    def test_stats_consistency(self):
        """Test that statistics are internally consistent."""
        stats = analyze_commits(self.git_runner, '2 weeks ago')

        self.assertEqual(len(stats.ai_commits), stats.ai_assisted_commits)

        self.assertGreaterEqual(stats.ai_assisted_commits, stats.ai_assisted_merge)
        self.assertGreaterEqual(stats.ai_assisted_commits, stats.ai_assisted_non_merge)


if __name__ == '__main__':
    unittest.main()
