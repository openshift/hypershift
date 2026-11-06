"""Offline tests for hypershift-ci-daily-health.py (Sippy-driven health window, two-axis)."""

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
# Axis A — presubmit elimination funnel (classify_presubmit_funnel)
# ---------------------------------------------------------------------------
def frun(state, hours_ago, pr, is_probe=False):
    """A presubmit run keyed by PR number (head_sha empty -> pr_identity uses the number)."""
    return {
        "state": state,
        "completed": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=hours_ago)),
        "head_sha": "",
        "pr_number": str(pr),
        "is_probe": is_probe,
    }


def test_funnel_gates():
    cf = MODULE.classify_presubmit_funnel
    # G0: no testable runs -> under-observed.
    assert cf([], AS_OF, volume="low")["classification"] == "under-observed"
    # G0: last run stale (10 days old) -> under-observed.
    assert cf([frun("FAILURE", 240, 1)], AS_OF, volume="low")["gate"] == "G0"
    # G1: a recent pass -> eliminated.
    assert cf([frun("SUCCESS", 1, 1), frun("FAILURE", 5, 2)], AS_OF, volume="low")["gate"] == "G1"
    # G2: one bad PR (all reds share a PR number) -> eliminated, f == 1.
    g2 = cf(
        [frun("FAILURE", 1, 7), frun("FAILURE", 10, 7), frun("FAILURE", 30, 7)],
        AS_OF,
        volume="low",
    )
    assert g2["classification"] == "eliminated" and g2["gate"] == "G2" and g2["f"] == 1
    # G3: >= T distinct failing PRs, span >= D, no recent pass -> permafailing.
    reds = [frun("FAILURE", 1 + 16 * i, i) for i in range(4)]  # 4 PRs, oldest ~49h old
    g3 = cf(reds, AS_OF, volume="low")
    assert g3["classification"] == "permafailing" and g3["gate"] == "G3" and g3["f"] == 4
    # G4 low-throughput: 2 failing PRs, span < D -> candidate.
    g4 = cf([frun("FAILURE", 1, 1), frun("FAILURE", 5, 2)], AS_OF, volume="low")
    assert g4["classification"] == "candidate" and g4["gate"] == "G4" and g4["f"] == 2
    # G4 high-throughput, sparse: few of many PRs failing -> eliminated.
    sparse = [frun("FAILURE", 1 + i, i) for i in range(2)] + [
        frun("SUCCESS", 30 + i, 100 + i) for i in range(10)
    ]
    assert cf(sparse, AS_OF, volume="high")["classification"] == "eliminated"
    # G4 high-throughput, overwhelming share -> candidate (suspicious, not yet sustained).
    overwhelming = [frun("FAILURE", 1 + i, i) for i in range(5)] + [frun("SUCCESS", 40, 200)]
    ov = cf(overwhelming, AS_OF, volume="high")
    assert ov["classification"] == "candidate" and ov["gate"] == "G4"


def test_funnel_pr_number_dedups_commits():
    # Three reds, three different head SHAs but the SAME PR number -> f == 1 -> eliminated (one bad
    # PR), not a branch break. Counting head SHAs alone would inflate f to 3 and misfire.
    cf = MODULE.classify_presubmit_funnel
    runs = [
        {
            "state": "FAILURE",
            "completed": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=h)),
            "head_sha": sha,
            "pr_number": "42",
            "is_probe": False,
        }
        for h, sha in ((1, "a" * 40), (20, "b" * 40), (49, "c" * 40))
    ]
    r = cf(runs, AS_OF, volume="low")
    assert r["f"] == 1 and r["classification"] == "eliminated" and r["gate"] == "G2"


def test_funnel_probe_path():
    # An under-observed job is classified from a FRESH no-op probe run (attribution-clean): the
    # distinct-PR gates are bypassed, but recency and sustained-span still apply.
    cf = MODULE.classify_presubmit_funnel
    assert cf([], AS_OF, volume="low")["classification"] == "under-observed"
    passed = cf([], AS_OF, volume="low", probe_runs=[frun("SUCCESS", 2, 900, is_probe=True)])
    assert passed["classification"] == "eliminated" and passed["gate"] == "probe"
    assert passed["probe"]["classified_from_probe"] is True
    one = cf([], AS_OF, volume="low", probe_runs=[frun("FAILURE", 2, 900, is_probe=True)])
    assert one["classification"] == "candidate" and one["gate"] == "probe"
    many = cf(
        [],
        AS_OF,
        volume="low",
        probe_runs=[
            frun("FAILURE", 2, 900, is_probe=True),
            frun("FAILURE", 10, 900, is_probe=True),
        ],
    )
    assert many["classification"] == "permafailing" and many["probe"]["pr"] == "900"
    # A stale probe is ignored -> the job stays under-observed.
    stale = cf([], AS_OF, volume="low", probe_runs=[frun("FAILURE", 240, 900, is_probe=True)])
    assert stale["classification"] == "under-observed"


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


