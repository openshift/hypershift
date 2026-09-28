"""Offline tests for hypershift-ci-daily-health.py."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPT = Path(__file__).with_name("hypershift-ci-daily-health.py")
FIXTURES = Path(__file__).parent / "testdata" / "hypershift_ci_daily_health"
SPEC = importlib.util.spec_from_file_location("hypershift_ci_daily_health", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


def as_of():
    return MODULE.parse_rfc3339("2026-09-28T12:00:00Z")


def collect_fixture():
    collector = MODULE.Collector(MODULE.FixtureTransport(FIXTURES), as_of())
    return collector.collect()


class FailingHealthTransport(MODULE.FixtureTransport):
    def get_json(self, url):
        if url.endswith("/_dashboard/health/windows/1w"):
            raise MODULE.ReportError("simulated dashboard outage")
        return super().get_json(url)


class NoLatestTransport(MODULE.FixtureTransport):
    def get_json(self, url):
        if url.endswith("/latest"):
            raise AssertionError("the latest Accepted endpoint must not be used")
        return super().get_json(url)


class FailingSupportedStreamTransport(NoLatestTransport):
    def get_json(self, url):
        data = super().get_json(url)
        if url == f"{MODULE.RELEASE_CONTROLLER_BASE}/api/v1/releasestreams/all":
            data = dict(data)
            data.pop("4.22.0-0.ci")
        return data


def valid_judgment(candidate):
    return {
        "candidate_id": candidate["candidate_id"],
        "classification": (
            "permafail_candidate"
            if candidate["kind"] == "presubmit"
            else "incident_candidate"
        ),
        "summary": "Repeated <failure> evidence was reviewed.",
        "signature": "bounded & verified signature",
        "recurring_evidence": ["same signature in two independent runs"],
        "next_action": "A human should verify the affected gate before taking action.",
        "tracking": {"status": "gap"},
    }


def test_collect_uses_active_payload_and_excludes_future_stream():
    collector = MODULE.Collector(NoLatestTransport(FIXTURES), as_of())
    stage_one, document = collector.collect()

    assert document["scope"]["releases"] == ["5.1", "5.0", "4.23", "4.22", "4.21"]
    assert "5.2" not in document["scope"]["releases"]
    assert "5.1.0-0.ci-2026-09-28-100000 Ready" in stage_one
    assert "5.1.0-0.ci-2026-09-24-111734" not in stage_one
    assert "*main → OCP 5.1*" in stage_one
    assert len(stage_one) < 2000


def test_collect_fails_closed_when_primary_source_is_partial():
    collector = MODULE.Collector(FailingHealthTransport(FIXTURES), as_of())
    stage_one, document = collector.collect()

    assert document["scope"]["state"] == "unknown"
    assert document["candidates"] == []
    assert "Overall*: Unknown" in stage_one
    assert "No release-gate or merge-gate conclusion" in stage_one


def test_collect_fails_closed_when_supported_stream_validation_is_partial():
    collector = MODULE.Collector(FailingSupportedStreamTransport(FIXTURES), as_of())
    stage_one, document = collector.collect()

    assert document["scope"]["state"] == "unknown"
    assert document["candidates"] == []
    assert "required public data could not be validated" in stage_one
    assert "No release-gate or merge-gate conclusion" in stage_one


def test_collect_joins_health_by_exact_identity_and_emits_bounded_schema():
    _, document = collect_fixture()

    assert document["schema_version"] == 1
    assert document["generated_at"] == "2026-09-28T12:00:00Z"
    assert len(document["candidates"]) == 3
    periodic = next(
        item for item in document["candidates"] if item["kind"] == "periodic"
    )
    assert periodic["dashboard_1w"]["rate"] == 75
    assert periodic["live_payload"]["impact"] == "verified_blocker"
    assert periodic["deterministic_trigger"] == "live_payload_blocking_verification"
    assert "classification" not in periodic
    assert len(periodic["runs"]) <= MODULE.MAX_SAMPLE_RUNS


def test_periodic_trend_uses_exact_boundaries_and_exact_job_filtering():
    _, document = collect_fixture()
    periodic = next(
        item for item in document["candidates"] if item["kind"] == "periodic"
    )
    trend = periodic["trend"]

    assert trend["current"]["SUCCESS"] == 2
    assert trend["current"]["FAILURE"] == 1
    assert trend["current"]["ERROR"] == 1
    assert trend["current"]["denominator"] == 3
    assert trend["baseline"]["SUCCESS"] == 2
    assert trend["baseline"]["FAILURE"] == 1
    assert trend["classification"] == "low_confidence"
    assert all(not run["url"].endswith("/108") for run in periodic["runs"])


def test_presubmit_uses_prow_when_sippy_disabled_and_applies_success_veto():
    _, document = collect_fixture()
    by_job = {item["job_id"]: item for item in document["candidates"]}

    assert "pull-ci-openshift-hypershift-main-e2e-streak" in by_job
    streak = by_job["pull-ci-openshift-hypershift-main-e2e-streak"]
    assert streak["deterministic_trigger"] == "repeated_failure_review"
    assert len({run["head_sha"] for run in streak["runs"][:3]}) == 2
    assert "pull-ci-openshift-hypershift-release-5.0-e2e-veto" not in by_job
    assert (
        by_job["pull-ci-openshift-hypershift-release-4.22-e2e-error"][
            "deterministic_trigger"
        ]
        == "infrastructure_state_review"
    )


def test_repeated_failures_on_one_head_are_not_selected():
    collector = MODULE.Collector(MODULE.FixtureTransport(FIXTURES), as_of())
    job = {
        "id": "single-head",
        "prow_job_history_url": (
            "https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/"
            "pull-ci-openshift-hypershift-main-e2e-streak"
        ),
    }
    evidence = collector._presubmit_history(job)
    for run in evidence["runs"]:
        run["head_sha"] = "same"

    trigger, status = MODULE.select_presubmit_trigger(evidence["runs"])
    assert trigger is None
    assert status == "recent_results"


def test_render_requires_exact_candidate_ids_and_escapes_judgment_text():
    stage_one, candidates = collect_fixture()
    judgments = {
        "schema_version": 1,
        "judgments": [
            valid_judgment(candidate) for candidate in candidates["candidates"]
        ],
    }

    rendered = MODULE.render_report(stage_one, candidates, judgments)

    assert "---THREAD_DETAILS---" in rendered
    assert "---THREAD_BREAK---" in rendered
    assert "Repeated &lt;failure&gt; evidence" in rendered
    assert "bounded &amp; verified signature" in rendered
    replies = rendered.split("---THREAD_DETAILS---", 1)[1].split("---THREAD_BREAK---")
    assert all(len(reply.strip()) <= MODULE.THREAD_REPLY_LIMIT for reply in replies)

    judgments["judgments"].pop()
    with pytest.raises(MODULE.ReportError, match="do not match candidates"):
        MODULE.render_report(stage_one, candidates, judgments)


def test_render_rejects_bad_candidates_schema():
    with pytest.raises(MODULE.ReportError, match="candidates schema_version"):
        MODULE.render_report(
            "stage one",
            {"schema_version": 2, "candidates": []},
            {
                "schema_version": 1,
                "judgments": [],
            },
        )


@pytest.mark.parametrize(
    "document,error",
    [
        ({}, "schema_version"),
        ({"schema_version": 1, "judgments": "bad"}, "must be a list"),
        (
            {
                "schema_version": 1,
                "judgments": [
                    {
                        "candidate_id": "unexpected",
                        "classification": "invented",
                        "summary": "x",
                        "signature": "x",
                        "recurring_evidence": [],
                        "next_action": "x",
                        "tracking": {"status": "none"},
                    }
                ],
            },
            "unsupported classification",
        ),
    ],
)
def test_render_rejects_bad_judgment_json(document, error):
    stage_one, candidates = collect_fixture()
    with pytest.raises(MODULE.ReportError, match=error):
        MODULE.render_report(stage_one, candidates, document)


def test_existing_tracking_requires_verified_supported_project_key():
    stage_one, candidates = collect_fixture()
    judgments = {
        "schema_version": 1,
        "judgments": [
            valid_judgment(candidate) for candidate in candidates["candidates"]
        ],
    }
    judgments["judgments"][0]["tracking"] = {
        "status": "existing",
        "verified": True,
        "key": "OTHER-1",
    }
    with pytest.raises(MODULE.ReportError, match="verified OCPBUGS/CNTRLPLANE"):
        MODULE.render_report(stage_one, candidates, judgments)


def test_public_url_allowlist_rejects_private_and_unknown_buckets():
    with pytest.raises(MODULE.ReportError, match="unknown host"):
        MODULE.validate_public_url("https://private.invalid/data")
    with pytest.raises(MODULE.ReportError, match="non-public Prow results bucket"):
        MODULE.validate_public_url(
            "https://storage.googleapis.com/test-platform-results/path"
        )
    assert MODULE.public_prowjob_url(
        "https://prow.ci.openshift.org/view/gs/test-platform-results/pr-logs/job/1"
    ) == (
        "https://storage.googleapis.com/test-platform-results-public/pr-logs/job/1/prowjob.json"
    )


def test_cli_offline_collect_and_render(tmp_path):
    stage_one = tmp_path / "stage-one.txt"
    candidates = tmp_path / "candidates.json"
    report = tmp_path / "report.txt"
    collect = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "collect",
            "--as-of",
            "2026-09-28T12:00:00Z",
            "--slack-out",
            str(stage_one),
            "--candidates-out",
            str(candidates),
            "--fixture-dir",
            str(FIXTURES),
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    assert collect.returncode == 0, collect.stderr
    candidate_data = json.loads(candidates.read_text())
    judgments = tmp_path / "judgments.json"
    judgments.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "judgments": [
                    valid_judgment(candidate)
                    for candidate in candidate_data["candidates"]
                ],
            }
        )
    )
    render = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "render",
            "--stage-one",
            str(stage_one),
            "--candidates",
            str(candidates),
            "--judgments",
            str(judgments),
            "--slack-out",
            str(report),
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    assert render.returncode == 0, render.stderr
    assert report.read_text().startswith("*HyperShift CI Daily Health Report*")
    assert "---THREAD_DETAILS---" in report.read_text()


def test_cli_rejects_malformed_judgments_json(tmp_path):
    stage_one = tmp_path / "stage-one.txt"
    candidates = tmp_path / "candidates.json"
    judgments = tmp_path / "judgments.json"
    report = tmp_path / "report.txt"
    stage_one.write_text("stage one")
    candidates.write_text('{"schema_version": 1, "candidates": []}')
    judgments.write_text("{not-json")

    result = subprocess.run(
        [
            sys.executable,
            str(SCRIPT),
            "render",
            "--stage-one",
            str(stage_one),
            "--candidates",
            str(candidates),
            "--judgments",
            str(judgments),
            "--slack-out",
            str(report),
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 2
    assert "invalid JSON in judgments" in result.stderr
    assert not report.exists()


def test_cli_help_documents_both_stages():
    result = subprocess.run(
        [sys.executable, str(SCRIPT), "--help"],
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0
    assert "collect" in result.stdout
    assert "render" in result.stdout
