"""Tests for verify-tekton-pipeline-pairs.py — covers normalise_cel(),
check_pair(), find_pairs(), and orphan-detection functions."""
import importlib.util
import os
import textwrap

import pytest
import yaml

# Import the verify script as a module (filename contains hyphens).
_script = os.path.join(os.path.dirname(__file__), "verify-tekton-pipeline-pairs.py")
_spec = importlib.util.spec_from_file_location("verify_tekton", _script)
_mod = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_mod)
normalise_cel = _mod.normalise_cel
check_pair = _mod.check_pair
find_pairs = _mod.find_pairs
find_orphaned_from_main = _mod.find_orphaned_from_main
find_orphaned_pull_requests = _mod.find_orphaned_pull_requests


# ---------------------------------------------------------------------------
# Helpers for building minimal PipelineRun YAML fixtures
# ---------------------------------------------------------------------------

_COMMON_ANNOTATIONS = {
    "build.appstudio.openshift.io/repo":
        "https://github.com/openshift/hypershift?rev={{revision}}",
    "build.appstudio.redhat.com/commit_sha": "{{revision}}",
    "build.appstudio.redhat.com/pull_request_number": "{{pull_request_number}}",
    "build.appstudio.redhat.com/target_branch": "{{target_branch}}",
    "pipelinesascode.tekton.dev/cancel-in-progress": "true",
    "pipelinesascode.tekton.dev/max-keep-runs": "3",
}

_COMMON_LABELS = {
    "appstudio.openshift.io/application": "test-app",
    "appstudio.openshift.io/component": "test-component",
    "pipelines.appstudio.openshift.io/type": "build",
}

_COMMON_SPEC_PARAMS = [
    {"name": "git-url", "value": "{{source_url}}"},
    {"name": "revision", "value": "{{revision}}"},
    {"name": "output-image",
     "value": "quay.io/redhat-user-workloads/tenant/comp:on-pr-{{revision}}"},
    {"name": "image-expires-after", "value": "5d"},
    {"name": "dockerfile", "value": "/Dockerfile"},
]


def _make_pr_doc(component_name):
    """Build a minimal valid PR-branch PipelineRun document."""
    pr_file = f".tekton/{component_name}-pull-request.yaml"
    cel = textwrap.dedent(f"""\
        event == "pull_request"
        && target_branch == "main"
        && (".tekton/pipelines/common-operator-build.yaml".pathChanged()
           || "{pr_file}".pathChanged())""")
    annotations = dict(_COMMON_ANNOTATIONS)
    annotations["pipelinesascode.tekton.dev/on-cel-expression"] = cel
    annotations["pipelinesascode.tekton.dev/pipeline"] = \
        ".tekton/pipelines/common-operator-build.yaml"
    return {
        "apiVersion": "tekton.dev/v1",
        "kind": "PipelineRun",
        "metadata": {
            "annotations": annotations,
            "labels": dict(_COMMON_LABELS),
            "name": f"{component_name}-on-pull-request",
            "namespace": "crt-redhat-acm-tenant",
        },
        "spec": {
            "params": list(_COMMON_SPEC_PARAMS),
            "pipelineRef": {"name": "hypershift-common-operator-build"},
            "taskRunTemplate": {
                "serviceAccountName": f"build-pipeline-{component_name}",
            },
            "workspaces": [
                {"name": "git-auth",
                 "secret": {"secretName": "{{ git_auth_secret }}"}},
            ],
        },
    }