def test_job_health_entries_carry_trend_and_flagged_blocker():
    doc = collect_fixture()
    jh = doc["job_health_below_slo"]
    assert jh, "fixture should surface below-SLO jobs"
    flagged_ids = {b["job_id"] for b in doc["incident_set"]["release_blockers"]} | {
        b["job_id"] for b in doc["incident_set"]["merge_queue_blockers"]
    }
    for entry in jh:
        assert "trend" in entry and "flagged_blocker" in entry
        assert entry["trend"] is None or isinstance(entry["trend"], str)
        # prev (prior-week rate) rides along so Phase 3 renders "previously N%" without a join.
        assert "prev" in entry
        assert entry["prev"] is None or isinstance(entry["prev"], (int, float))
        # flagged_blocker == membership in the permafailing-blocker set, so chaibot can exclude
        # already-flagged jobs from periodics-health deterministically (no LLM join needed).
        assert entry["flagged_blocker"] == (entry["job_id"] in flagged_ids)
        # Periodic entries carry a run link so Phase 3 can open logs to verify tracking fidelity.
        if entry["kind"] == "periodic":
            assert "history_url" in entry
            assert entry["history_url"] is None or isinstance(entry["history_url"], str)
    by_name = {e["name"]: e for e in jh}
    assert by_name["e2e-perma-aws"]["flagged_blocker"] is True
    assert by_name["e2e-perma-aws"]["history_url"]  # periodic carries its Prow history link


def test_payload_blocking_below_category_is_the_single_tracked_set():
    doc = collect_fixture()
    cat = {c["name"]: c for c in doc["payload_blocking_below_category"]}
    # Exactly the payload-blocking, non-permafailing, below-threshold periodic.
    assert set(cat) == {"e2e-both"}
    entry = cat["e2e-both"]
    assert entry["gate"] in {"release-payload", "both"}
    assert entry["rate"] < MODULE.PAYLOAD_BELOW_CATEGORY_PERCENT
    # The entry is pre-joined so Phase 3 reads it without touching periodics[].
    for key in ("job_id", "name", "release", "gate", "rate", "prev", "trend", "history_url"):
        assert key in entry
    # Excluded for the right reasons:
    #  - e2e-perma-aws: payload + below (rate 0%) but PERMAFAILING -> already a Phase-1 blocker
    #  - e2e-cr-perma / e2e-cr-ok: component-readiness, not payload-blocking
    #  - e2e-ok-aws: payload-blocking but meeting SLO
    #  - e2e-nulls: null rate (unknown), never silently categorized
    assert "e2e-perma-aws" not in cat
    assert "e2e-cr-perma" not in cat and "e2e-cr-ok" not in cat
    assert "e2e-ok-aws" not in cat and "e2e-nulls" not in cat


def test_payload_category_threshold_is_the_config_knob(monkeypatch):
    # The knob is loaded from config.toml (single source of truth), not hard-coded.
    assert isinstance(MODULE.PAYLOAD_BELOW_CATEGORY_PERCENT, (int, float))
    assert (
        MODULE._CONFIG["job_health"]["payload_below_category_percent"]
        == MODULE.PAYLOAD_BELOW_CATEGORY_PERCENT
    )
    # Lower the bar below e2e-both's 25% and it drops out -> the knob governs membership.
    monkeypatch.setattr(MODULE, "PAYLOAD_BELOW_CATEGORY_PERCENT", 20.0)
    assert collect_fixture()["payload_blocking_below_category"] == []


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


class _NoRegistryTransport(MODULE.FixtureTransport):
    def get_json(self, url):
        if url.rstrip("/").endswith("/api/job-registry"):
            raise MODULE.ReportError("job registry unavailable")
        return super().get_json(url)


def test_registry_failure_fails_closed_to_unknown():
    # The job inventory (/api/job-registry) is a hard dependency: losing it fails the report closed.
    collector = MODULE.Collector(
        _NoRegistryTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    )
    doc = collector.collect()
    assert doc["scope"]["state"] == "unknown"
    assert doc["periodics"] == [] and doc["scope"]["source_failures"]
    # the fail-closed twin still carries a well-formed (all-zero) at_a_glance block.
    assert doc["at_a_glance"]["release_blockers"] == 0 and doc["at_a_glance"]["jobs_below_slo"] == 0
    assert doc["at_a_glance"]["trend"] == {
        "improving": 0,
        "degrading": 0,
        "stable": 0,
        "insufficient": 0,
    }
    MODULE.validate_candidates_document(doc)  # the unknown report is still a valid document


