"""Pipeline-specific logic for the HO Release Gate."""

import json
import re
import time
from datetime import datetime, timezone

from kubearchive_utils import fetch_pipelineruns, build_pipelinerun_url
from prow_utils import (trigger_prow_job, resolve_prow_url,
                        get_prow_job_status, short_name)
from slack_utils import build_slack_payload, mrkdwn_section, fields_section, divider

__all__ = ["extract_component_image",
           "trigger_all_jobs", "resolve_all_urls", "poll_until_complete",
           "print_run_summary", "build_results_json", "evaluate_gate",
           "build_gate_notification", "build_error_notification",
           "check_failure_streak", "build_stale_notification",
           "check_and_build_stale_payload"]


def extract_component_image(snapshot_json_str,
                            component_name="hypershift-operator-main"):
    """Extract container image reference from a Konflux Snapshot JSON.

    Parses the Snapshot, locates the named component, and returns its
    container image along with the sha256 digest and git revision.

    Args:
        snapshot_json_str: Raw JSON string of the Konflux Snapshot.
        component_name: Name of the component to extract (default:
            hypershift-operator-main).

    Returns:
        Dict with keys: image, digest, revision.

    Raises:
        ValueError: If the JSON is invalid, the component is not found,
            or the containerImage field is missing/empty.
    """
    try:
        snapshot = json.loads(snapshot_json_str)
    except (json.JSONDecodeError, TypeError) as e:
        raise ValueError(f"SNAPSHOT JSON is not valid: {e}")

    for comp in snapshot.get("components", []):
        if comp.get("name") == component_name:
            image = comp.get("containerImage", "")
            if not image:
                raise ValueError(
                    f"Component '{component_name}' has no containerImage")

            match = re.search(r"sha256:[a-f0-9]+", image)
            digest = match.group(0) if match else "N/A"

            revision = (comp.get("source", {})
                        .get("git", {})
                        .get("revision", "N/A"))

            return {"image": image, "digest": digest, "revision": revision}

    raise ValueError(f"Component '{component_name}' not found in SNAPSHOT")


def trigger_all_jobs(blocking_names, informing_names, gangway_url, token,
                     env_overrides, trigger_delay=60, rate_limit_backoff=120,
                     max_retries=3):
    """Trigger all blocking and informing Prow jobs sequentially.

    Iterates through blocking jobs first, then informing, with a delay
    between each trigger to respect Gangway rate limits. Raises
    ValueError if blocking_names is empty.

    Args:
        blocking_names: List of blocking Prow periodic job name strings.
            At least one is required.
        informing_names: List of informing Prow periodic job name strings.
            May be empty.
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        env_overrides: Dict of environment variables injected into each
            Prow job's PodSpec (e.g. HO image override).
        trigger_delay: Seconds to wait between triggering consecutive jobs.
        rate_limit_backoff: Seconds to wait on 429/5xx before retrying
            a single trigger (passed through to trigger_prow_job).
        max_retries: Maximum trigger attempts per job on retryable errors.

    Returns:
        List of job dicts, each with keys: name, type, job_id, url, result.
        Jobs that failed to trigger have job_id=None and result="error".
    """
    if not blocking_names:
        raise ValueError("e2e-blocking-job-names is empty"
                         " - at least one blocking job is required")

    num_blocking = len(blocking_names)
    num_informing = len(informing_names)
    print(f"Blocking: {num_blocking} | Informing: {num_informing}"
          f" | Total: {num_blocking + num_informing}", flush=True)
    print(f"Trigger delay: {trigger_delay}s"
          f" | Rate limit backoff: {rate_limit_backoff}s"
          f" | Max retries: {max_retries}", flush=True)

    jobs = []

    print(f"\n=== run-e2e: triggering {num_blocking} blocking job(s) ===",
          flush=True)
    for i, name in enumerate(blocking_names):
        job = _trigger_single(name, "blocking", gangway_url, token,
                              env_overrides, max_retries, rate_limit_backoff)
        jobs.append(job)
        if i < num_blocking - 1 or num_informing > 0:
            time.sleep(trigger_delay)

    if num_informing > 0:
        print(f"\n=== run-e2e: triggering {num_informing} informing job(s) ===",
              flush=True)
        for i, name in enumerate(informing_names):
            job = _trigger_single(name, "informing", gangway_url, token,
                                  env_overrides, max_retries, rate_limit_backoff)
            jobs.append(job)
            if i < num_informing - 1:
                time.sleep(trigger_delay)

    return jobs


