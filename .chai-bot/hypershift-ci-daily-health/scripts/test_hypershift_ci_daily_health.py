"""Offline tests for hypershift-ci-daily-health.py (dashboard-driven, two-axis)."""

from __future__ import annotations

import copy
import datetime as dt
import importlib.util
import json
from pathlib import Path

import pytest

SCRIPT = Path(__file__).with_name("hypershift-ci-daily-health.py")
FIXTURES = Path(__file__).parent / "testdata" / "hypershift_ci_daily_health"
SPEC = importlib.util.spec_from_file_location("hypershift_ci_daily_health", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)
SOURCE_REVISION = MODULE.checked_out_source_revision()
UTC = dt.timezone.utc
AS_OF = MODULE.parse_rfc3339("2026-09-28T12:00:00Z")

FAIL = [1, 0, 1, 0]
PASS = [1, 1, 0, 0]


def collect_fixture():
    collector = MODULE.Collector(
        MODULE.FixtureTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    )
    return collector.collect()


def slots(count, step_hours=6):
    """`count` slot labels ('YYYY-MM-DD HH:MM'), ascending, ending at AS_OF."""
    return [
        (AS_OF - dt.timedelta(hours=step_hours * (count - 1 - i))).strftime(
            "%Y-%m-%d %H:%M"
        )
        for i in range(count)
    ]


def prun(state, hours_ago, sha="a" * 40):
    return {
        "state": state,
        "completed": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=hours_ago)),
        "head_sha": sha,
    }


# ---------------------------------------------------------------------------
# Axis A — periodic binary permafail (classify_periodic_sparkline)
# ---------------------------------------------------------------------------
def test_periodic_permafail_matches_author_examples():
    cp = MODULE.classify_periodic_sparkline
    # A recent pass then a couple of fails -> "not yet clear" -> not permafailing.
    r = cp([PASS, FAIL, FAIL], slots(3), AS_OF)
    assert r["classification"] == "not_permafailing" and r["streak_runs"] == 2
    # An older pass followed by enough failures spanning >= 2 days -> permafailing.
    spark = [PASS] + [FAIL] * 5  # slots(6, 12h): the oldest failure is 48h old
    r = cp(spark, slots(6, step_hours=12), AS_OF)
    assert r["classification"] == "permafailing"
    assert r["streak_runs"] == 5 and r["span_hours"] >= 48
    # Failures are counted in RUN terms, not slot terms (multi-run buckets).
    multi = [PASS, [2, 0, 2, 0], [2, 0, 2, 0], [2, 0, 2, 0]]
    r = cp(multi, slots(4, step_hours=24), AS_OF)
    assert r["classification"] == "permafailing" and r["streak_runs"] == 6


def test_periodic_recent_pass_is_not_permafailing():
    # Many old failures, but the newest slot passed -> streak broken -> not permafailing.
    r = MODULE.classify_periodic_sparkline([FAIL] * 11 + [PASS], slots(12), AS_OF)
    assert r["classification"] == "not_permafailing" and r["streak_runs"] == 0


def test_periodic_permafail_fails_safe_to_unknown():
    cp = MODULE.classify_periodic_sparkline
    assert cp([], [], AS_OF)["classification"] == "unknown"  # empty
    assert cp(None, slots(2), AS_OF)["classification"] == "unknown"  # missing
    assert cp([FAIL, FAIL], slots(1), AS_OF)["classification"] == "unknown"  # length mismatch
    assert cp([None, None], slots(2), AS_OF)["classification"] == "unknown"  # only gaps
    assert cp([FAIL], ["not-a-timestamp"], AS_OF)["classification"] == "unknown"  # bad stamp