class _MalformedAnalysisTransport(MODULE.FixtureTransport):
    # HTTP 200, valid JSON, wrong shape: result_count is a list, not a dict.
    def get_json(self, url):
        if "/api/jobs/analysis" in url:
            return {"by_period": {"2026-09-28 06:00": {"total_runs": 3, "result_count": [1, 2]}}}
        return super().get_json(url)


def test_malformed_sippy_analysis_degrades_not_crash():
    # Untrusted Sippy evidence of the wrong shape must not crash collect(); the guards coerce it
    # and the report is still produced WITH data (scope computed), not an empty crash-fallback.
    doc = MODULE.Collector(
        _MalformedAnalysisTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    ).collect()
    MODULE.validate_candidates_document(doc)
    assert doc["scope"]["releases"] == ["5.1", "5.0", "4.22", "4.21", "4.20"]  # not a crash fallback
    assert doc["periodics"]  # periodics still enumerated despite the malformed analyses


class _MalformedRegistryTransport(MODULE.FixtureTransport):
    # HTTP 200, valid JSON, wrong field types on otherwise-valid jobs.
    def get_json(self, url):
        if url.rstrip("/").endswith("/api/job-registry"):
            reg = super().get_json(url)
            for job in reg["jobs"]:
                if job.get("type") == "presubmit":
                    job["versions"] = 5  # non-list (would crash the versions filter)
                if job.get("type") == "periodic":
                    job["release_controller"] = 7  # non-list (would crash the participation loop)
            return reg
        return super().get_json(url)


def test_malformed_registry_types_degrade_not_crash():
    doc = MODULE.Collector(
        _MalformedRegistryTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    ).collect()
    MODULE.validate_candidates_document(doc)
    # The window is still derived (guards let _select_plan finish) rather than a crash fallback.
    assert doc["scope"]["dev_release"] == "5.1"
    assert doc["scope"]["branches"][0] == "main"


class _IdleAnalysisTransport(MODULE.FixtureTransport):
    # perma-aws's analysis is reachable but empty (no runs this window) -> idle, not a 0% failure.
    def get_json(self, url):
        if "/api/jobs/analysis" in url and "e2e-perma-aws" in url:
            return {"by_period": {}}
        return super().get_json(url)


def test_idle_periodic_reads_unknown_not_a_false_below_slo_blocker():
    doc = MODULE.Collector(
        _IdleAnalysisTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    ).collect()
    aws = next(p for p in doc["periodics"] if p["name"] == "e2e-perma-aws")
    assert aws["permafail"]["class"] == "unknown"  # idle -> not permafailing
    assert aws["slo"]["below_slo"] is None  # idle -> Unknown SLO, never a fabricated 0%
    assert "e2e-perma-aws" not in {j["name"] for j in doc["job_health_below_slo"]}
    assert "e2e-perma-aws" not in {b["name"] for b in doc["incident_set"]["release_blockers"]}


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
    # The 50h-old red must be retained (the window is not clipped at D=48h), so a >= D span is
    # observable by the funnel; classification itself happens later, in collect().
    oldest = min(MODULE.parse_rfc3339(r["completed"]) for r in evidence["runs"])
    assert MODULE.hours_between(oldest, AS_OF) >= 49


def test_release_blocker_payload_blocking_state():
    # Q1/Q2 attribution from the release controller: a release-blocking periodic is only
    # "blocking a payload" when its blocking verification on the stream's newest tag is not
    # Succeeded; last-accepted is the newest Accepted tag per gated stream (orthogonal to Q1).
    per = {p["name"]: p["payload"] for p in collect_fixture()["periodics"]}
    # e2e-perma-aws: 5.1.0-0.ci newest tag is Rejected and hypershift-e2e Failed -> blocking.
    aws = per["e2e-perma-aws"]
    assert aws["blocking_state"] == "yes"
    blocked = {b["stream"]: b for b in aws["blocked_payloads"]}
    assert "5.1.0-0.ci" in blocked and blocked["5.1.0-0.ci"]["overridden"] is False
    assert blocked["5.1.0-0.ci"]["url"].endswith(
        "/releasestream/5.1.0-0.ci/release/5.1.0-0.ci-2026-09-28-090000"
    )
    # last accepted is the earlier Accepted tag, not the Rejected newest (Q2 is independent).
    acc = {a["stream"]: a["tag"] for a in aws["last_accepted"]}
    assert acc["5.1.0-0.ci"] == "5.1.0-0.ci-2026-09-27-090000"
    # e2e-ok-aws: 5.0.0-0.ci newest tag Accepted and hypershift-e2e Succeeded -> not blocking.
    ok = per["e2e-ok-aws"]
    assert ok["blocking_state"] == "no" and ok["blocked_payloads"] == []