def _trigger_single(name, job_type, gangway_url, token, env_overrides,
                    max_retries, backoff):
    """Trigger a single Prow job and return a job dict.

    Args:
        name: Full Prow periodic job name.
        job_type: "blocking" or "informing" (used for logging and result type).
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        env_overrides: Dict of environment variables for the Prow job.
        max_retries: Maximum trigger attempts on retryable errors.
        backoff: Seconds to wait between retry attempts.

    Returns:
        Job dict with keys: name, type, job_id, url, result.
    """
    sn = short_name(name)
    print(f"\n--- [{job_type}] Triggering [{sn}] ---", flush=True)
    print(f"  Full name: {name}", flush=True)

    job_id = trigger_prow_job(gangway_url, token, name, env_overrides,
                              max_retries=max_retries, backoff=backoff)

    job = {
        "name": name,
        "type": job_type,
        "job_id": job_id,
        "url": "",
        "result": "pending" if job_id else "error"
    }

    if job_id:
        print(f"  Job ID: {job_id}", flush=True)
    else:
        print(f"  ERROR: Trigger failed after {max_retries} attempts", flush=True)

    return job


def resolve_all_urls(jobs, gangway_url, token, max_attempts=10, delay=15):
    """Resolve Prow UI URLs for all successfully triggered jobs.

    Skips jobs with job_id=None (trigger failures). Updates each job
    dict in-place with the resolved URL.

    Args:
        jobs: List of job dicts (as returned by trigger_all_jobs).
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        max_attempts: Maximum polling attempts per job for URL resolution.
        delay: Seconds to wait between polling attempts per job.
    """
    print("\n=== run-e2e: resolving Prow URLs ===", flush=True)
    for job in jobs:
        if job["job_id"] is None:
            continue
        url = resolve_prow_url(gangway_url, token, job["job_id"],
                               max_attempts=max_attempts, delay=delay)
        job["url"] = url
        sn = short_name(job["name"])
        if url:
            print(f"  [{job['type']}] [{sn}] {url}", flush=True)
        else:
            print(f"  [{job['type']}] [{sn}] WARN: could not resolve URL",
                  flush=True)