def _make_fm_doc(component_name):
    """Build a minimal valid from-main PipelineRun document."""
    pr_file = f".tekton/{component_name}-pull-request.yaml"
    cel = textwrap.dedent(f"""\
        event == "pull_request"
        && target_branch == "main"
        && ("src/***".pathChanged()
           || ".tekton/{component_name}-pull-request-from-main.yaml".pathChanged())
        && !".tekton/pipelines/common-operator-build.yaml".pathChanged()
        && !"{pr_file}".pathChanged()""")
    annotations = dict(_COMMON_ANNOTATIONS)
    annotations["pipelinesascode.tekton.dev/on-cel-expression"] = cel
    return {
        "apiVersion": "tekton.dev/v1",
        "kind": "PipelineRun",
        "metadata": {
            "annotations": annotations,
            "labels": dict(_COMMON_LABELS),
            "name": f"{component_name}-on-pr-from-main",
            "namespace": "crt-redhat-acm-tenant",
        },
        "spec": {
            "params": list(_COMMON_SPEC_PARAMS),
            "pipelineRef": {
                "resolver": "git",
                "params": [
                    {"name": "url",
                     "value": "https://github.com/openshift/hypershift.git"},
                    {"name": "revision", "value": "main"},
                    {"name": "pathInRepo",
                     "value": ".tekton/pipelines/common-operator-build.yaml"},
                ],
            },
            "taskRunTemplate": {
                "serviceAccountName": f"build-pipeline-{component_name}",
            },
            "workspaces": [
                {"name": "git-auth",
                 "secret": {"secretName": "{{ git_auth_secret }}"}},
            ],
        },
    }


def _write_yaml(directory, filename, doc):
    """Write a YAML document to a file in the given directory."""
    path = os.path.join(directory, filename)
    with open(path, "w") as f:
        yaml.dump(doc, f, default_flow_style=False)
    return path


# ---------------------------------------------------------------------------
# TestNormaliseCel — preserved original coverage plus new patterns
# ---------------------------------------------------------------------------

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

    def test_strips_general_source_trigger_block(self):
        """From-main CELs with non-.tekton source paths are also stripped."""
        cel = (
            'event == "pull_request" && target_branch == "main"'
            ' && ("src/***".pathChanged()'
            ' || "Dockerfile".pathChanged())'
            ' && !".tekton/pipelines/common-operator-build.yaml".pathChanged()'
        )
        result = normalise_cel(cel)
        assert 'pathChanged' not in result
        assert 'event == "pull_request"' in result
        assert 'target_branch == "main"' in result

    def test_strips_files_all_exists_block(self):
        """Complex blocks with files.all.exists() are also stripped."""
        cel = (
            'event == "pull_request" && target_branch == "main"'
            ' && (files.all.exists(x, !x.matches(\'^docs/\'))'
            ' || "Containerfile".pathChanged())'
            ' && !".tekton/pipelines/common-operator-build.yaml".pathChanged()'
        )
        result = normalise_cel(cel)
        assert 'files.all.exists' not in result
        assert 'pathChanged' not in result
        assert 'event == "pull_request"' in result

    def test_pr_and_from_main_with_source_triggers_same_base(self):
        """A realistic pair normalises to the same base expression."""
        pr_branch = (
            'event == "pull_request"\n'
            '&& target_branch == "main"\n'
            '&& (".tekton/pipelines/common-operator-build.yaml".pathChanged()\n'
            '   || ".tekton/comp-pull-request.yaml".pathChanged())'
        )
        from_main = (
            'event == "pull_request"\n'
            '&& target_branch == "main"\n'
            '&& ("src/***".pathChanged()\n'
            '   || ".tekton/comp-pull-request-from-main.yaml".pathChanged())\n'
            '&& !".tekton/pipelines/common-operator-build.yaml".pathChanged()\n'
            '&& !".tekton/comp-pull-request.yaml".pathChanged()'
        )
        assert normalise_cel(pr_branch) == normalise_cel(from_main)


# ---------------------------------------------------------------------------
# TestCheckPair — exercises the cross-pair validation logic
# ---------------------------------------------------------------------------

