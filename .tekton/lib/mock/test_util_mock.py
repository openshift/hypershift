"""Mock utilities for HO release gate pipeline integration testing.

This module provides drop-in replacements for functions that call
external services (Gangway for Prow job triggering, KubeArchive for
PipelineRun history). It allows running the full Tekton pipeline
end-to-end without hitting real infrastructure, while still exercising
the gate evaluation logic, Slack notifications, and stale promotion
alerting.

Each mock function name carries a suffix that describes its behavior,
making substitutions easy to spot in diffs and code review. All output
is prefixed with ``[MOCK]`` in PipelineRun logs for easy identification.

Usage:
    To use these mocks, temporarily change the imports and function
    calls in the pipeline inline scripts. For example, in run-e2e::

        # Real:
        from ho_release_gate import trigger_all_jobs, poll_until_complete
        jobs = trigger_all_jobs(blocking, informing, ...)
        poll_until_complete(jobs, ...)

        # Mock (gate failure path):
        from mock.test_util_mock import trigger_all_jobs_mock_test_fail
        from mock.test_util_mock import poll_until_complete_mock
        jobs = trigger_all_jobs_mock_test_fail(blocking, informing, ...)
        poll_until_complete_mock(jobs, ...)

    For the stale check in notify-slack / notify-slack-error, patch
    the module-level reference before calling check_and_build_stale_payload::

        # Real: check_and_build_stale_payload calls fetch_pipelineruns internally.
        # Mock (stale alert path):
        import ho_release_gate
        from mock.test_util_mock import fetch_pipelineruns_mock_stale
        ho_release_gate.fetch_pipelineruns = fetch_pipelineruns_mock_stale

Test scenarios:
    1. Gate failed, real KubeArchive, no stale (notify-slack path):
       Use trigger_all_jobs_mock_test_fail in run-e2e.
       No mock on fetch_pipelineruns (real KubeArchive call).
       gate-label: "WIP: TEST 1 - notify-slack, real KA, no stale"

    2. Gate failed, real KubeArchive, no stale (notify-slack-error path):
       Use trigger_all_jobs_mock_error in run-e2e.
       No mock on fetch_pipelineruns (real KubeArchive call).
       gate-label: "WIP: TEST 2 - notify-slack-error, real KA, no stale"

    3. Gate failed, mock KubeArchive, stale alert (notify-slack path):
       Use trigger_all_jobs_mock_test_fail in run-e2e.
       Patch fetch_pipelineruns with fetch_pipelineruns_mock_stale.
       gate-label: "WIP: TEST 3 - notify-slack, mock KA, stale alert"

    4. Gate failed, mock KubeArchive, stale alert (notify-slack-error path):
       Use trigger_all_jobs_mock_error in run-e2e.
       Patch fetch_pipelineruns with fetch_pipelineruns_mock_stale.
       gate-label: "WIP: TEST 4 - notify-slack-error, mock KA, stale alert"

Side effects:
    Every test PipelineRun is archived in KubeArchive with the same
    ITS scenario label as production runs. Failed test runs therefore
    contribute to the real streak history. The first successful
    nightly run after testing resets the streak automatically.
"""

import os
import sys
from datetime import datetime, timezone, timedelta

from prow_utils import short_name

__all__ = [
    "trigger_all_jobs_mock_test_fail",
    "trigger_all_jobs_mock_error",
    "poll_until_complete_mock",
    "fetch_pipelineruns_mock_empty",
    "fetch_pipelineruns_mock_stale",
    "fetch_pipelineruns_mock_stale_long",
]


# ---------------------------------------------------------------------------
# Gangway / Prow mocks (used in run-e2e task)
# ---------------------------------------------------------------------------

def trigger_all_jobs_mock_test_fail(blocking_names, informing_names,
                                    gangway_url, token, env_overrides,
                                    trigger_delay=60, rate_limit_backoff=120,
                                    max_retries=3):
    """Mock replacement for ho_release_gate.trigger_all_jobs.

    Simulates a gate failure without calling Gangway. Returns all
    jobs with result="failed" and job_id=None. Since job_id is None,
    resolve_all_urls will skip every job automatically (no Gangway
    polling needed).

    Use this when testing the notify-slack path: run-e2e completes
    (exit 0), evaluate-gate sees all blocking tests failed, gate
    verdict is false, and the notify-slack finally task fires.

    Same signature as the real function so it can be swapped in
    without changing the call site.

    Args:
        blocking_names: List of blocking Prow job name strings.
        informing_names: List of informing Prow job name strings.
        gangway_url: Ignored (no API calls made).
        token: Ignored (no API calls made).
        env_overrides: Ignored (no API calls made).
        trigger_delay: Ignored (no delays).
        rate_limit_backoff: Ignored (no retries).
        max_retries: Ignored (no retries).

    Returns:
        List of job dicts with result="failed", job_id=None, url="".

    Raises:
        ValueError: If blocking_names is empty (same guard as real).
    """
    if not blocking_names:
        raise ValueError("e2e-blocking-job-names is empty"
                         " - at least one blocking job is required")

    print("[MOCK] trigger_all_jobs_mock_test_fail"
          " - returning all jobs as failed", flush=True)

    jobs = []

    for name in blocking_names:
        sn = short_name(name)
        print(f"  [MOCK] [blocking] [{sn}] -> failed", flush=True)
        jobs.append({
            "name": name,
            "type": "blocking",
            "job_id": None,
            "url": "",
            "result": "failed",
        })

    for name in informing_names:
        sn = short_name(name)
        print(f"  [MOCK] [informing] [{sn}] -> failed", flush=True)
        jobs.append({
            "name": name,
            "type": "informing",
            "job_id": None,
            "url": "",
            "result": "failed",
        })

    return jobs