def poll_until_complete(jobs, gangway_url, token, initial_delay=2700,
                        poll_interval=600, poll_stagger=30, timeout=14400):
    """Poll all pending jobs until every job completes or the timeout expires.

    Waits initial_delay seconds before the first poll cycle. Each cycle
    iterates over pending jobs with poll_stagger seconds between individual
    status checks. Rate-limited responses (429/5xx) skip the job for the
    current cycle. Jobs still pending after the timeout are marked as error.

    Args:
        jobs: List of job dicts (as returned by trigger_all_jobs).
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        initial_delay: Seconds to wait before the first poll cycle
            (gives Prow time to schedule and start the jobs).
        poll_interval: Seconds to wait between poll cycles.
        poll_stagger: Seconds to wait between polling individual jobs
            within a single cycle (avoids Gangway rate limits).
        timeout: Total seconds (including initial_delay) after which
            remaining pending jobs are marked as error.
    """
    print(f"\nPoll interval: {poll_interval}s | Timeout: {timeout}s", flush=True)
    print(f"Initial delay: {initial_delay}s | Poll stagger: {poll_stagger}s",
          flush=True)

    print(f"\n=== run-e2e: waiting {initial_delay}s before first poll ===",
          flush=True)
    start_time = time.monotonic()
    time.sleep(initial_delay)

    print("=== run-e2e: polling all jobs ===", flush=True)
    poll_count = 0

    while (time.monotonic() - start_time) < timeout:
        pending = [j for j in jobs if j["result"] == "pending"]
        if not pending:
            break

        poll_count += 1
        elapsed = int(time.monotonic() - start_time)
        ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        print(f"\n[{ts}] Poll #{poll_count} ({elapsed}s)"
              f" - {len(pending)} job(s) pending", flush=True)

        poll_idx = 0
        for job in jobs:
            if job["result"] != "pending" or job["job_id"] is None:
                continue

            if poll_idx > 0:
                time.sleep(poll_stagger)
            poll_idx += 1

            sn = short_name(job["name"])
            mapped, raw = get_prow_job_status(gangway_url, token, job["job_id"])

            if mapped == "rate_limited":
                print(f"  [{job['type']}] [{sn}] HTTP {raw.split()[1] if ' ' in raw else raw}"
                      f" - rate limited, will retry next cycle", flush=True)
                continue
            elif mapped == "error":
                print(f"  [{job['type']}] [{sn}] {raw}", flush=True)
                job["result"] = "error"
                continue

            print(f"  [{job['type']}] [{sn}] {raw}", flush=True)

            if mapped == "passed":
                job["result"] = "passed"
                print(f"  [{job['type']}] [{sn}] PASSED", flush=True)
            elif mapped == "failed":
                job["result"] = "failed"
                print(f"  [{job['type']}] [{sn}] FAILED ({raw})", flush=True)

        pending_after = sum(1 for j in jobs if j["result"] == "pending")
        if pending_after == 0:
            break

        time.sleep(poll_interval)

    for job in jobs:
        if job["result"] == "pending":
            job["result"] = "error"
            sn = short_name(job["name"])
            print(f"  [{job['type']}] [{sn}] TIMEOUT", flush=True)


def print_run_summary(jobs):
    """Print a short results summary to stdout after polling completes.

    Groups results by type (blocking, informing) and prints each job's
    short name, result, and Prow URL.

    Args:
        jobs: List of job dicts (as returned by trigger_all_jobs).
    """
    print("\n=== run-e2e: results summary ===\n", flush=True)
    blocking = [j for j in jobs if j["type"] == "blocking"]
    informing = [j for j in jobs if j["type"] == "informing"]

    print("--- BLOCKING TESTS ---", flush=True)
    for j in blocking:
        sn = short_name(j["name"])
        print(f"  {sn}: {j['result']} ({j['url']})", flush=True)

    if informing:
        print("\n--- INFORMING TESTS ---", flush=True)
        for j in informing:
            sn = short_name(j["name"])
            print(f"  {sn}: {j['result']} ({j['url']})", flush=True)


def build_results_json(jobs):
    """Serialize job results into a compact JSON string for Tekton results.

    The output format is a JSON array of objects with keys:
    job (full name), result, url, type. Uses compact separators
    to minimize size (Tekton results have a 4KB limit).

    Args:
        jobs: List of job dicts (as returned by trigger_all_jobs).

    Returns:
        Compact JSON string, e.g. '[{"job":"...","result":"passed",...}]'.
    """
    results = []
    for job in jobs:
        results.append({
            "job": job["name"],
            "result": job["result"],
            "url": job["url"],
            "type": job["type"]
        })
    return json.dumps(results, separators=(",", ":"))