def test_payload_blocking_truth_table_and_override():
    collector = MODULE.Collector(
        MODULE.FixtureTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    )
    base = MODULE.RELEASE_CONTROLLER_BASE
    host = "https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com"
    status_map = {
        (base, "rej"): {"phase": "Rejected", "tag": "rej-t",
                        "last_accepted_tag": "rej-acc", "blocking_states": {"vrej": "Failed"}},
        (base, "ok"): {"phase": "Accepted", "tag": "ok-t",
                       "last_accepted_tag": "ok-t", "blocking_states": {"vok": "Succeeded"}},
        (base, "ovr"): {"phase": "Accepted", "tag": "ovr-t",
                        "last_accepted_tag": "ovr-prev", "blocking_states": {"vovr": "Failed"}},
    }

    def row(stream, verification):
        return {"participations": [{
            "stream_name": stream, "architecture": "amd64",
            "verification_name": verification,
            "release_status_url": f"{host}/releasestream/{stream}",
        }]}

    # Rejected + Failed -> blocking (not overridden)
    r = collector._payload_context(row("rej", "vrej"), status_map)
    assert r["blocking_state"] == "yes"
    assert r["blocked_payloads"][0]["overridden"] is False
    assert r["blocked_payloads"][0]["url"].endswith("/release/rej-t")
    assert r["last_accepted"][0]["tag"] == "rej-acc"
    # Accepted + Succeeded -> not blocking
    r = collector._payload_context(row("ok", "vok"), status_map)
    assert r["blocking_state"] == "no" and r["blocked_payloads"] == []
    # Accepted + Failed -> blocking but overridden (shipped despite the gate failing)
    r = collector._payload_context(row("ovr", "vovr"), status_map)
    assert r["blocking_state"] == "yes" and r["blocked_payloads"][0]["overridden"] is True
    # verification missing on the tag -> Unknown (never silently "no")
    assert collector._payload_context(row("rej", "absent"), status_map)["blocking_state"] == "unknown"
    # component-readiness-only (no participations) -> no
    assert collector._payload_context(None, status_map)["blocking_state"] == "no"


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
    assert "Recommended incident" in report and "e2e-perma-aws" in report


def test_html_report_escapes_annotation_html():
    report = MODULE.render_html_report(
        collect_fixture(),
        MODULE.validate_annotations({"summary": "<script>alert(1)</script>"}),
    )
    assert "<script>alert(1)</script>" not in report
    assert "&lt;script&gt;" in report


def test_html_report_collapses_eliminated_presubmits():
    # Under the funnel: an 'eliminated' stub with no rate collapses into a per-branch count, while an
    # 'under-observed' job (no signal to judge) is still surfaced -- never silently hidden.
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
                    "permafail": {"class": "candidate", "f": 3},
                    "slo": {"rate": 42.0, "below_slo": True},
                }
            ],
            "release-4.20": [
                {
                    "job_id": "pull-ci-openshift-hypershift-release-4.20-e2e-dead",
                    "name": "e2e-dead",
                    "permafail": {"class": "under-observed", "f": 0},
                    "slo": {"rate": None, "below_slo": False},
                },
                {
                    "job_id": "pull-ci-openshift-hypershift-release-4.20-e2e-empty",
                    "name": "e2e-empty",
                    "permafail": {"class": "eliminated", "f": 0},
                    "slo": {"rate": None, "below_slo": False},
                },
            ],
        },
        "incident_set": {"release_blockers": [], "merge_queue_blockers": []},
        "flaky_tests": [],
    }
    report = MODULE.render_html_report(doc, None)
    assert "Merge queue — required presubmits" in report
    # The candidate (with signal) is tabled.
    assert "<td><code>e2e-live</code>" in report
    assert 'class="pill amber">candidate' in report
    # under-observed is surfaced with its own pill, not collapsed.
    assert "<td><code>e2e-dead</code>" in report
    assert 'class="pill slate">under-observed' in report
    # The eliminated dataless stub is collapsed, never tabled.
    assert "1 presubmit(s) eliminated or without window data" in report
    assert "<td><code>e2e-empty</code>" not in report
    assert "<code>e2e-empty</code>" in report