def trigger_all_jobs_mock_error(blocking_names, informing_names,
                                gangway_url, token, env_overrides,
                                trigger_delay=60, rate_limit_backoff=120,
                                max_retries=3):
    """Mock replacement for ho_release_gate.trigger_all_jobs.

    Simulates a DAG-level crash by calling sys.exit(1). This causes
    the run-e2e task to fail with a non-zero exit code, which means
    downstream DAG tasks (evaluate-gate, create-release) are never
    reached. The notify-slack-error finally task fires instead of
    notify-slack.

    Use this when testing the notify-slack-error path.

    Same signature as the real function so it can be swapped in
    without changing the call site.

    Args:
        blocking_names: Ignored (crash happens before processing).
        informing_names: Ignored.
        gangway_url: Ignored.
        token: Ignored.
        env_overrides: Ignored.
        trigger_delay: Ignored.
        rate_limit_backoff: Ignored.
        max_retries: Ignored.

    Raises:
        SystemExit: Always (exit code 1).
    """
    print("[MOCK] trigger_all_jobs_mock_error"
          " - simulating DAG-level crash (sys.exit(1))", flush=True)
    sys.exit(1)


def poll_until_complete_mock(jobs, gangway_url, token, initial_delay=2700,
                             poll_interval=600, poll_stagger=30,
                             timeout=14400):
    """Mock replacement for ho_release_gate.poll_until_complete.

    No-op. When used with trigger_all_jobs_mock_test_fail, all jobs
    already have final results (result="failed"), so there is nothing
    to poll. This avoids the real function's initial_delay (45 min)
    and all subsequent poll cycles, making the test run complete in
    seconds instead of hours.

    Same signature as the real function so it can be swapped in
    without changing the call site.

    Args:
        jobs: List of job dicts (already have final results).
        gangway_url: Ignored.
        token: Ignored.
        initial_delay: Ignored (no waiting).
        poll_interval: Ignored (no polling).
        poll_stagger: Ignored (no polling).
        timeout: Ignored (no polling).
    """
    pending = [j for j in jobs if j["result"] == "pending"]
    print(f"[MOCK] poll_until_complete_mock: {len(pending)} pending"
          f" job(s), skipping all delays and polling", flush=True)


# ---------------------------------------------------------------------------
# KubeArchive mocks (used in notify-slack / notify-slack-error tasks)
# ---------------------------------------------------------------------------

def fetch_pipelineruns_mock_empty(token, namespace, label_selector):
    """Mock replacement for kubearchive_utils.fetch_pipelineruns.

    Returns an empty list, simulating a scenario where KubeArchive
    has no historical PipelineRuns for the given label selector.
    This means check_and_build_stale_payload will find no streak
    and will skip the stale alert, producing a normal failure
    notification instead.

    Use this when testing the notification path without stale
    alerting (tests 1 and 2). Alternatively, leave the real
    fetch_pipelineruns in place to query actual KubeArchive data.

    Same signature as the real function so it can be patched in::

        import ho_release_gate
        from mock.test_util_mock import fetch_pipelineruns_mock_empty
        ho_release_gate.fetch_pipelineruns = fetch_pipelineruns_mock_empty

    Args:
        token: Ignored (no API calls made).
        namespace: Ignored (no API calls made).
        label_selector: Ignored (no API calls made).

    Returns:
        Empty list.
    """
    print("[MOCK] fetch_pipelineruns_mock_empty"
          " - returning empty run list (no streak)", flush=True)
    return []


