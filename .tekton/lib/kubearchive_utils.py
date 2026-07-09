"""KubeArchive REST API utilities (stdlib only)."""

import json
from datetime import datetime, timezone, timedelta
from urllib.parse import quote

from http_utils import http_request_with_retry

__all__ = ["fetch_pipelineruns", "build_pipelinerun_url"]

KUBEARCHIVE_API_BASE = (
    "https://kubearchive-api-server-product-kubearchive"
    ".apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
)

# KubeArchive does not guarantee server-side sort order, so the first
# page of results may contain arbitrarily old runs rather than the most
# recent ones.  We use a time filter + high limit to ensure all recent
# runs fit in a single response without pagination.
# Stale promotion is a condition that should be resolved within days,
# so 60 days is deliberately oversized to never cut off relevant data.
STALE_LOOKBACK_DAYS = 60
STALE_QUERY_LIMIT = STALE_LOOKBACK_DAYS * 2  # ~1 run/day + margin for retriggers


def fetch_pipelineruns(token, namespace, label_selector):
    """Fetch PipelineRun records from the KubeArchive REST API.

    Queries the KubeArchive API for PipelineRuns matching the given
    label selector in the specified namespace. Returns a simplified
    list of dicts sorted by creation time (most recent first).

    Args:
        token: Bearer token for KubeArchive authentication
            (typically from ``oc whoami -t``).
        namespace: Kubernetes namespace to query.
        label_selector: Kubernetes label selector string
            (e.g. ``test.appstudio.openshift.io/scenario=my-its``).

    Returns:
        List of dicts with keys: name, created, status, reason.
        ``created`` is an ISO 8601 string from creationTimestamp.
        ``status`` is a boolean derived from the condition's status
        field (True when the Kubernetes condition status=="True").
        ``reason`` is the condition reason string for display
        (e.g. "Completed", "Failed", "PipelineRunTimeout").
        Sorted most-recent-first.
        Returns an empty list on any error (non-200, parse failure).
    """
    cutoff = (datetime.now(timezone.utc)
              - timedelta(days=STALE_LOOKBACK_DAYS))
    cutoff_ts = cutoff.strftime("%Y-%m-%dT00:00:00Z")

    url = (f"{KUBEARCHIVE_API_BASE}/apis/tekton.dev/v1"
           f"/namespaces/{namespace}"
           f"/pipelineruns?labelSelector={quote(label_selector)}"
           f"&limit={STALE_QUERY_LIMIT}"
           f"&creationTimestampAfter={quote(cutoff_ts)}")

    headers = {"Authorization": f"Bearer {token}"}

    print(f"  KubeArchive: fetching PipelineRuns"
          f" (namespace={namespace})", flush=True)
    print(f"  Label selector: {label_selector}", flush=True)

    status, body = http_request_with_retry(
        url, headers=headers, retries=3, backoff=10, timeout=30)

    if status != 200:
        print(f"  WARN: KubeArchive returned HTTP {status}"
              f" - skipping stale check", flush=True)
        return []

    try:
        data = json.loads(body)
    except (json.JSONDecodeError, TypeError) as e:
        print(f"  WARN: KubeArchive response is not valid JSON: {e}"
              f" - skipping stale check", flush=True)
        return []

    items = data.get("items", [])
    runs = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        created = item.get("metadata", {}).get("creationTimestamp", "")
        conditions = (item.get("status", {})
                      .get("conditions", []))
        last_cond = conditions[-1] if conditions else {}
        if not isinstance(last_cond, dict):
            last_cond = {}
        reason = last_cond.get("reason", "Unknown")
        cond_status = last_cond.get("status", "False")

        runs.append({"name": name, "created": created,
                     "status": cond_status == "True", "reason": reason})

    runs.sort(key=lambda r: r["created"], reverse=True)

    print(f"  KubeArchive: found {len(runs)} PipelineRun(s)", flush=True)
    return runs


def build_pipelinerun_url(pipelinerun_name, konflux_base_url):
    """Build a Konflux UI URL for a PipelineRun.

    Args:
        pipelinerun_name: Name of the Tekton PipelineRun.
        konflux_base_url: Base URL of the Konflux UI application page
            (e.g. ``https://konflux-ui.../ns/.../applications/...``).

    Returns:
        Full URL string pointing to the PipelineRun in the Konflux UI.
    """
    return f"{konflux_base_url}/pipelineruns/{pipelinerun_name}/"