def test_html_report_includes_overview_scoreboard():
    # Aggregation lives in the HTML, not Slack: the report opens with an at-a-glance scoreboard
    # whose counts are derived deterministically from the document.
    doc = collect_fixture()
    report = MODULE.render_html_report(doc, None)
    assert "At a glance" in report
    assert "source of truth" in report and "Sippy" in report
    assert "CI Health registry" in report  # inventory link
    n_below = len(doc["job_health_below_slo"])
    assert f"<tr><td>Jobs below SLO</td><td>{n_below}</td></tr>" in report
    n_release = len(doc["incident_set"]["release_blockers"])
    assert f"<tr><td>Release blockers</td><td>{n_release}</td></tr>" in report
    # the emitted at_a_glance block is the single source the HTML scoreboard renders from.
    glance = doc["at_a_glance"]
    assert glance["jobs_below_slo"] == n_below and glance["release_blockers"] == n_release
    assert f"<tr><td>Trend degrading</td><td>{glance['trend']['degrading']}</td></tr>" in report


def test_at_a_glance_emitted_and_single_sourced():
    # The scoreboard tally is emitted into data.json (so Phase 1 reads it, never re-tallies) and is
    # computed by the same function the HTML renders from -> Slack and the report cannot diverge.
    doc = collect_fixture()
    glance = doc["at_a_glance"]
    assert glance == MODULE.compute_at_a_glance(doc)
    assert glance["jobs_below_slo"] == len(doc["job_health_below_slo"])
    assert glance["release_blockers"] == len(doc["incident_set"]["release_blockers"])
    assert set(glance["trend"]) == {"improving", "degrading", "stable", "insufficient"}


def test_at_a_glance_fallback_when_absent():
    # A document without the emitted block (e.g. an older fixture) still renders via a recompute.
    doc = collect_fixture()
    stripped = copy.deepcopy(doc)
    stripped.pop("at_a_glance", None)
    report = MODULE.render_html_report(stripped, None)
    assert "At a glance" in report
    assert (
        f"<tr><td>Jobs below SLO</td><td>{len(doc['job_health_below_slo'])}</td></tr>" in report
    )


def test_release_blockers_enriched_from_periodics():
    # Each release blocker carries the payload/streak/rate fields Phase 1 renders and the SLI rate
    # Phase 4 reads -- copied faithfully from the matching periodic, so no cross-section join.
    doc = collect_fixture()
    blockers = doc["incident_set"]["release_blockers"]
    assert blockers, "fixture should surface release blockers"
    per = {p["job_id"]: p for p in doc["periodics"]}
    for b in blockers:
        p = per[b["job_id"]]
        assert b["rate"] == p["slo"].get("rate")
        assert b["streak_runs"] == p["permafail"]["streak_runs"]
        assert b["span_hours"] == p["permafail"]["span_hours"]
        assert b["blocking_state"] == p["payload"]["blocking_state"]
        assert b["blocked_payloads"] == p["payload"]["blocked_payloads"]
        assert b["last_accepted"] == p["payload"]["last_accepted"]
        assert b["prev"] == (p["slo"].get("trend") or {}).get("prev")
        assert b["blocking_state"] in {"yes", "no", "unknown"}


def test_candidate_red_runs_and_f():
    # Phase 2 reads these instead of counting by hand: red_runs = FAILUREs in the sampled runs it can
    # open; f = distinct failing PRs (the real-break bar); gate = the funnel gate that surfaced it.
    doc = collect_fixture()
    cands = [c for group in doc["presubmit_candidates"].values() for c in group]
    assert cands, "fixture should surface merge-queue candidates"
    for c in cands:
        assert c["name"] and isinstance(c["name"], str)  # short display name for Slack, emitted
        expected_reds = sum(1 for r in c["runs"] if r.get("state") == "FAILURE")
        assert c["red_runs"] == expected_reds
        assert isinstance(c["f"], int) and c["f"] >= 0
        assert c["gate"] in {"G4", "probe"}
        if c["gate"] == "G4":
            assert c["f"] >= 2  # a throughput-gate candidate has >= 2 distinct failing PRs
        assert isinstance(c["red_days_met"], bool)
        assert c["red_days_met"] == (c["span_hours"] >= MODULE.PERMAFAIL_DURATION_HOURS)


def test_presubmit_throughput_and_under_observed_emitted():
    # The funnel emits a per-branch throughput verdict and an under-observed list, both keyed by the
    # exact scope branches, so Phase 1/5 and the HTML can read them without a join.
    doc = collect_fixture()
    branches = set(doc["scope"]["branches"])
    assert set(doc["presubmit_throughput"]) == branches
    for block in doc["presubmit_throughput"].values():
        assert block["volume"] in {"high", "low"}
        assert isinstance(block["distinct_prs"], int)
    assert set(doc["under_observed"]) == branches
    for rows in doc["under_observed"].values():
        assert isinstance(rows, list)