class TestCheckPair:
    """Unit tests for check_pair()."""

    def test_valid_pair_no_errors(self, tmp_path):
        """A correctly structured pair produces zero errors."""
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              _make_fm_doc("comp"))
        errors = check_pair(pr_path, fm_path)
        assert errors == [], errors

    def test_missing_pipeline_annotation_on_pr(self, tmp_path):
        """PR-branch file without the pipeline annotation is an error."""
        doc = _make_pr_doc("comp")
        del doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/pipeline"]
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml", doc)
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              _make_fm_doc("comp"))
        errors = check_pair(pr_path, fm_path)
        assert any("missing" in e and "pipeline" in e for e in errors)

    def test_pipeline_annotation_on_from_main_is_error(self, tmp_path):
        """From-main file with the pipeline annotation is an error."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/pipeline"] = \
            ".tekton/pipelines/common-operator-build.yaml"
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("must NOT have" in e for e in errors)

    def test_missing_cel_common_pipeline_guard(self, tmp_path):
        """PR-branch CEL without common pipeline guard is an error."""
        doc = _make_pr_doc("comp")
        doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/on-cel-expression"] = (
            'event == "pull_request" && target_branch == "main"'
            ' && (".tekton/comp-pull-request.yaml".pathChanged())'
        )
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml", doc)
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              _make_fm_doc("comp"))
        errors = check_pair(pr_path, fm_path)
        assert any("CEL missing common pipeline guard" in e for e in errors)

    def test_missing_negated_guard_on_from_main(self, tmp_path):
        """From-main CEL without negated common pipeline guard is an error."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/on-cel-expression"] = (
            'event == "pull_request" && target_branch == "main"'
            ' && ("src/***".pathChanged())'
        )
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("CEL missing negated" in e for e in errors)

    def test_normalised_cel_mismatch_detected(self, tmp_path):
        """Drift in the base CEL expression (after normalisation) is caught."""
        pr_doc = _make_pr_doc("comp")
        fm_doc = _make_fm_doc("comp")
        # Change the target_branch in the from-main CEL to create drift.
        fm_doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/on-cel-expression"] = (
            'event == "pull_request"\n'
            '&& target_branch == "release-4.18"\n'
            '&& ("src/***".pathChanged())\n'
            '&& !".tekton/pipelines/common-operator-build.yaml".pathChanged()\n'
            '&& !".tekton/comp-pull-request.yaml".pathChanged()'
        )
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml", pr_doc)
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("Normalised CEL mismatch" in e for e in errors)

    def test_label_mismatch_detected(self, tmp_path):
        """Differing labels between pair files are caught."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["metadata"]["labels"]["extra-label"] = "true"
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("Label mismatch" in e for e in errors)

    def test_namespace_mismatch_detected(self, tmp_path):
        """Differing namespace between pair files is caught."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["metadata"]["namespace"] = "other-tenant"
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("Namespace mismatch" in e for e in errors)

    def test_spec_mismatch_detected(self, tmp_path):
        """Differing spec (excluding pipelineRef) is caught."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["spec"]["params"].append(
            {"name": "extra-param", "value": "val"})
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("spec mismatch" in e for e in errors)

    def test_wrong_git_resolver_param(self, tmp_path):
        """Incorrect git resolver param value is caught."""
        fm_doc = _make_fm_doc("comp")
        for p in fm_doc["spec"]["pipelineRef"]["params"]:
            if p["name"] == "revision":
                p["value"] = "release-4.18"
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("pipelineRef param 'revision'" in e for e in errors)

    def test_non_git_resolver_error(self, tmp_path):
        """From-main pipelineRef without 'git' resolver is caught."""
        fm_doc = _make_fm_doc("comp")
        fm_doc["spec"]["pipelineRef"] = {"resolver": "bundles", "params": []}
        pr_path = _write_yaml(tmp_path, "comp-pull-request.yaml",
                              _make_pr_doc("comp"))
        fm_path = _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                              fm_doc)
        errors = check_pair(pr_path, fm_path)
        assert any("resolver must be 'git'" in e for e in errors)


# ---------------------------------------------------------------------------
# TestFindPairs — exercises pair-discovery over a fake .tekton dir
# ---------------------------------------------------------------------------

class TestFindPairs:
    """Unit tests for find_pairs()."""

    def test_finds_valid_pair(self, tmp_path, monkeypatch):
        """A matching PR + from-main file pair is discovered."""
        _write_yaml(tmp_path, "comp-pull-request.yaml", _make_pr_doc("comp"))
        _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                     _make_fm_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        pairs = find_pairs()
        assert len(pairs) == 1
        assert pairs[0][0].endswith("comp-pull-request.yaml")
        assert pairs[0][1].endswith("comp-pull-request-from-main.yaml")

    def test_ignores_pr_without_from_main(self, tmp_path, monkeypatch):
        """A PR file without a from-main counterpart is not returned as a pair."""
        _write_yaml(tmp_path, "comp-pull-request.yaml", _make_pr_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        pairs = find_pairs()
        assert len(pairs) == 0

    def test_multiple_pairs_sorted(self, tmp_path, monkeypatch):
        """Multiple pairs are returned in sorted order."""
        for name in ("alpha", "beta"):
            _write_yaml(tmp_path, f"{name}-pull-request.yaml",
                         _make_pr_doc(name))
            _write_yaml(tmp_path, f"{name}-pull-request-from-main.yaml",
                         _make_fm_doc(name))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        pairs = find_pairs()
        assert len(pairs) == 2
        assert "alpha" in os.path.basename(pairs[0][0])
        assert "beta" in os.path.basename(pairs[1][0])

    def test_ignores_non_pull_request_files(self, tmp_path, monkeypatch):
        """Files not ending in -pull-request.yaml are ignored."""
        _write_yaml(tmp_path, "comp-push.yaml", _make_pr_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        pairs = find_pairs()
        assert len(pairs) == 0


# ---------------------------------------------------------------------------
# TestFindOrphanedFromMain — exercises from-main orphan detection
# ---------------------------------------------------------------------------

class TestFindOrphanedFromMain:
    """Unit tests for find_orphaned_from_main()."""

    def test_no_orphans_when_pair_exists(self, tmp_path, monkeypatch):
        """A from-main file with its PR counterpart is not an orphan."""
        _write_yaml(tmp_path, "comp-pull-request.yaml", _make_pr_doc("comp"))
        _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                     _make_fm_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_from_main()
        assert orphans == []

    def test_detects_orphaned_from_main(self, tmp_path, monkeypatch):
        """A from-main file without its PR counterpart is flagged."""
        _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                     _make_fm_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_from_main()
        assert len(orphans) == 1
        assert "comp-pull-request-from-main.yaml" in orphans[0]

    def test_multiple_orphans_sorted(self, tmp_path, monkeypatch):
        """Multiple orphaned from-main files are returned in sorted order."""
        for name in ("beta", "alpha"):
            _write_yaml(tmp_path, f"{name}-pull-request-from-main.yaml",
                         _make_fm_doc(name))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_from_main()
        assert len(orphans) == 2
        assert "alpha" in orphans[0]
        assert "beta" in orphans[1]


# ---------------------------------------------------------------------------
# TestFindOrphanedPullRequests — exercises reverse orphan detection
# ---------------------------------------------------------------------------

class TestFindOrphanedPullRequests:
    """Unit tests for find_orphaned_pull_requests()."""

    def test_no_orphans_when_pair_exists(self, tmp_path, monkeypatch):
        """A PR file with its from-main counterpart is not an orphan."""
        _write_yaml(tmp_path, "comp-pull-request.yaml", _make_pr_doc("comp"))
        _write_yaml(tmp_path, "comp-pull-request-from-main.yaml",
                     _make_fm_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_pull_requests()
        assert orphans == []

    def test_detects_orphaned_pr_with_pipeline_annotation(
            self, tmp_path, monkeypatch):
        """A PR file with a pipeline annotation but no from-main is flagged."""
        _write_yaml(tmp_path, "comp-pull-request.yaml", _make_pr_doc("comp"))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_pull_requests()
        assert len(orphans) == 1
        assert "comp-pull-request.yaml" in orphans[0]

    def test_ignores_pr_without_pipeline_annotation(
            self, tmp_path, monkeypatch):
        """A PR file without a pipeline annotation is not flagged as orphan."""
        doc = _make_pr_doc("comp")
        del doc["metadata"]["annotations"][
            "pipelinesascode.tekton.dev/pipeline"]
        _write_yaml(tmp_path, "comp-pull-request.yaml", doc)
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_pull_requests()
        assert orphans == []

    def test_multiple_orphans_sorted(self, tmp_path, monkeypatch):
        """Multiple orphaned PR files are returned in sorted order."""
        for name in ("beta", "alpha"):
            _write_yaml(tmp_path, f"{name}-pull-request.yaml",
                         _make_pr_doc(name))
        monkeypatch.setattr(_mod, "TEKTON_DIR", str(tmp_path))
        orphans = find_orphaned_pull_requests()
        assert len(orphans) == 2
        assert "alpha" in orphans[0]
        assert "beta" in orphans[1]
