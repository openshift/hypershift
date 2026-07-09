"""Prow / Gangway API utilities (stdlib only)."""

import json
import re
import time
from http_utils import http_request, http_request_with_retry

__all__ = ["short_name", "trigger_prow_job",
           "resolve_prow_url", "get_prow_job_status"]

_JOB_RE = re.compile(
    r"periodic-ci-openshift-hypershift-release-(\d+\.\d+)-periodics-(.*)")


def short_name(job_name):
    """Extract a compact version/test label from a Prow job name.

    Args:
        job_name: Full Prow periodic job name.

    Returns:
        ``"<version>/<test>"`` (e.g. ``"5.0/e2e-aks"``), or the
        original name if the pattern does not match.
    """
    m = _JOB_RE.match(job_name)
    if m:
        return f"{m.group(1)}/{m.group(2)}"
    return job_name


def trigger_prow_job(gangway_url, token, job_name, env_overrides,
                     max_retries=3, backoff=120):
    """Trigger a single Prow periodic job via the Gangway API.

    Sends a POST to Gangway with the job name and environment variable
    overrides. Retries on 429 and 5xx responses.

    Args:
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        job_name: Full Prow periodic job name to trigger.
        env_overrides: Dict of environment variables injected into the
            Prow job's PodSpec (e.g. image overrides).
        max_retries: Maximum trigger attempts on retryable errors.
        backoff: Seconds to wait between retry attempts.

    Returns:
        Job ID string on success, or None if the trigger failed.
    """
    payload = {
        "job_name": job_name,
        "job_execution_type": "1",
        "pod_spec_options": {
            "envs": env_overrides
        }
    }

    headers = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json"
    }

    def should_retry(status):
        return status == 0 or status == 429 or 500 <= status < 600

    status, body = http_request_with_retry(
        gangway_url, method="POST", headers=headers, body=payload,
        retries=max_retries, backoff=backoff, should_retry=should_retry
    )

    if status != 200:
        print(f"  ERROR: Trigger failed - HTTP {status}", flush=True)
        if body:
            print(f"  Raw response: {body}", flush=True)
        return None

    try:
        data = json.loads(body)
    except (json.JSONDecodeError, TypeError):
        print("  ERROR: Response is not valid JSON", flush=True)
        print(f"  Raw response: {body}", flush=True)
        return None

    job_id = data.get("id")
    if job_id is None:
        print("  ERROR: Failed to parse job ID", flush=True)
        print(f"  Raw response: {body}", flush=True)
        return None

    return str(job_id)


def resolve_prow_url(gangway_url, token, job_id, max_attempts=10, delay=15):
    """Resolve the Prow UI URL for a triggered job.

    Polls the Gangway execution endpoint until the job_url field is
    populated, or max_attempts is reached.

    Args:
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        job_id: Job execution ID returned by trigger_prow_job.
        max_attempts: Maximum number of polling attempts.
        delay: Seconds to wait between polling attempts.

    Returns:
        Prow URL string, or empty string if resolution failed.
    """
    headers = {"Authorization": f"Bearer {token}"}

    for attempt in range(1, max_attempts + 1):
        status, body = http_request(
            f"{gangway_url}/{job_id}", method="GET", headers=headers
        )
        if status == 200:
            try:
                data = json.loads(body)
                url = data.get("job_url", "")
                if url:
                    return url
            except (json.JSONDecodeError, TypeError):
                pass
        elif status in (401, 403):
            print(f"  WARN: URL resolve got HTTP {status} - auth error, "
                  f"aborting", flush=True)
            return ""
        elif status != 0:
            print(f"  WARN: URL resolve got HTTP {status} "
                  f"(attempt {attempt}/{max_attempts})", flush=True)
        if attempt < max_attempts:
            time.sleep(delay)

    return ""


def get_prow_job_status(gangway_url, token, job_id):
    """Get current status of a single Prow job execution.

    Performs a single GET to the Gangway execution endpoint (no retry).
    Maps the Gangway job_status to a simplified status string.

    Args:
        gangway_url: Base URL of the Gangway executions endpoint.
        token: Bearer token for Gangway authentication.
        job_id: Job execution ID returned by trigger_prow_job.

    Returns:
        Tuple (mapped_status, raw_info) where:
        - mapped_status: one of "pending", "passed", "failed",
          "error", "rate_limited"
        - raw_info: the raw Gangway job_status string (e.g. "SUCCESS",
          "FAILURE", "TRIGGERED") or an error description
    """
    headers = {"Authorization": f"Bearer {token}"}

    status, body = http_request(
        f"{gangway_url}/{job_id}", method="GET", headers=headers
    )

    if status == 0 or status == 429 or (500 <= status < 600):
        return "rate_limited", f"HTTP {status}"

    if status != 200:
        msg = f"HTTP {status} - auth error or unexpected failure"
        if body:
            print(f"  Raw response: {body}", flush=True)
        return "error", msg

    try:
        data = json.loads(body)
    except (json.JSONDecodeError, TypeError):
        return "error", "response is not valid JSON"

    raw = data.get("job_status", "")

    if raw == "SUCCESS":
        return "passed", raw
    elif raw in ("FAILURE", "ABORTED", "ERROR"):
        return "failed", raw
    else:
        return "pending", raw