def fetch_pipelineruns_mock_stale(token, namespace, label_selector):
    """Mock replacement for kubearchive_utils.fetch_pipelineruns.

    Generates a synthetic list of failed PipelineRuns that spans
    enough days to trigger the stale promotion alert. The number
    of days is derived from the STALE_THRESHOLD_DAYS environment
    variable, which is already set in the pipeline tasks as part
    of the stale alert configuration. The generated streak covers
    threshold_days + 1 days to guarantee the stale condition is met
    (streak_days >= threshold_days).

    Each generated run is spaced one day apart, with the most recent
    run placed yesterday (relative to now) and the oldest run placed
    threshold_days ago. All runs have status=False and reason="Failed".

    Use this when testing the stale alert notification (tests 3 and 4).
    Patch into ho_release_gate before calling check_and_build_stale_payload::

        import ho_release_gate
        from mock.test_util_mock import fetch_pipelineruns_mock_stale
        ho_release_gate.fetch_pipelineruns = fetch_pipelineruns_mock_stale

    Args:
        token: Ignored (no API calls made).
        namespace: Ignored (no API calls made).
        label_selector: Ignored (no API calls made).

    Returns:
        List of run dicts sorted most-recent-first, each with keys
        name, created (ISO 8601), status (bool), reason (str).
        The list spans threshold_days + 1 days to ensure the stale
        alert triggers.

    Note:
        Reads STALE_THRESHOLD_DAYS from the environment (set by the
        pipeline, default 3). This is NOT a mock-specific env var;
        it is the same threshold the production stale check uses.
    """
    threshold = int(os.environ.get("STALE_THRESHOLD_DAYS", "3"))
    num_runs = threshold + 1
    now = datetime.now(timezone.utc)

    failure_reasons = ["Failed", "PipelineRunTimeout", "CouldntGetPipeline"]

    runs = []
    for i in range(num_runs):
        days_ago = i + 1
        ts = (now - timedelta(days=days_ago)).strftime(
            "%Y-%m-%dT%H:%M:%SZ")
        runs.append({
            "name": f"mock-failed-run-{num_runs - i}",
            "created": ts,
            "status": False,
            "reason": failure_reasons[i % len(failure_reasons)],
        })

    print(f"[MOCK] fetch_pipelineruns_mock_stale: generating"
          f" {num_runs} failed run(s) over {threshold + 1} day(s)"
          f" (STALE_THRESHOLD_DAYS={threshold})", flush=True)
    for r in runs:
        print(f"  [MOCK] {r['name']}"
              f" | {r['created']}"
              f" | {r['status']}", flush=True)

    return runs


def fetch_pipelineruns_mock_stale_long(token, namespace, label_selector):
    """Mock replacement for kubearchive_utils.fetch_pipelineruns.

    Generates a large list of failed PipelineRuns spanning 15 days,
    with multiple runs on some days, to test the history truncation
    logic in build_stale_notification. The list includes all three
    failure reason types (Failed, PipelineRunTimeout, CouldntGetPipeline)
    distributed across the runs.

    The generated runs simulate a realistic pattern where some days
    have a single nightly run while others have retries or re-triggers.

    Use this when testing the stale alert with history truncation
    (test 5). Patch into ho_release_gate before calling
    check_and_build_stale_payload::

        import ho_release_gate
        from mock.test_util_mock import fetch_pipelineruns_mock_stale_long
        ho_release_gate.fetch_pipelineruns = fetch_pipelineruns_mock_stale_long

    Args:
        token: Ignored (no API calls made).
        namespace: Ignored (no API calls made).
        label_selector: Ignored (no API calls made).

    Returns:
        List of 18 run dicts sorted most-recent-first, spanning 15
        days, with varied reasons and multiple runs on some days.
    """
    now = datetime.now(timezone.utc)
    failure_reasons = ["Failed", "PipelineRunTimeout", "CouldntGetPipeline"]

    day_pattern = [
        (1, 1), (2, 1), (3, 2), (4, 1), (5, 1),
        (6, 2), (7, 1), (8, 1), (9, 2), (10, 1),
        (11, 1), (12, 1), (13, 1), (14, 1), (15, 1),
    ]

    runs = []
    run_idx = 0
    for days_ago, count in day_pattern:
        for j in range(count):
            hour = 3 + j * 4
            ts = (now - timedelta(days=days_ago, hours=-hour)).strftime(
                "%Y-%m-%dT%H:%M:%SZ")
            run_idx += 1
            runs.append({
                "name": f"mock-long-run-{run_idx}",
                "created": ts,
                "status": False,
                "reason": failure_reasons[run_idx % len(failure_reasons)],
            })

    runs.sort(key=lambda r: r["created"], reverse=True)

    print(f"[MOCK] fetch_pipelineruns_mock_stale_long: generating"
          f" {len(runs)} failed run(s) over 15 day(s)", flush=True)
    for r in runs:
        print(f"  [MOCK] {r['name']}"
              f" | {r['created']}"
              f" | {r['reason']}", flush=True)

    return runs