def evaluate_gate(results_json_str):
    """Evaluate the release gate verdict and print a formatted summary.

    Gate logic: all blocking tests must have result=="passed" (AND).
    Informing tests are reported but do not affect the verdict.
    Prints a formatted table of results and the verdict to stdout.

    Args:
        results_json_str: JSON string as produced by build_results_json.

    Returns:
        True if the gate passed (all blocking tests passed), False otherwise.
        Returns False if results_json_str is not valid JSON.
    """
    try:
        results = json.loads(results_json_str)
    except (json.JSONDecodeError, TypeError):
        print("ERROR: results JSON is invalid or truncated, failing gate",
              flush=True)
        return False

    blocking = [r for r in results if r["type"] == "blocking"]
    informing = [r for r in results if r["type"] == "informing"]

    if not blocking:
        print("ERROR: no blocking tests found in results"
              " - at least one blocking test is required", flush=True)
        return False

    print(f"--- BLOCKING TESTS ({len(blocking)}) ---", flush=True)
    print(f"{'JOB':<50} {'RESULT':<10} URL", flush=True)
    print(f"{'---':<50} {'------':<10} ---", flush=True)

    gate_passed = True
    for r in blocking:
        job = short_name(r["job"])
        print(f"{job:<50} {r['result']:<10} {r.get('url', '')}", flush=True)
        if r["result"] != "passed":
            gate_passed = False

    if informing:
        print(f"\n--- INFORMING TESTS ({len(informing)}) ---", flush=True)
        print(f"{'JOB':<50} {'RESULT':<10} URL", flush=True)
        print(f"{'---':<50} {'------':<10} ---", flush=True)
        warnings = 0
        for r in informing:
            job = short_name(r["job"])
            print(f"{job:<50} {r['result']:<10} {r.get('url', '')}", flush=True)
            if r["result"] != "passed":
                warnings += 1
        if warnings > 0:
            print(f"\nWARN: {warnings} informing test(s) failed"
                  " (does not affect gate verdict)", flush=True)

    verdict = "true" if gate_passed else "false"
    print(f"\nGate verdict: {verdict} ({len(blocking)} blocking, AND logic)",
          flush=True)

    if gate_passed:
        print("\n=== GATE PASSED - create-release will create the Release CR ===",
              flush=True)
    else:
        print("\n=== GATE FAILED - image will NOT be promoted ===", flush=True)

    return gate_passed


def build_gate_notification(gate_passed, gate_label, results_json_str,
                            release_name, snapshot, pipeline_run,
                            konflux_base_url):
    """Build the Slack Block Kit payload for a gate result notification.

    Handles three visual states:
    - Green (#2E7D32): gate passed and Release CR was created.
    - Orange (#F57C00): gate passed but Release creation failed.
    - Red (#D32F2F): gate failed (one or more blocking tests failed).

    Args:
        gate_passed: Boolean gate verdict from evaluate_gate.
        gate_label: Human-readable gate label (e.g. "ARO HCP").
        results_json_str: JSON string as produced by build_results_json.
        release_name: Name of the created Release CR, or "N/A" if none.
        snapshot: Konflux Snapshot name.
        pipeline_run: Tekton PipelineRun name.
        konflux_base_url: Base URL of the Konflux UI for building links.

    Returns:
        Dict payload ready to be passed to send_slack_message.
    """
    if release_name and release_name != "N/A":
        release_field = (f"<{konflux_base_url}/releases/{release_name}/"
                         f"|{release_name}>")
    else:
        release_field = "N/A"

    if gate_passed and (release_name == "N/A" or not release_name):
        color = "#F57C00"
        header = (f":warning: *HO Release Gate [{gate_label}] PASSED"
                  f" - Release creation failed*")
    elif gate_passed:
        color = "#2E7D32"
        header = f":white_check_mark: *HO Release Gate [{gate_label}] PASSED*"
    else:
        color = "#D32F2F"
        header = f":x: *HO Release Gate [{gate_label}] FAILED*"

    result_lines = _format_test_results(results_json_str)

    pr_link = (f"<{konflux_base_url}/pipelineruns/{pipeline_run}/"
               f"|{pipeline_run}>")

    blocks = [
        mrkdwn_section(header),
        fields_section([
            f"*Gate:*\n{gate_label}",
            f"*Snapshot:*\n{snapshot}",
            f"*PipelineRun:*\n{pr_link}",
            f"*Release:*\n{release_field}"
        ]),
        divider(),
        mrkdwn_section(result_lines)
    ]

    return build_slack_payload(color, blocks)