# ---------------------------------------------------------------------------
# Axis A — presubmit streak (classify_presubmit_streak)
# ---------------------------------------------------------------------------
def test_presubmit_streak_ladder():
    cs = MODULE.classify_presubmit_streak
    # no testable runs -> unknown
    assert cs([prun("ERROR", 1), prun("ABORTED", 2)], AS_OF)["classification"] == "unknown"
    # r <= S -> not_flagged
    assert (
        cs([prun("FAILURE", i + 1) for i in range(3)], AS_OF)["classification"]
        == "not_flagged"
    )
    # S < r < V -> candidate
    c = cs([prun("FAILURE", i + 1) for i in range(5)], AS_OF)
    assert c["classification"] == "candidate" and c["streak"] == 5
    # r >= V and span >= D -> permafailing (60 reds spanning ~50h)
    reds = [prun("FAILURE", 1 + 49 * i / 59, sha=("a" if i % 2 else "b") * 40) for i in range(60)]
    p = cs(reds, AS_OF)
    assert p["classification"] == "permafailing" and p["streak"] == 60 and p["span_hours"] >= 48
    # r >= V but span < D -> candidate (volume alone is not enough)
    tight = [prun("FAILURE", 1 + 40 * i / 59) for i in range(60)]
    assert cs(tight, AS_OF)["classification"] == "candidate"


def test_presubmit_error_aborted_are_neutral():
    # FAILURE, ERROR (skipped), FAILURE, SUCCESS -> streak 2 -> not_flagged.
    runs = [prun("FAILURE", 1), prun("ERROR", 2), prun("FAILURE", 3), prun("SUCCESS", 4)]
    r = MODULE.classify_presubmit_streak(runs, AS_OF)
    assert r["classification"] == "not_flagged" and r["streak"] == 2 and r["infra_aborts"] == 1


# ---------------------------------------------------------------------------
# Axis B — Wilson, week-over-week trend, SLO
# ---------------------------------------------------------------------------
def test_wilson_interval():
    lo, hi = MODULE.wilson_interval(5, 10)
    assert round(lo, 4) == 0.2366 and round(hi, 4) == 0.7634
    assert MODULE.wilson_interval(0, 0) == (0.0, 1.0)  # no data
    # successes clamped to total (guards a float rounding overshoot)
    lo, hi = MODULE.wilson_interval(9, 8)
    assert 0.0 <= lo <= hi <= 1.0


def test_wow_trend():
    ct = MODULE.classify_wow_trend
    assert ct(90.0, 50.0, 4, 4)["classification"] == "insufficient"  # runs < N_min
    assert ct(85.0, 80.0, 500, 500)["classification"] == "stable"  # real but < threshold
    assert ct(90.0, 50.0, 200, 200)["classification"] == "improving"
    assert ct(40.0, 80.0, 200, 200)["classification"] == "degrading"
    assert ct(None, 80.0, 200, 200)["classification"] == "insufficient"


def test_job_below_slo():
    assert MODULE.job_below_slo(79.9) is True
    assert MODULE.job_below_slo(80.0) is False
    assert MODULE.job_below_slo(None) is False
    assert MODULE.job_below_slo(True) is False  # bool is not a rate


# ---------------------------------------------------------------------------
# Integration — collect() against the four-section dashboard fixture
# ---------------------------------------------------------------------------
def test_collect_scope_comes_from_the_envelope_releases():
    scope = collect_fixture()["scope"]
    # last supported_predecessors+1 of the envelope releases, newest first; 4.19 trimmed.
    assert scope["releases"] == ["5.1", "5.0", "4.22", "4.21", "4.20"]
    assert scope["dev_release"] == "5.1"
    assert scope["branches"] == [
        "main",
        "release-5.0",
        "release-4.22",
        "release-4.21",
        "release-4.20",
    ]


def test_blockers_enumerated_from_both_sections_with_gates():
    doc = collect_fixture()
    per = {p["name"]: p for p in doc["periodics"]}
    assert set(per) == {
        "e2e-perma-aws",
        "e2e-ok-aws",
        "e2e-both",
        "e2e-nulls",
        "e2e-cr-perma",
        "e2e-cr-ok",
    }
    assert "e2e-old" not in per  # out-of-scope release (4.19) excluded
    assert per["e2e-perma-aws"]["gate"] == "release-payload"
    assert per["e2e-cr-perma"]["gate"] == "component-readiness"
    assert per["e2e-both"]["gate"] == "both"  # deduped across the two sections