class _ThroughputTransport:
    """Serves one Prow history page of `count` distinct-PR SUCCESS builds within the window."""

    def __init__(self, count):
        self.count = count

    def configure_collection_limits(self, deadline_seconds, max_requests):
        pass

    def get_text(self, url):
        job = "pull-ci-openshift-hypershift-main-e2e-rep"
        builds = [
            {
                "SpyglassLink": f"/view/gs/test-platform-results/pr-logs/pull/openshift_hypershift/{i + 1}/{job}/{i + 1}",
                "ID": str(i + 1),
                "Started": MODULE.format_rfc3339(AS_OF - dt.timedelta(hours=2 + i)),
                "Duration": 600000000000,
                "Result": "SUCCESS",
                "Refs": {"pulls": [{"number": i + 1}]},
            }
            for i in range(self.count)
        ]
        return "<script>var allBuilds = " + json.dumps(builds) + ";</script>"


def test_branch_throughput_high_low(monkeypatch):
    rep = {
        "prow_job_history_url": (
            "https://prow.ci.openshift.org/job-history/gs/test-platform-results/"
            "pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-rep"
        )
    }
    # 20 distinct PRs over a 5-business-day window -> 4.0 PRs/bd >= HI (3.0) -> high.
    monkeypatch.setattr(MODULE, "THROUGHPUT_WINDOW_BUSINESS_DAYS", 5)
    hi = MODULE.Collector(
        _ThroughputTransport(20), AS_OF, source_revision=SOURCE_REVISION
    )._branch_throughput(rep, "e2e-rep")
    assert hi["volume"] == "high" and hi["distinct_prs"] == 20
    # The same jobs over a 20-business-day window -> 1.0 PRs/bd -> low.
    monkeypatch.setattr(MODULE, "THROUGHPUT_WINDOW_BUSINESS_DAYS", 20)
    lo = MODULE.Collector(
        _ThroughputTransport(20), AS_OF, source_revision=SOURCE_REVISION
    )._branch_throughput(rep, "e2e-rep")
    assert lo["volume"] == "low"


def test_report_renders_incident_from_annotation_when_incident_set_empty():
    # A Phase-2-confirmed break or merge-queue-handoff incident is not in incident_set; the recommended
    # incident must still render from the annotation, never be dropped for a green all-clear.
    doc = copy.deepcopy(collect_fixture())
    doc["incident_set"] = {"release_blockers": [], "merge_queue_blockers": []}
    note = "release-4.22 blocked 3 days across sub-2-day presubmit handoffs."
    report = MODULE.render_html_report(
        doc, MODULE.validate_annotations({"summary": "s", "incident": note})
    )
    assert note in report
    assert "No permafailing release or merge-queue blockers." not in report
    # with no incident annotation and an empty incident_set, the green all-clear shows
    clear = MODULE.render_html_report(doc, MODULE.validate_annotations({"summary": "s"}))
    assert "No permafailing release or merge-queue blockers." in clear


def test_validate_rejects_malformed_at_a_glance_and_release_blocker():
    doc = collect_fixture()
    MODULE.validate_candidates_document(doc)  # valid baseline
    missing = copy.deepcopy(doc)
    missing.pop("at_a_glance")
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_candidates_document(missing)
    negative = copy.deepcopy(doc)
    negative["at_a_glance"]["jobs_below_slo"] = -1
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_candidates_document(negative)
    assert doc["incident_set"]["release_blockers"], "fixture should surface release blockers"
    bad_state = copy.deepcopy(doc)
    bad_state["incident_set"]["release_blockers"][0]["blocking_state"] = "maybe"
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_candidates_document(bad_state)
    bad_rate = copy.deepcopy(doc)
    bad_rate["incident_set"]["release_blockers"][0]["rate"] = 150
    with pytest.raises(MODULE.ReportError):
        MODULE.validate_candidates_document(bad_rate)


def test_dead_flaky_threshold_knob_removed():
    # The knob was loaded but never applied; it is gone from both the module and the config.
    assert not hasattr(MODULE, "PERIODIC_FLAKY_THRESHOLD_PERCENT")
    assert "flaky_threshold_percent" not in MODULE._CONFIG.get("periodic", {})


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


# ---------------------------------------------------------------------------
# Sippy health-window builder — the transforms that replace the dashboard /windows/1w fetch
# ---------------------------------------------------------------------------
def _period(moment):
    return moment.strftime("%Y-%m-%d %H:00")


def test_release_rank_and_inverse():
    assert MODULE.release_rank("4.20") == 20
    assert MODULE.release_rank("5.0") == 23 and MODULE.release_rank("5.1") == 24
    assert MODULE.release_rank("bogus") == -1
    assert MODULE.release_from_rank(20) == "4.20"
    assert MODULE.release_from_rank(23) == "5.0" and MODULE.release_from_rank(24) == "5.1"


