"""Tests for verify-tekton-pipeline-pairs.py — covers normalise_cel()."""
import importlib.util
import os
import sys

import pytest

# Import the verify script as a module (filename contains hyphens).
_script = os.path.join(os.path.dirname(__file__), "verify-tekton-pipeline-pairs.py")
_spec = importlib.util.spec_from_file_location("verify_tekton", _script)
_mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_mod)
normalise_cel = _mod.normalise_cel


class TestNormaliseCel:
    """Unit tests for normalise_cel()."""

    def test_strips_positive_guard(self):
        cel = 'event == "push" && ".tekton/***".pathChanged()'
        assert '".tekton/***".pathChanged()' not in normalise_cel(cel)

    def test_strips_negated_guard(self):
        cel = 'event == "push" && !".tekton/***".pathChanged()'
        assert '".tekton/***".pathChanged()' not in normalise_cel(cel)

    def test_positive_and_negated_produce_same_base(self):
        pos = 'event == "push" && ".tekton/***".pathChanged()'
        neg = 'event == "push" && !".tekton/***".pathChanged()'
        assert normalise_cel(pos) == normalise_cel(neg)

    def test_preserves_non_guard_content(self):
        cel = 'event == "push" && ".tekton/***".pathChanged()'
        assert 'event == "push"' in normalise_cel(cel)

    def test_none_input_returns_empty(self):
        assert normalise_cel(None) == ""

    def test_empty_string_returns_empty(self):
        assert normalise_cel("") == ""

    def test_no_guard_clause_unchanged(self):
        cel = 'event == "pull_request"'
        assert normalise_cel(cel) == cel

    def test_multiline_block_style(self):
        cel = (
            'event == "pull_request"\n'
            '&& ".tekton/***".pathChanged()\n'
        )
        result = normalise_cel(cel)
        assert 'event == "pull_request"' in result
        assert '".tekton/***".pathChanged()' not in result

    def test_whitespace_collapsed(self):
        cel = '  event == "push"   &&   ".tekton/***".pathChanged()  '
        result = normalise_cel(cel)
        # No leading/trailing whitespace or double spaces.
        assert result == result.strip()
        assert "  " not in result

    def test_strips_specific_negated_guard(self):
        cel = (
            'event == "pull_request" && target_branch == "main"'
            ' && !".tekton/pipelines/common-operator-build.yaml".pathChanged()'
            ' && !".tekton/foo-pull-request.yaml".pathChanged()'
        )
        result = normalise_cel(cel)
        assert '".tekton/pipelines/common-operator-build.yaml"' not in result
        assert '".tekton/foo-pull-request.yaml"' not in result
        assert 'event == "pull_request"' in result

    def test_strips_pr_branch_guard_block(self):
        cel = (
            'event == "pull_request"\n'
            '&& target_branch == "main"\n'
            '&& (".tekton/pipelines/common-operator-build.yaml".pathChanged()\n'
            '   || ".tekton/foo-pull-request.yaml".pathChanged())'
        )
        result = normalise_cel(cel)
        assert '".tekton/pipelines/common-operator-build.yaml"' not in result
        assert '".tekton/foo-pull-request.yaml"' not in result
        assert 'event == "pull_request"' in result

    def test_specific_guards_both_directions_same_base(self):
        pr_branch = (
            'event == "pull_request" && target_branch == "main"'
            ' && (".tekton/pipelines/common-operator-build.yaml".pathChanged()'
            ' || ".tekton/foo-pull-request.yaml".pathChanged())'
        )
        from_main = (
            'event == "pull_request" && target_branch == "main"'
            ' && !".tekton/pipelines/common-operator-build.yaml".pathChanged()'
            ' && !".tekton/foo-pull-request.yaml".pathChanged()'
        )
        assert normalise_cel(pr_branch) == normalise_cel(from_main)