def _format_test_results(results_json_str):
    """Format test results as Slack mrkdwn text for the notification body.

    Groups results by type (blocking, informing) with emoji indicators
    and Prow links.

    Args:
        results_json_str: JSON string as produced by build_results_json.

    Returns:
        Slack mrkdwn string with newline-separated result lines.
    """
    try:
        results = json.loads(results_json_str)
    except (json.JSONDecodeError, TypeError):
        return ("\n:x: Pipeline failed before tests started."
                " Check PipelineRun logs.")

    def _format_group(group, header):
        if not group:
            return []
        parts = [f"\n*{header}:*"]
        for r in group:
            job = short_name(r["job"])
            emoji = (":white_check_mark:" if r["result"] == "passed"
                     else ":red_circle:")
            url = r.get("url", "")
            if url:
                parts.append(f"{emoji} `{job}` - {r['result']} (<{url}|Prow>)")
            else:
                parts.append(f"{emoji} `{job}` - {r['result']}")
        return parts

    parts = []
    parts.extend(_format_group(
        [r for r in results if r["type"] == "blocking"], "Blocking Tests"))
    parts.extend(_format_group(
        [r for r in results if r["type"] == "informing"], "Informing Tests"))

    return "\n".join(parts) if parts else "\n:x: No test results available."


def build_error_notification(pipeline_run, konflux_base_url, gate_label):
    """Build the Slack Block Kit payload for a pipeline DAG failure.

    Used by the notify-slack-error finally task, which fires only when
    create-release was never reached (status=None). Always red (#D32F2F).

    Args:
        pipeline_run: Tekton PipelineRun name.
        konflux_base_url: Base URL of the Konflux UI for building links.
        gate_label: Human-readable gate label (e.g. "ARO HCP").

    Returns:
        Dict payload ready to be passed to send_slack_message.
    """
    pr_link = (f"<{konflux_base_url}/pipelineruns/{pipeline_run}/"
               f"|{pipeline_run}>")

    blocks = [
        mrkdwn_section(
            f":x: *HO Release Gate [{gate_label}] PIPELINE ERROR*\n"
            "Pipeline failed before reaching the evaluation phase."
            " Check PipelineRun logs."
        ),
        mrkdwn_section(f"*PipelineRun:*\n{pr_link}")
    ]

    return build_slack_payload("#D32F2F", blocks)


def check_failure_streak(runs):
    """Extract the consecutive failure streak from a list of PipelineRuns.

    Iterates from the most recent run forward, collecting runs whose
    ``status`` field is False. Stops at the first successful run.

    Args:
        runs: List of run dicts from fetch_pipelineruns, sorted
            most-recent-first. Each dict has keys: name, created,
            status, reason.

    Returns:
        List of consecutive failed run dicts (most-recent-first).
        Empty if the most recent run succeeded or if runs is empty.
    """
    streak = []
    for run in runs:
        if run.get("status", False):
            break
        streak.append(run)
    return streak


def build_stale_notification(streak, gate_label, threshold_days,
                             current_run_name, konflux_base_url,
                             streak_days):
    """Build the Slack Block Kit payload for a stale promotion alert.

    Replaces the normal failure notification when the failure streak
    exceeds the configured threshold. Includes links to the current
    PipelineRun and all historical failed runs in the streak.

    Args:
        streak: List of consecutive failed run dicts from
            check_failure_streak (most-recent-first). Each dict has
            keys: name, created, status, reason.
        gate_label: Human-readable gate label (e.g. "ARO HCP").
        threshold_days: Configured stale threshold in days.
        current_run_name: Name of the currently running PipelineRun.
        konflux_base_url: Base URL of the Konflux UI for building links.
        streak_days: Number of days spanned by the failure streak
            (pre-calculated by the caller).

    Returns:
        Dict payload ready to be passed to send_slack_message.
    """
    total_runs = len(streak) + 1
    today_str = datetime.now(timezone.utc).strftime("%Y-%m-%d")

    pr_link = build_pipelinerun_url(current_run_name, konflux_base_url)

    header = (
        f":alert-siren: *HO Release Gate"
        f" - Stale Promotion Alert [{gate_label}]*\n"
        f"No successful promotion in the last {streak_days} day(s)"
        f" ({total_runs} consecutive failed run(s))."
    )

    max_displayed = 10  # Slack mrkdwn_section text limit is 3000 chars
    history_lines = [
        f"`{today_str}` <{pr_link}|{current_run_name}>"
        f" (current)"
    ]
    displayed = streak[:max_displayed]
    remaining = len(streak) - len(displayed)

    for run in displayed:
        run_date = run["created"][:10] if run["created"] else "N/A"
        run_link = build_pipelinerun_url(run["name"], konflux_base_url)
        history_lines.append(
            f"`{run_date}` <{run_link}|{run['name']}>:"
            f" {run['reason']}"
        )

    if remaining > 0:
        oldest_date = streak[-1]["created"][:10] if streak[-1]["created"] else "N/A"
        history_lines.append(
            f"... and {remaining} other failed"
            f" run(s) since {oldest_date}"
        )

    blocks = [
        mrkdwn_section(header),
        fields_section([
            f"*Gate:*\n{gate_label}",
            f"*Threshold:*\n{threshold_days} day(s)",
            f"*Streak:*\n{streak_days} day(s)",
            f"*Current PipelineRun:*\n<{pr_link}|{current_run_name}>",
        ]),
        divider(),
        mrkdwn_section("\n".join(history_lines)),
        divider(),
        mrkdwn_section(
            "Check the failing tests for persistent infrastructure"
            " issues or test regressions. If the streak continues,"
            " consider investigating the promotion pipeline"
            " and escalating."
        ),
    ]

    return build_slack_payload("#D32F2F", blocks)