def test_sippy_display_name():
    dn = MODULE.sippy_display_name
    assert dn("pull-ci-openshift-hypershift-main-e2e-streak") == "e2e-streak"
    assert dn("pull-ci-openshift-hypershift-release-5.0-e2e-veto") == "e2e-veto"
    assert (
        dn("periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-perma-aws")
        == "e2e-perma-aws"
    )


def test_spark_slot_keys_span_one_week():
    keys = MODULE.spark_slot_keys(AS_OF)
    assert len(keys) == MODULE.SPARK_SLOTS
    assert keys[-1] == "2026-09-28 12:00" and keys[0] == "2026-09-21 18:00"


def test_summarize_analysis_current_vs_previous():
    by_period = {
        _period(AS_OF - dt.timedelta(days=1)): {"total_runs": 10, "result_count": {"S": 8, "F": 2}},
        _period(AS_OF - dt.timedelta(days=10)): {"total_runs": 10, "result_count": {"S": 5, "F": 5}},
    }
    s = MODULE.summarize_analysis(by_period, AS_OF)
    assert s["rate"] == 80.0 and s["prev"] == 50.0
    assert s["runs"] == 10 and s["prev_runs"] == 10 and s["trend"] == 30.0
    # No current-window runs -> None (Unknown), never a false 0%: absent analysis AND an
    # idle/present-but-empty one both read as no-data (a dormant job is not a 0% failure).
    assert MODULE.summarize_analysis(None, AS_OF) is None
    assert MODULE.summarize_analysis({}, AS_OF) is None
    only_prev = {_period(AS_OF - dt.timedelta(days=10)): {"total_runs": 4, "result_count": {"S": 4}}}
    assert MODULE.summarize_analysis(only_prev, AS_OF) is None  # runs only outside the current window


def test_summarize_analysis_clamps_rate_to_100():
    # A malformed Sippy row (S > total_runs) must not yield rate > 100, which would fail
    # validate_health and discard the whole report.
    bad = {_period(AS_OF - dt.timedelta(days=1)): {"total_runs": 5, "result_count": {"S": 10}}}
    s = MODULE.summarize_analysis(bad, AS_OF)
    assert s["rate"] == 100.0


def test_analysis_sparkline_empty_vs_absent():
    slots = MODULE.spark_slot_keys(AS_OF)
    assert MODULE.analysis_sparkline(None, slots, AS_OF) is None  # absent
    empty = MODULE.analysis_sparkline({}, slots, AS_OF)  # present but empty
    assert empty == [None] * MODULE.SPARK_SLOTS
    one = MODULE.analysis_sparkline(
        {_period(AS_OF): {"total_runs": 1, "result_count": {"S": 1}}}, slots, AS_OF
    )
    assert one[-1] == [1, 1, 0, 0] and one[0] is None


def test_build_sippy_alerts_filters_to_presubmits():
    rows = [
        {"test_name": "flaky A", "outputs": [{"prow_job_name": "pull-ci-openshift-hypershift-main-e2e-x"}]},
        {"test_name": "flaky B", "outputs": [{"prow_job_name": "periodic-not-a-presubmit"}]},
    ]
    alerts = MODULE.build_sippy_alerts(rows, {"pull-ci-openshift-hypershift-main-e2e-x"})
    assert len(alerts) == 1
    assert alerts[0]["test_name"] == "flaky A" and alerts[0]["jobs"] == ["e2e-x"]