def test_permafailing_payload_and_component_readiness_both_drive_incident():
    doc = collect_fixture()
    blockers = {(b["name"], b["gate"]) for b in doc["incident_set"]["release_blockers"]}
    assert ("e2e-perma-aws", "release-payload") in blockers
    assert ("e2e-cr-perma", "component-readiness") in blockers  # CR permafail is a blocker
    assert len(doc["incident_set"]["release_blockers"]) == 2
    # A periodic with a recent pass is not a blocker even if it dips below SLO.
    assert {p["name"]: p["permafail"]["class"] for p in doc["periodics"]}["e2e-both"] == (
        "not_permafailing"
    )


def test_null_periodic_is_unknown_and_marks_scope_unknown():
    doc = collect_fixture()
    nulls = next(p for p in doc["periodics"] if p["name"] == "e2e-nulls")
    assert nulls["permafail"]["class"] == "unknown"  # never silently green
    assert nulls not in doc["incident_set"]["release_blockers"]
    assert doc["scope"]["state"] == "unknown"  # a coverage gap fails the scope closed


def test_below_slo_excludes_no_data_and_null_rate_jobs():
    doc = collect_fixture()
    below = {j["name"] for j in doc["job_health_below_slo"]}
    assert {"e2e-perma-aws", "e2e-cr-perma", "e2e-both", "e2e-streak"} <= below
    assert "e2e-ok-aws" not in below and "e2e-cr-ok" not in below  # meeting SLO
    assert "e2e-nulls" not in below  # null rate is unknown, not below
    assert "e2e-veto" not in below  # release-branch presubmit with no dashboard data


def test_flaky_tests_come_from_alerts():
    flaky = collect_fixture()["flaky_tests"]
    assert len(flaky) == 1 and "Teardown" in flaky[0]["test_name"]


def test_payload_phase_comes_from_release_controller():
    per = {p["name"]: p["payload"] for p in collect_fixture()["periodics"]}
    # The newest tag's phase, fetched per stream from the release controller.
    assert per["e2e-perma-aws"]["phase"] == "Rejected"
    assert per["e2e-perma-aws"]["tag"] == "5.1.0-0.ci-2026-09-28-090000"
    assert per["e2e-perma-aws"]["release_status_url"].endswith("/releasestream/5.1.0-0.ci")
    assert per["e2e-ok-aws"]["phase"] == "Accepted"
    # A periodic with no payload participation reports phase unknown, never fabricated.
    assert per["e2e-both"]["phase"] == "unknown"
    assert per["e2e-cr-perma"]["phase"] == "unknown"


class _NoPhaseTransport(MODULE.FixtureTransport):
    def get_json(self, url):
        if "releasestream" in url and url.rstrip("/").endswith("/tags"):
            raise MODULE.ReportError("release controller unavailable")
        return super().get_json(url)


def test_payload_phase_failure_is_silent_unknown_not_a_scope_gap():
    doc = MODULE.Collector(
        _NoPhaseTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    ).collect()
    per = {p["name"]: p["payload"] for p in doc["periodics"]}
    assert per["e2e-perma-aws"]["phase"] == "unknown"  # gap -> unknown, not a crash
    # Phase is context only: a phase gap must not add a scope-flipping coverage note.
    assert not any(
        "phase" in note.lower() or "release controller" in note.lower()
        for note in doc["scope"]["coverage_uncertainties"]
    )
    MODULE.validate_candidates_document(doc)


def test_presubmit_candidate_is_flagged_for_llm():
    doc = collect_fixture()
    names = {c["job_id"].rsplit("-", 1)[-1] for c in doc["presubmit_candidates"]["main"]}
    assert "streak" in names  # e2e-streak (5 reds/5h) is a candidate the LLM resolves


# ---------------------------------------------------------------------------
# Config is the single source of truth: a knob change changes an outcome
# ---------------------------------------------------------------------------
def test_periodic_volume_knob_flows_through(monkeypatch):
    monkeypatch.setattr(MODULE, "PERIODIC_PERMAFAIL_VOLUME", 100)
    doc = collect_fixture()
    # With the volume raised past the fixture's streaks, no periodic is permafailing.
    assert doc["incident_set"]["release_blockers"] == []
    assert all(p["permafail"]["class"] != "permafailing" for p in doc["periodics"])