def check_and_build_stale_payload(token, its_scenario, threshold_days,
                                  current_run_name, konflux_base_url,
                                  namespace, gate_label):
    """Check for a stale promotion streak and build the alert payload.

    Queries KubeArchive for historical PipelineRuns matching the ITS
    scenario label, evaluates the consecutive failure streak in days,
    and returns a stale alert payload if the streak exceeds the
    threshold. Called by both notify-slack and notify-slack-error
    inline scripts.

    Args:
        token: Bearer token for KubeArchive authentication.
        its_scenario: Integration Test Scenario name used as the
            KubeArchive label selector value.
        threshold_days: Number of consecutive failure days before
            triggering the stale alert.
        current_run_name: Name of the currently running PipelineRun.
        konflux_base_url: Base URL of the Konflux UI for building links.
        namespace: Kubernetes namespace for the KubeArchive query.
        gate_label: Human-readable gate label (e.g. "ARO HCP").

    Returns:
        Dict payload for send_slack_message if stale (streak days >=
        threshold), or None if the streak does not meet the threshold.
    """
    label_selector = (
        f"test.appstudio.openshift.io/scenario={its_scenario}")

    print("\n=== Stale promotion check ===", flush=True)
    print(f"  ITS scenario:    {its_scenario}", flush=True)
    print(f"  Label selector:  {label_selector}", flush=True)
    print(f"  Threshold:       {threshold_days} day(s)", flush=True)

    runs = fetch_pipelineruns(token, namespace, label_selector)
    runs = [r for r in runs if r["name"] != current_run_name]
    if not runs:
        print("  No historical runs found - skipping stale check",
              flush=True)
        return None

    streak = check_failure_streak(runs)
    if not streak:
        print("  Most recent run succeeded - no failure streak",
              flush=True)
        return None

    oldest_created = streak[-1]["created"]
    if not oldest_created:
        print("  WARN: oldest run has no creation timestamp"
              " - skipping stale check", flush=True)
        return None

    oldest_dt = datetime.fromisoformat(
        oldest_created.replace("Z", "+00:00"))
    streak_days = (datetime.now(timezone.utc) - oldest_dt).days + 1

    print(f"  Failure streak: {len(streak)} run(s)"
          f" over {streak_days} day(s)", flush=True)

    if streak_days < threshold_days:
        print(f"  Streak ({streak_days}d) < threshold"
              f" ({threshold_days}d) - normal notification",
              flush=True)
        return None

    print(f"  Streak ({streak_days}d) >= threshold"
          f" ({threshold_days}d) - STALE ALERT", flush=True)

    return build_stale_notification(
        streak, gate_label, threshold_days,
        current_run_name, konflux_base_url, streak_days)