def test_select_plan_applies_filters_and_window():
    collector = MODULE.Collector(
        MODULE.FixtureTransport(FIXTURES), AS_OF, source_revision=SOURCE_REVISION
    )
    jobs = [
        {
            "id": "pull-ci-openshift-hypershift-main-e2e-req", "type": "presubmit",
            "e2e_framework": "v1", "versions": [], "prow_job_history_url": "",
            "presubmit": {"target_branch": "main", "target_release": "5.1",
                          "required": True, "sippy_ingestion": {"enabled": True}},
        },
        {
            "id": "pull-ci-openshift-hypershift-main-e2e-opt", "type": "presubmit",
            "e2e_framework": "v1", "versions": [], "prow_job_history_url": "",
            "presubmit": {"target_branch": "main", "target_release": "5.1",
                          "required": False, "sippy_ingestion": {"enabled": True}},
        },
        {
            "id": "pull-ci-openshift-hypershift-main-verify", "type": "presubmit",
            "e2e_framework": "none", "versions": [], "prow_job_history_url": "",
            "presubmit": {"target_branch": "main", "target_release": "5.1",
                          "required": True, "sippy_ingestion": {"enabled": False}},
        },
        {
            "id": "periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-block",
            "type": "periodic", "prow_job_history_url": "",
            "release_controller": [{"stream": {"name": "5.1.0-0.ci", "release": "5.1",
                                                "end_of_life": False},
                                    "verification": {"name": "hypershift-e2e", "role": "blocking"}}],
        },
        {
            "id": "periodic-ci-openshift-hypershift-release-4.10-periodics-e2e-oldrel",
            "type": "periodic", "prow_job_history_url": "",
            "release_controller": [{"stream": {"release": "4.10", "end_of_life": False},
                                    "verification": {"role": "blocking"}}],
        },
        {
            "id": "periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-inform",
            "type": "periodic", "prow_job_history_url": "",
            "release_controller": [{"stream": {"release": "5.1", "end_of_life": False},
                                    "verification": {"role": "informing"}}],
        },
    ]
    plan = collector._select_plan(jobs)
    assert plan["dev_release"] == "5.1"
    assert plan["releases"] == ["4.20", "4.21", "4.22", "5.0", "5.1"]
    # required presubmits, e2e or not (verify included); non-required and informing/out-of-window excluded.
    assert sorted(p["name"] for p in plan["presubmits"]) == [
        "pull-ci-openshift-hypershift-main-e2e-req",
        "pull-ci-openshift-hypershift-main-verify",
    ]
    assert [p["name"] for p in plan["payload_jobs"]] == [
        "periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-block"
    ]
    assert ("Presubmits", "pull-ci-openshift-hypershift-main-e2e-req") in plan["analysis_targets"]
    assert (
        "5.1",
        "periodic-ci-openshift-hypershift-release-5.1-periodics-e2e-block",
    ) in plan["analysis_targets"]


def test_parse_bodies_sections():
    text = (
        "@@ title_time\n"
        "07 Oct, 19:38 UTC\n"
        "@@ overview\n"
        "• *Scope:* OCP 5.1.\n"
        "\n"
        "• *SLO:* 47 below.\n"
        "@@ merge_queue\n"
        "• *Confirmed*\n"
    )
    bodies = MODULE.parse_bodies(text)
    assert bodies["title_time"] == "07 Oct, 19:38 UTC"
    # internal blank line kept; leading/trailing newlines trimmed
    assert bodies["overview"] == "• *Scope:* OCP 5.1.\n\n• *SLO:* 47 below."
    assert bodies["merge_queue"] == "• *Confirmed*"


def test_parse_bodies_errors():
    with pytest.raises(MODULE.ReportError):
        MODULE.parse_bodies("no markers here\njust text")  # no sections
    with pytest.raises(MODULE.ReportError):
        MODULE.parse_bodies("stray text\n@@ overview\nbody")  # content before first section
    with pytest.raises(MODULE.ReportError):
        MODULE.parse_bodies("@@ overview\na\n@@ overview\nb")  # duplicate section


def test_render_message_stamps_phase1_template_with_exact_bars(tmp_path):
    bodies = (
        "@@ title_time\n"
        "07 Oct, 19:38 UTC\n"
        "@@ overview\n"
        "• *Scope:* OCP 5.1, 5.0, 4.22, 4.21, 4.20.\n"
        "@@ release_blockers\n"
        "• *OCP 4.22*\n"
        "  ◦ `e2e-v2-aws`\n"
        "@@ merge_queue\n"
        "• *Confirmed*\n"
        "  ◦ None found in assessed data.\n"
        "@@ partial_data\n"
        ":warning: Partial data: 25 jobs could not be fully assessed this run (Unknown).\n"
    )
    bodies_path = tmp_path / "phase-1-bodies.txt"
    bodies_path.write_text(bodies)
    out = tmp_path / "message.txt"
    args = MODULE.build_parser().parse_args(
        ["render-message", "--phase", "1", "--bodies", str(bodies_path), "--out", str(out)]
    )
    assert MODULE.render_message_command(args) == 0
    text = out.read_text()
    # the hand-tuned divider bars survive verbatim
    assert "=" * 40 in text and "-" * 23 in text and "-" * 48 in text and "-" * 58 in text
    assert text.rstrip().endswith("=" * 65)
    # header substituted and bold/code body stamped in
    assert ":mega: HyperShift CI Daily Health — 07 Oct, 19:38 UTC" in text
    assert "• *OCP 4.22*" in text and "`e2e-v2-aws`" in text and "(part 1/5)" in text


def test_render_message_missing_section_raises():
    template = "====\n:bulb: $title_time\n====\n$overview\n(part 1/5)"
    with pytest.raises(MODULE.ReportError):
        MODULE.render_message(template, {"title_time": "now"})  # $overview not supplied


def test_render_message_dollar_in_body_is_literal():
    out = MODULE.render_message("---\n:x: Header\n---\n$body", {"body": "cost is $5 for $x"})
    assert out.endswith("cost is $5 for $x")