# ---------------------------------------------------------------------------
# Document validation + CLI round-trip + fail-safe
# ---------------------------------------------------------------------------
def test_document_validates_and_rejects_malformed():
    doc = collect_fixture()
    MODULE.validate_candidates_document(doc)  # the emitted document is valid
    bad = copy.deepcopy(doc)
    bad["scope"]["state"] = "green"
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_candidates_document(bad)
    bad = copy.deepcopy(doc)
    if bad["periodics"]:
        bad["periodics"][0]["permafail"]["class"] = "healthy"  # not a valid Axis-A class
        with pytest.raises(MODULE.ReportError):
            MODULE.validate_candidates_document(bad)


def test_collect_and_report_cli_round_trip(tmp_path):
    data = tmp_path / "data.json"
    html = tmp_path / "report.html"
    args = MODULE.build_parser().parse_args(
        [
            "collect",
            "--as-of",
            "2026-09-28T12:00:00Z",
            "--source-revision",
            SOURCE_REVISION,
            "--data-out",
            str(data),
            "--html-out",
            str(html),
            "--fixture-dir",
            str(FIXTURES),
        ]
    )
    assert MODULE.collect_command(args) == 0
    doc = json.loads(data.read_text())
    assert doc["scope"]["releases"][0] == "5.1"
    assert "</html>" in html.read_text().lower()
    # report renders HTML from the previously written data document (via load_json).
    report_html = tmp_path / "report2.html"
    rargs = MODULE.build_parser().parse_args(
        ["report", "--data", str(data), "--html-out", str(report_html)]
    )
    assert MODULE.report_command(rargs) == 0
    assert "</html>" in report_html.read_text().lower()


class _NoHealthTransport(MODULE.FixtureTransport):
    def get_json(self, url):
        if "windows/1w" in url:
            raise MODULE.ReportError("dashboard health unavailable")
        return super().get_json(url)


def test_dashboard_failure_fails_closed_to_unknown():
    collector = MODULE.Collector(
        _NoHealthTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    )
    doc = collector.collect()
    assert doc["scope"]["state"] == "unknown"
    assert doc["periodics"] == [] and doc["scope"]["source_failures"]
    MODULE.validate_candidates_document(doc)  # the unknown report is still a valid document


# ---------------------------------------------------------------------------
# Regression: the presubmit history window must exceed the permafail duration,
# or a >= D span (and hence the deterministic permafailing class) is unreachable.
# ---------------------------------------------------------------------------
class _WindowTransport:
    """Serves a Prow history whose oldest red completed 50h before AS_OF."""

    def configure_collection_limits(self, deadline_seconds, max_requests):
        pass

    def get_text(self, url):
        job = "pull-ci-openshift-hypershift-main-e2e-window"
        builds = [
            {
                "SpyglassLink": f"/view/gs/test-platform-results/pr-logs/pull/openshift_hypershift/1/{job}/{i}",
                "ID": str(i),
                "Started": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=hours)),
                "Duration": 600000000000,
                "Result": "FAILURE",
                "Refs": {"base_ref": "main", "pulls": [{"sha": "a" * 40}]},
            }
            for i, hours in ((310, 1), (309, 2), (308, 3), (307, 50))
        ]
        return "<!doctype html><script>var allBuilds = " + json.dumps(builds) + ";</script>"

    def get_json(self, url):
        return {
            "status": {
                "startTime": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=2)),
                "completionTime": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=1)),
                "state": "failure",
            },
            "spec": {"refs": {"pulls": [{"sha": "a" * 40}]}},
        }


def test_presubmit_history_window_exceeds_permafail_duration():
    collector = MODULE.Collector(_WindowTransport(), AS_OF, source_revision=SOURCE_REVISION)
    evidence = collector._presubmit_history(
        {
            "id": "pull-ci-openshift-hypershift-main-e2e-window",
            "prow_job_history_url": (
                "https://prow.ci.openshift.org/job-history/gs/test-platform-results/"
                "pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-window"
            ),
        }
    )
    # The 50h-old red must be retained (the window is not clipped at D=48h), so the
    # observed span exceeds the permafail duration.
    assert evidence["classification"]["span_hours"] >= 49


# ---------------------------------------------------------------------------
# Rich HTML report + chaibot annotations
# ---------------------------------------------------------------------------
def test_html_report_renders_rich_structure_and_annotations():
    doc = collect_fixture()
    annotations = MODULE.validate_annotations(
        {
            "summary": "Two release blockers today.\nAKS capacity is the dominant signature.",
            "incident": "Proposing one jobs-incident for the two permafailing blockers.",
            "job_notes": {
                "periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-perma-aws": (
                    "Suspected AKS capacity/lease trouble."
                )
            },
        }
    )
    report = MODULE.render_html_report(doc, annotations)
    for needle in (
        "<section>",
        'class="pill red">permafailing',
        'class="bar"',
        "<details><summary>Trend charts",
        "<svg ",
        "OCP 5.1 — release payloads",
        "Merge queue — required presubmits",
        "</html>",
    ):
        assert needle in report, needle
    # chaibot's free-form content is rendered, and its permafailing blocker drives the
    # incident section (never withheld for a missing Jira story).
    assert "Two release blockers today." in report
    assert "Suspected AKS capacity/lease trouble." in report
    assert "Proposed incident" in report and "e2e-perma-aws" in report


def test_html_report_escapes_annotation_html():
    report = MODULE.render_html_report(
        collect_fixture(),
        MODULE.validate_annotations({"summary": "<script>alert(1)</script>"}),
    )
    assert "<script>alert(1)</script>" not in report
    assert "&lt;script&gt;" in report


def test_html_report_collapses_dataless_presubmits():
    # The dashboard emits a row for every planned presubmit, but release-branch presubmits come
    # back with no Sippy rate/trend and no Prow runs. Those empty stubs must be collapsed into a
    # per-branch count, not tabled, so the actionable rows are not drowned out.
    doc = {
        "generated_at": "2026-09-30T12:00:00Z",
        "source_revision": "a" * 40,
        "collection_id": "test",
        "scope": {
            "state": "complete",
            "releases": ["5.1"],
            "branches": ["main", "release-4.20"],
        },
        "periodics": [],
        "presubmits": {
            "main": [
                {
                    "job_id": "pull-ci-openshift-hypershift-main-e2e-live",
                    "name": "e2e-live",
                    "permafail": {"class": "candidate", "streak": 5},
                    "slo": {"rate": 42.0, "below_slo": True},
                }
            ],
            "release-4.20": [
                {
                    "job_id": "pull-ci-openshift-hypershift-release-4.20-e2e-empty",
                    "name": "e2e-empty",
                    "permafail": {"class": "unknown", "streak": None},
                    "slo": {"rate": None, "below_slo": False},
                }
            ],
        },
        "incident_set": {"release_blockers": [], "merge_queue_blockers": []},
        "flaky_tests": [],
    }
    report = MODULE.render_html_report(doc, None)
    assert "Merge queue — required presubmits" in report
    # The job with signal is tabled.
    assert "<td><code>e2e-live</code>" in report
    assert 'class="pill amber">candidate' in report
    # The dataless release-branch job is collapsed, never tabled.
    assert "1 presubmit(s) with no window data" in report
    assert "<td><code>e2e-empty</code>" not in report
    assert "<code>e2e-empty</code>" in report


def test_validate_annotations_bounds_and_rejects():
    assert MODULE.validate_annotations(None) == {}
    ok = MODULE.validate_annotations(
        {"summary": "line1\nline2", "job_notes": {"job-a": "note"}}
    )
    assert ok["summary"] == "line1\nline2" and ok["job_notes"]["job-a"] == "note"
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_annotations("not a dict")
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_annotations({"summary": "bad\x07bell"})  # control character
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_annotations({"summary": "x" * 5000})  # exceeds the length bound
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_annotations({"job_notes": ["not", "a", "dict"]})
