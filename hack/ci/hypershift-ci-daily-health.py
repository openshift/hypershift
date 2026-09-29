#!/usr/bin/env python3
"""Build the deterministic portions of the HyperShift CI daily health report.

The CLI deliberately separates data collection from LLM judgment:

* ``collect`` reads only allow-listed public endpoints and writes a compact
  stage-one Slack message plus a bounded, schema-versioned candidate document.
* ``render`` accepts human/LLM judgments for exactly those candidate IDs and
  renders bounded replies for the same Slack thread.

The collector identifies evidence that needs judgment; it never declares a
test permafailing or flaky, infers a root cause, or writes to Jira.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import datetime as dt
import hashlib
import html
import http.client
import json
import math
import os
import re
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import defaultdict
from collections.abc import Iterable
from pathlib import Path
from typing import Any

SCHEMA_VERSION = 1
REPOSITORY = "openshift/hypershift"
DASHBOARD_BASE = (
    "https://hypershift-ci-health.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com"
)
RELEASE_CONTROLLER_BASE = "https://amd64.ocp.releases.ci.openshift.org"
MULTI_RELEASE_CONTROLLER_BASE = "https://multi.ocp.releases.ci.openshift.org"
SIPPY_RUNS_URL = "https://sippy.dptools.openshift.org/api/jobs/runs"
PROW_HOST = "prow.ci.openshift.org"
PUBLIC_RESULTS_BUCKET = "test-platform-results-public"
MAX_HTTP_BYTES = 8 * 1024 * 1024
MAX_SIPPY_ROWS = 2500
MAX_HISTORY_PAGES = 6
MAX_CANDIDATES = 100
MAX_SAMPLE_RUNS = 6
MAX_SOURCE_FAILURES = 100
MAX_WORKERS = 8
MAX_COLLECTION_REQUESTS = 500
COLLECTION_DEADLINE_SECONDS = 120.0
MAX_SOURCE_JOBS = 5000
MAX_SOURCE_STRING = 2000
MAX_CANDIDATE_STRING = 1000
MAX_CANDIDATE_DOCUMENT_BYTES = 512 * 1024
MAX_PROW_DURATION_NS = 7 * 24 * 60 * 60 * 1_000_000_000
MAX_METRIC_COUNT = 1_000_000_000
STAGE_ONE_LIMIT = 1999
THREAD_REPLY_LIMIT = 3900

ALLOWED_HOSTS = {
    urllib.parse.urlparse(DASHBOARD_BASE).hostname,
    urllib.parse.urlparse(RELEASE_CONTROLLER_BASE).hostname,
    urllib.parse.urlparse(MULTI_RELEASE_CONTROLLER_BASE).hostname,
    urllib.parse.urlparse(SIPPY_RUNS_URL).hostname,
    PROW_HOST,
    "storage.googleapis.com",
    "openshift-release.apps.ci.l2s4.p1.openshiftapps.com",
}

RESULT_STATES = {
    "S": "SUCCESS",
    "SUCCESS": "SUCCESS",
    "F": "FAILURE",
    "FAILURE": "FAILURE",
    "N": "ERROR",
    "ERROR": "ERROR",
    "A": "ABORTED",
    "ABORTED": "ABORTED",
}

PRESUBMIT_JUDGMENTS = {
    "not_permafailing",
    "flaky",
    "permafail_candidate",
    "infrastructure_triage",
    "one_off_failure",
    "no_data",
}
PERIODIC_JUDGMENTS = {
    "flaky",
    "infrastructure_triage",
    "one_off_failure",
    "incident_candidate",
    "payload_impact_unknown",
    "no_data",
}
KIND_JUDGMENTS = {
    "presubmit": PRESUBMIT_JUDGMENTS,
    "periodic": PERIODIC_JUDGMENTS,
}
TRACKING_KEY_RE = re.compile(r"^(?:OCPBUGS|CNTRLPLANE)-[1-9][0-9]*$")
JOB_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$")
PAYLOAD_TAG_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$")
PHASE_RE = re.compile(r"^[A-Za-z][A-Za-z0-9 ._-]{0,63}$")
CANDIDATE_ID_RE = re.compile(r"^hci-[0-9a-f]{16}$")
COLLECTION_ID_RE = re.compile(r"^hci-doc-[0-9a-f]{16}$")
RELEASE_RE = re.compile(r"^[0-9]+\.[0-9]+$")
BRANCH_RE = re.compile(r"^(?:main|release-[0-9]+\.[0-9]+)$")
GIT_SHA_RE = re.compile(r"^[0-9a-f]{40}$")
RFC3339_UTC_RE = re.compile(
    r"^(?P<date>[0-9]{4}-[0-9]{2}-[0-9]{2})T"
    r"(?P<time>[0-9]{2}:[0-9]{2}:[0-9]{2})"
    r"(?P<fraction>\.[0-9]{1,9})?Z$"
)
STREAM_LINK_RE = re.compile(r'href=["\']/releasestream/([^"\'/]+)')
ALL_BUILDS_RE = re.compile(r"var\s+allBuilds\s*=\s*(\[.*?\]);\s*</script>", re.DOTALL)
OLDER_LINK_RE = re.compile(r'href="([^"]+\?buildId=\d+)">&lt;- Older Runs')


class ReportError(RuntimeError):
    """A fail-closed data or validation error."""


class NoDataError(ReportError):
    """An expected, explicit absence of public history."""


def _parse_url(url: str) -> urllib.parse.ParseResult:
    try:
        parsed = urllib.parse.urlparse(url)
        # Accessing hostname performs additional validation and can raise.
        _ = parsed.hostname
    except (TypeError, ValueError) as exc:
        raise ReportError("refusing malformed URL") from exc
    return parsed


def _bounded_string(value: Any, label: str, limit: int = MAX_SOURCE_STRING) -> str:
    if not isinstance(value, str) or not value or len(value) > limit:
        raise ReportError(f"{label} must be a non-empty string of at most {limit} characters")
    if any(character in value for character in ("\r", "\n", "\x00")):
        raise ReportError(f"{label} contains a control character")
    return value


def _grammar_string(value: Any, label: str, pattern: re.Pattern[str]) -> str:
    text = _bounded_string(value, label, 255)
    if not pattern.fullmatch(text):
        raise ReportError(f"{label} has an invalid format")
    return text


def _is_missing_prow_history(status: int, body: bytes) -> bool:
    if status not in {404, 500}:
        return False
    text = body.decode("utf-8", errors="replace").lower()
    return "latest-build.txt" in text and (
        "object doesn't exist" in text or "no such object" in text
    )


class ValidatingRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Validate every redirect target before urllib follows it."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        validate_public_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


class HTTPTransport:
    """Small bounded HTTP client restricted to the report's public hosts."""

    def __init__(self, timeout: float = 20.0, max_bytes: int = MAX_HTTP_BYTES):
        self.timeout = timeout
        self.max_bytes = max_bytes
        self.deadline: float | None = None
        self.requests_remaining = MAX_COLLECTION_REQUESTS
        self._limit_lock = threading.Lock()
        self.opener = urllib.request.build_opener(ValidatingRedirectHandler())

    def configure_collection_limits(
        self, deadline_seconds: float, max_requests: int
    ) -> None:
        self.deadline = time.monotonic() + deadline_seconds
        self.requests_remaining = max_requests

    def _request_timeout(self) -> float:
        with self._limit_lock:
            if self.requests_remaining <= 0:
                raise ReportError("public request budget exhausted")
            self.requests_remaining -= 1
        remaining = (
            self.deadline - time.monotonic() if self.deadline is not None else self.timeout
        )
        if remaining <= 0:
            raise ReportError("collection deadline exhausted")
        return min(self.timeout, remaining)

    def get_text(self, url: str) -> str:
        validate_public_url(url)
        request = urllib.request.Request(
            url,
            headers={"User-Agent": "hypershift-ci-daily-health/1"},
        )
        try:
            with self.opener.open(request, timeout=self._request_timeout()) as response:
                validate_public_url(response.geturl())
                data = response.read(self.max_bytes + 1)
        except urllib.error.HTTPError as exc:
            try:
                body = exc.read(self.max_bytes + 1)
            except (http.client.HTTPException, OSError, TimeoutError) as read_exc:
                raise ReportError(
                    f"GET failed while reading {safe_url_label(url)}: {read_exc}"
                ) from read_exc
            if _is_missing_prow_history(exc.code, body):
                raise NoDataError("Prow history has never run") from exc
            raise ReportError(f"GET failed for {safe_url_label(url)}: {exc}") from exc
        except (
            urllib.error.URLError,
            http.client.HTTPException,
            OSError,
            TimeoutError,
        ) as exc:
            raise ReportError(f"GET failed for {safe_url_label(url)}: {exc}") from exc
        if len(data) > self.max_bytes:
            raise ReportError(f"response too large from {safe_url_label(url)}")
        try:
            return data.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ReportError(f"non-UTF-8 response from {safe_url_label(url)}") from exc

    def get_json(self, url: str) -> Any:
        try:
            return json.loads(self.get_text(url))
        except json.JSONDecodeError as exc:
            raise ReportError(f"invalid JSON from {safe_url_label(url)}") from exc


class FixtureTransport:
    """Offline transport backed by a regex-to-file manifest."""

    def __init__(self, directory: Path):
        self.directory = directory
        try:
            manifest = json.loads((directory / "manifest.json").read_text())
        except (OSError, json.JSONDecodeError) as exc:
            raise ReportError(f"invalid fixture manifest in {directory}") from exc
        self.entries = []
        self.deadline: float | None = None
        self.requests_remaining = MAX_COLLECTION_REQUESTS
        self._limit_lock = threading.Lock()
        for item in manifest.get("responses", []):
            if not isinstance(item, dict):
                raise ReportError("fixture manifest responses must be objects")
            self.entries.append((re.compile(item["pattern"]), item))

    def configure_collection_limits(
        self, deadline_seconds: float, max_requests: int
    ) -> None:
        self.deadline = time.monotonic() + deadline_seconds
        self.requests_remaining = max_requests

    def _before_request(self) -> None:
        with self._limit_lock:
            if self.requests_remaining <= 0:
                raise ReportError("public request budget exhausted")
            self.requests_remaining -= 1
        if self.deadline is not None and time.monotonic() >= self.deadline:
            raise ReportError("collection deadline exhausted")

    def get_text(self, url: str) -> str:
        validate_public_url(url)
        self._before_request()
        for pattern, item in self.entries:
            if pattern.search(url):
                status = int(item.get("status", 200))
                if status < 200 or status >= 300:
                    body = b""
                    if item.get("file"):
                        try:
                            body = (self.directory / item["file"]).read_bytes()
                        except OSError as exc:
                            raise ReportError(f"missing fixture {item['file']}") from exc
                    if _is_missing_prow_history(status, body):
                        raise NoDataError("Prow history has never run")
                    raise ReportError(
                        f"GET failed for {safe_url_label(url)}: HTTP {status}"
                    )
                try:
                    return (self.directory / item["file"]).read_text()
                except OSError as exc:
                    raise ReportError(f"missing fixture {item['file']}") from exc
        raise ReportError(f"no offline fixture for {safe_url_label(url)}")

    def get_json(self, url: str) -> Any:
        try:
            return json.loads(self.get_text(url))
        except json.JSONDecodeError as exc:
            raise ReportError(
                f"invalid fixture JSON for {safe_url_label(url)}"
            ) from exc


def validate_public_url(url: str) -> None:
    if not isinstance(url, str) or len(url) > MAX_SOURCE_STRING:
        raise ReportError("URL must be a bounded string")
    if any(character in url for character in ("\r", "\n", "\x00", "`", "<", ">", "|")):
        raise ReportError("refusing URL with Slack/control delimiters")
    parsed = _parse_url(url)
    if parsed.scheme != "https" or parsed.hostname not in ALLOWED_HOSTS:
        raise ReportError(
            f"refusing non-public or unknown host: {parsed.hostname or '<missing>'}"
        )
    if parsed.username or parsed.password or parsed.fragment:
        raise ReportError("refusing URL with credentials or fragment")
    try:
        port = parsed.port
    except ValueError as exc:
        raise ReportError("refusing URL with invalid port") from exc
    if port not in {None, 443}:
        raise ReportError("refusing URL with non-HTTPS port")
    path = parsed.path or "/"
    allowed_path = False
    if parsed.hostname == urllib.parse.urlparse(DASHBOARD_BASE).hostname:
        allowed_path = path in {
            "/api/job-registry",
            "/api/schemas/Registry.json",
            "/_dashboard/health/windows/1w",
            "/_dashboard/health/windows/2w",
            "/_dashboard/health/windows/1m",
        }
    elif parsed.hostname in {
        urllib.parse.urlparse(RELEASE_CONTROLLER_BASE).hostname,
        urllib.parse.urlparse(MULTI_RELEASE_CONTROLLER_BASE).hostname,
    }:
        allowed_path = path == "/" or path == "/api/v1/releasestreams/all" or path.startswith(
            ("/api/v1/releasestream/", "/releasestream/")
        )
    elif parsed.hostname == urllib.parse.urlparse(SIPPY_RUNS_URL).hostname:
        allowed_path = path == "/api/jobs/runs" or path.startswith("/sippy-ng/")
    elif parsed.hostname == PROW_HOST:
        allowed_path = path.startswith("/job-history/gs/test-platform-results/") or bool(
            re.match(
                r"^/view/gs/(?:test-platform-results|test-platform-results-public)/",
                path,
            )
        )
    elif parsed.hostname == "openshift-release.apps.ci.l2s4.p1.openshiftapps.com":
        allowed_path = path.startswith("/releasestream/")
    if parsed.hostname == "storage.googleapis.com":
        decoded_path = urllib.parse.unquote(parsed.path)
        segments = [part for part in decoded_path.split("/") if part]
        if any(part in {".", ".."} for part in segments) or "\\" in decoded_path:
            raise ReportError("refusing noncanonical Prow results path")
        if not segments or segments[0] != PUBLIC_RESULTS_BUCKET:
            raise ReportError("refusing non-public Prow results bucket")
        allowed_path = True
    if not allowed_path:
        raise ReportError(f"refusing unexpected public-source path: {path}")


def safe_url_label(url: str) -> str:
    try:
        parsed = _parse_url(url)
    except ReportError:
        return "<malformed URL>"
    return f"{parsed.hostname}{parsed.path}"


def parse_rfc3339(value: str) -> dt.datetime:
    if not isinstance(value, str) or not RFC3339_UTC_RE.fullmatch(value):
        raise ReportError("timestamp must be canonical RFC3339 UTC")
    match = RFC3339_UTC_RE.fullmatch(value)
    assert match is not None
    fraction = match.group("fraction") or ""
    if fraction:
        fraction = "." + fraction[1:].ljust(6, "0")[:6]
    normalized = f"{match.group('date')}T{match.group('time')}{fraction}+00:00"
    try:
        result = dt.datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise ReportError(f"invalid RFC3339 timestamp: {value}") from exc
    return result.astimezone(dt.timezone.utc)


def format_rfc3339(value: dt.datetime) -> str:
    return (
        value.astimezone(dt.timezone.utc)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z")
    )


def release_key(value: str) -> tuple[int, int]:
    match = re.fullmatch(r"(\d+)\.(\d+)", value or "")
    if not match:
        raise ReportError(f"invalid release major/minor: {value!r}")
    return int(match.group(1)), int(match.group(2))


def payload_release(tag: str) -> str:
    match = re.match(r"^(\d+\.\d+)\.", tag or "")
    if not match:
        raise ReportError(f"payload tag does not begin with major.minor: {tag!r}")
    return match.group(1)


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(content)
        os.replace(temporary, path)
    except Exception:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def validate_registry(registry: Any) -> list[dict[str, Any]]:
    if not isinstance(registry, dict) or not isinstance(registry.get("jobs"), list):
        raise ReportError("dashboard registry jobs is not a list")
    jobs = registry["jobs"]
    if len(jobs) > MAX_SOURCE_JOBS:
        raise ReportError(f"dashboard registry exceeds {MAX_SOURCE_JOBS} jobs")
    for index, job in enumerate(jobs):
        if not isinstance(job, dict):
            raise ReportError(f"dashboard registry job {index} is not an object")
        job_id = _grammar_string(job.get("id"), f"registry job {index} id", JOB_ID_RE)
        for field in ("repository", "type"):
            if field in job and not isinstance(job[field], str):
                raise ReportError(f"registry {job_id} {field} must be a string")
        platforms = job.get("platforms") or []
        if not isinstance(platforms, list) or len(platforms) > 20 or any(
            not isinstance(item, str) or len(item) > 100 for item in platforms
        ):
            raise ReportError(f"registry {job_id} platforms has an unexpected shape")
        if "e2e_framework" in job and not isinstance(job["e2e_framework"], str):
            raise ReportError(f"registry {job_id} e2e_framework must be a string")
        if job.get("prow_job_history_url"):
            validate_public_url(job["prow_job_history_url"])
        presubmit = job.get("presubmit")
        if presubmit is not None:
            if not isinstance(presubmit, dict):
                raise ReportError(f"registry {job_id} presubmit must be an object")
            if "required" in presubmit and not isinstance(presubmit["required"], bool):
                raise ReportError(f"registry {job_id} presubmit.required must be boolean")
            for field in ("target_branch", "target_release"):
                if presubmit.get(field) is not None and not isinstance(
                    presubmit[field], str
                ):
                    raise ReportError(f"registry {job_id} presubmit.{field} must be a string")
        participations = job.get("release_controller") or []
        if not isinstance(participations, list) or len(participations) > 20:
            raise ReportError(f"registry {job_id} release_controller must be a bounded list")
        for participation in participations:
            if not isinstance(participation, dict):
                raise ReportError(f"registry {job_id} release participation is not an object")
            stream = participation.get("stream")
            verification = participation.get("verification")
            if not isinstance(stream, dict) or not isinstance(verification, dict):
                raise ReportError(f"registry {job_id} release participation is incomplete")
            # Custom streams legitimately omit the release-controller fields used by
            # this report. Select the report's ci/nightly stream class first, then
            # apply the strict schema only to that selected scope.
            if stream.get("kind") not in {"ci", "nightly"}:
                continue
            for field in ("name", "release", "kind", "architecture"):
                _bounded_string(stream.get(field), f"registry {job_id} stream.{field}", 255)
            if stream.get("architecture") not in {"amd64", "multi"}:
                continue
            if not isinstance(stream.get("end_of_life"), bool):
                raise ReportError(f"registry {job_id} stream.end_of_life must be boolean")
            if stream.get("release_status_url"):
                validate_public_url(stream["release_status_url"])
            _bounded_string(
                verification.get("name"), f"registry {job_id} verification.name", 255
            )
            _bounded_string(
                verification.get("role"), f"registry {job_id} verification.role", 64
            )
            for field in ("optional", "disabled"):
                if not isinstance(verification.get(field), bool):
                    raise ReportError(
                        f"registry {job_id} verification.{field} must be boolean"
                    )
    return jobs


def _validate_metric(value: Any, label: str, minimum: float, maximum: float) -> None:
    if (
        isinstance(value, bool)
        or not isinstance(value, (int, float))
        or not math.isfinite(value)
        or not minimum <= value <= maximum
    ):
        raise ReportError(f"{label} must be finite and between {minimum:g} and {maximum:g}")


def validate_health(health: Any) -> dict[str, Any]:
    if not isinstance(health, dict) or not isinstance(health.get("data"), dict):
        raise ReportError("dashboard health data is not an object")
    for group in ("jobs", "payload_blocking_jobs"):
        rows = health["data"].get(group) or []
        if not isinstance(rows, list) or len(rows) > MAX_SOURCE_JOBS:
            raise ReportError(f"dashboard health {group} is not a bounded list")
        for index, row in enumerate(rows):
            if not isinstance(row, dict):
                raise ReportError(f"dashboard health {group} row {index} is not an object")
            for identity in ("id", "prow"):
                if row.get(identity) is not None and not isinstance(row[identity], str):
                    raise ReportError(
                        f"dashboard health {group} row {index} {identity} must be a string"
                    )
            if row.get("rate") is not None:
                _validate_metric(
                    row["rate"], f"dashboard health {group} row {index} rate", 0, 100
                )
            for metric in ("runs", "fails", "test_fails", "infra_fails"):
                value = row.get(metric)
                if value is not None and (
                    isinstance(value, bool)
                    or not isinstance(value, int)
                    or not 0 <= value <= MAX_METRIC_COUNT
                ):
                    raise ReportError(
                        f"dashboard health {group} row {index} {metric} is out of range"
                    )
    return health


def validate_tag_catalog(
    catalog: Any, label: str, selected_streams: set[str]
) -> dict[str, list[str]]:
    if not isinstance(catalog, dict) or len(catalog) > MAX_SOURCE_JOBS:
        raise ReportError(f"{label} tag catalog is not a bounded object")
    result: dict[str, list[str]] = {}
    for stream in sorted(selected_streams):
        tags = catalog.get(stream)
        if tags is None:
            continue
        if not isinstance(stream, str) or not isinstance(tags, list) or len(tags) > 1000:
            raise ReportError(f"{label} tag catalog has an unexpected entry")
        if any(
            not isinstance(tag, str)
            or len(tag) > 255
            or not PAYLOAD_TAG_RE.fullmatch(tag)
            for tag in tags
        ):
            raise ReportError(f"{label} tag catalog has an invalid payload tag")
        result[stream] = tags
    return result


def selected_stream_names(
    jobs: list[dict[str, Any]], architecture: str, index_html: str | None = None
) -> set[str]:
    index_streams = set(STREAM_LINK_RE.findall(index_html)) if index_html is not None else None
    names = set()
    for job in jobs:
        if job.get("repository") != REPOSITORY:
            continue
        for participation in job.get("release_controller") or []:
            stream = participation.get("stream") or {}
            name = stream.get("name")
            if (
                stream.get("architecture") == architecture
                and stream.get("kind") in {"ci", "nightly"}
                and stream.get("end_of_life") is False
                and isinstance(name, str)
                and (index_streams is None or name in index_streams)
            ):
                names.add(name)
    return names


def exact_health_rows(health: dict[str, Any]) -> dict[str, dict[str, Any]]:
    rows: dict[str, dict[str, Any]] = {}
    data = health.get("data") or {}
    for group in ("jobs", "payload_blocking_jobs"):
        for row in data.get(group) or []:
            for identity in (row.get("id"), row.get("prow")):
                if identity:
                    rows[str(identity)] = row
    return rows


def normalize_state(value: Any) -> str:
    return RESULT_STATES.get(str(value or "").upper(), str(value or "UNKNOWN").upper())


def calculate_trend(
    runs: Iterable[dict[str, Any]], as_of: dt.datetime
) -> dict[str, Any]:
    current_start = as_of - dt.timedelta(hours=24)
    baseline_start = as_of - dt.timedelta(days=8)
    windows = {
        "current": (current_start, as_of),
        "baseline": (baseline_start, current_start),
    }
    result: dict[str, Any] = {}
    for name, (start, end) in windows.items():
        counts = {"SUCCESS": 0, "FAILURE": 0, "ABORTED": 0, "ERROR": 0}
        for run in runs:
            timestamp = run.get("_timestamp")
            if not isinstance(timestamp, dt.datetime) or not start <= timestamp < end:
                continue
            state = normalize_state(run.get("overall_result") or run.get("state"))
            if state in counts:
                counts[state] += 1
        denominator = counts["SUCCESS"] + counts["FAILURE"]
        result[name] = {
            **counts,
            "denominator": denominator,
            "rate": counts["SUCCESS"] / denominator if denominator else None,
        }

    current = result["current"]
    baseline = result["baseline"]
    if not current["denominator"] or not baseline["denominator"]:
        classification = "no_data"
        change = None
    else:
        change = (current["rate"] - baseline["rate"]) * 100
        if current["denominator"] < 5 or baseline["denominator"] < 5:
            classification = "low_confidence"
        elif change > 10:
            classification = "improving"
        elif change < -10:
            classification = "degrading"
        else:
            classification = "stable"
    return {**result, "classification": classification, "change_points": change}


def select_presubmit_trigger(relevant: list[dict[str, Any]]) -> tuple[str | None, str]:
    """Select evidence needing LLM review without assigning a verdict."""
    newest_three = relevant[:3]
    if not newest_three:
        return None, "no_data"
    if any(row["state"] == "SUCCESS" for row in newest_three):
        return None, "recent_success_veto"
    if newest_three[0]["state"] == "ERROR":
        return "infrastructure_state_review", "recent_results"
    streak = []
    for row in relevant:
        if row["state"] != "FAILURE":
            break
        streak.append(row)
    heads = {
        row["head_sha"]
        for row in streak
        if isinstance(row.get("head_sha"), str)
        and GIT_SHA_RE.fullmatch(row["head_sha"])
    }
    if len(streak) >= 3 and len(heads) >= 2:
        return "repeated_failure_review", "recent_results"
    return None, "recent_results"


def candidate_id(*parts: str) -> str:
    digest = hashlib.sha256("\x00".join(parts).encode()).hexdigest()[:16]
    return f"hci-{digest}"


def collection_id(document: dict[str, Any]) -> str:
    bound = {key: value for key, value in document.items() if key != "collection_id"}
    digest = hashlib.sha256(
        json.dumps(bound, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()[:16]
    return f"hci-doc-{digest}"


def abbreviated_job(job: str, limit: int = 48) -> str:
    for prefix in (
        "periodic-ci-openshift-hypershift-",
        "pull-ci-openshift-hypershift-",
    ):
        if job.startswith(prefix):
            job = job[len(prefix) :]
            break
    return job if len(job) <= limit else f"…{job[-(limit - 1) :]}"


def bound_text(text: str, limit: int) -> str:
    if len(text) <= limit:
        return text
    suffix = "\n… output truncated; see candidate evidence"
    return text[: limit - len(suffix)].rstrip() + suffix


def escape_slack(value: Any, limit: int = 1000) -> str:
    text = str(value or "").replace("\r", " ").replace("\n", " ").strip()
    text = text.replace("`", "ˋ")
    text = html.escape(text, quote=False)
    return text[:limit]


def bound_candidates(
    candidates: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], dict[str, Any] | None]:
    """Apply deterministic count/byte priority without concealing overflow."""
    selected = list(candidates[:MAX_CANDIDATES])
    # Reserve room for bounded scope failures/uncertainties and envelope fields.
    byte_limit = MAX_CANDIDATE_DOCUMENT_BYTES - 224 * 1024
    while selected and len(
        json.dumps({"candidates": selected}, separators=(",", ":")).encode("utf-8")
    ) > byte_limit:
        selected.pop()
    omitted = len(candidates) - len(selected)
    if not omitted:
        return selected, None
    return selected, {
        "total": len(candidates),
        "published": len(selected),
        "omitted": omitted,
        "count_limit": MAX_CANDIDATES,
        "byte_limit": MAX_CANDIDATE_DOCUMENT_BYTES,
        "priority": "periodic first, newest release first, then job ID",
    }


def bound_messages(messages: Iterable[str], label: str) -> list[str]:
    unique = sorted({str(message) for message in messages})
    bounded = []
    for message in unique[: MAX_SOURCE_FAILURES - 1]:
        if len(message) > 1000:
            message = message[:960].rstrip() + " … message truncated by 1000-character bound"
        bounded.append(message)
    omitted = len(unique) - len(bounded)
    if omitted:
        bounded.append(f"{omitted} additional {label} omitted by the explicit count bound")
    return bounded


class Collector:
    def __init__(
        self,
        transport: HTTPTransport | FixtureTransport,
        as_of: dt.datetime,
        deadline_seconds: float = COLLECTION_DEADLINE_SECONDS,
        max_requests: int = MAX_COLLECTION_REQUESTS,
    ):
        self.transport = transport
        self.as_of = as_of
        self.source_failures: list[str] = []
        self.coverage_uncertainties: list[str] = []
        if deadline_seconds <= 0 or max_requests <= 0:
            raise ReportError("collection deadline and request budget must be positive")
        self.transport.configure_collection_limits(deadline_seconds, max_requests)

    def collect(self) -> tuple[str, dict[str, Any]]:
        try:
            registry = self.transport.get_json(f"{DASHBOARD_BASE}/api/job-registry")
            health = self.transport.get_json(
                f"{DASHBOARD_BASE}/_dashboard/health/windows/1w"
            )
            index_html = self.transport.get_text(f"{RELEASE_CONTROLLER_BASE}/")
            all_tags = self.transport.get_json(
                f"{RELEASE_CONTROLLER_BASE}/api/v1/releasestreams/all"
            )
            jobs = validate_registry(registry)
            health = validate_health(health)
            all_tags = validate_tag_catalog(
                all_tags,
                "amd64 release controller",
                selected_stream_names(jobs, "amd64", index_html),
            )
        except ReportError as exc:
            return self._unknown_report(str(exc))

        try:
            releases, validated_streams = self._discover_scope(
                jobs, index_html, all_tags
            )
        except ReportError as exc:
            return self._unknown_report(str(exc))

        health_rows = exact_health_rows(health)
        blockers = self._configured_blockers(jobs, releases)
        required = self._required_presubmits(jobs, releases)
        tag_catalogs = {RELEASE_CONTROLLER_BASE: all_tags}
        if any(item["architecture"] == "multi" for item in blockers):
            try:
                tag_catalogs[MULTI_RELEASE_CONTROLLER_BASE] = validate_tag_catalog(
                    self.transport.get_json(
                        f"{MULTI_RELEASE_CONTROLLER_BASE}/api/v1/releasestreams/all"
                    ),
                    "multi-architecture release controller",
                    {item["stream"] for item in blockers if item["architecture"] == "multi"},
                )
            except ReportError as exc:
                self.source_failures.append(
                    f"multi-architecture release controller: {exc}"
                )
                tag_catalogs[MULTI_RELEASE_CONTROLLER_BASE] = {}
        payloads = self._payload_statuses(blockers, tag_catalogs)

        trend_by_job: dict[tuple[str, str], dict[str, Any]] = {}
        unique_periodics = sorted(
            {(item["job_id"], item["release"]) for item in blockers}
        )
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = {
                executor.submit(self._periodic_runs, job_id, release): (job_id, release)
                for job_id, release in unique_periodics
            }
            for future, key in futures.items():
                try:
                    runs, uncertainty = future.result()
                    for item in uncertainty:
                        self.coverage_uncertainties.append(f"Sippy {key[0]}: {item}")
                    trend_by_job[key] = {
                        "trend": calculate_trend(runs, self.as_of),
                        "runs": runs,
                        "uncertainties": uncertainty,
                    }
                except ReportError as exc:
                    self.source_failures.append(f"Sippy {key[0]}: {exc}")
                    trend_by_job[key] = {
                        "trend": calculate_trend([], self.as_of),
                        "runs": [],
                        "uncertainties": ["timestamped periodic history unavailable"],
                    }

        presubmit_evidence: dict[str, dict[str, Any]] = {}
        all_required = [job for branch_jobs in required.values() for job in branch_jobs]
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = {
                executor.submit(self._presubmit_history, job): job
                for job in all_required
            }
            for future, job in futures.items():
                try:
                    evidence = future.result()
                    for item in evidence["uncertainties"]:
                        self.coverage_uncertainties.append(f"Prow {job['id']}: {item}")
                    presubmit_evidence[job["id"]] = evidence
                except ReportError as exc:
                    self.source_failures.append(f"Prow {job['id']}: {exc}")
                    presubmit_evidence[job["id"]] = {
                        "candidate_trigger": None,
                        "runs": [],
                        "uncertainties": ["ordered presubmit history unavailable"],
                        "recent_status": "no_data",
                    }

        try:
            all_candidates = self._build_candidates(
                blockers,
                payloads,
                trend_by_job,
                required,
                presubmit_evidence,
                health_rows,
            )
        except ReportError as exc:
            return self._unknown_report(str(exc))
        candidates, candidate_overflow = bound_candidates(all_candidates)
        if candidate_overflow:
            self.source_failures.append(
                "candidate coverage overflow: "
                f"published {len(candidates)} of {len(all_candidates)} using "
                "periodic-first, newest-release, job-ID priority"
            )
        self.source_failures = bound_messages(self.source_failures, "source failure(s)")
        self.coverage_uncertainties = bound_messages(
            self.coverage_uncertainties, "coverage uncertainty item(s)"
        )

        gate_ids = {item["job_id"] for item in blockers}
        gate_ids.update(
            job["id"] for branch_jobs in required.values() for job in branch_jobs
        )
        returned_gate_rows = [
            health_rows[job_id] for job_id in sorted(gate_ids) if job_id in health_rows
        ]
        dashboard_total = len(returned_gate_rows)
        dashboard_healthy = sum(
            1
            for row in returned_gate_rows
            if isinstance(row.get("rate"), (int, float)) and row["rate"] >= 80
        )

        document = {
            "schema_version": SCHEMA_VERSION,
            "generated_at": format_rfc3339(self.as_of),
            "scope": {
                "state": (
                    "unknown"
                    if self.source_failures or self.coverage_uncertainties
                    else "complete"
                ),
                "releases": releases,
                "branches": ["main", *[f"release-{release}" for release in releases]],
                "validated_streams": validated_streams,
                "source_failures": self.source_failures,
                "coverage_uncertainties": self.coverage_uncertainties,
                "candidate_overflow": candidate_overflow,
            },
            "candidates": candidates,
        }
        document["collection_id"] = collection_id(document)
        stage_one = self._render_stage_one(
            releases,
            blockers,
            payloads,
            trend_by_job,
            required,
            presubmit_evidence,
            candidates,
            dashboard_healthy,
            dashboard_total,
            candidate_overflow,
            document["collection_id"],
        )
        try:
            validate_candidates_document(document)
        except ReportError as exc:
            return self._unknown_report(str(exc))
        return stage_one, document

    def _unknown_report(self, failure: str) -> tuple[str, dict[str, Any]]:
        failure = bound_messages([failure], "source failure(s)")[0]
        message = (
            f"*HyperShift CI Daily Health Report* — as of {format_rfc3339(self.as_of)}\n\n"
            "⚪ *Overall*: Unknown — required public data could not be validated\n"
            f"• Failed check: {escape_slack(failure, 500)}\n"
            "• No release-gate or merge-gate conclusion was published\n"
            "• Tracking: None; restore the failed public source and rerun"
        )
        document = {
            "schema_version": SCHEMA_VERSION,
            "generated_at": format_rfc3339(self.as_of),
            "scope": {
                "state": "unknown",
                "releases": [],
                "branches": [],
                "source_failures": [failure],
                "coverage_uncertainties": [],
                "candidate_overflow": None,
            },
            "candidates": [],
        }
        document["collection_id"] = collection_id(document)
        message = message.replace(
            "\n\n⚪", f" · evidence {document['collection_id']}\n\n⚪", 1
        )
        return bound_text(message, STAGE_ONE_LIMIT), document

    def _discover_scope(
        self,
        jobs: list[dict[str, Any]],
        index_html: str,
        current_tags: dict[str, Any],
    ) -> tuple[list[str], dict[str, list[str]]]:
        index_streams = set(STREAM_LINK_RE.findall(index_html))
        registry_streams: dict[str, set[str]] = defaultdict(set)
        for job in jobs:
            if job.get("repository") != REPOSITORY:
                continue
            for participation in job.get("release_controller") or []:
                stream = participation.get("stream") or {}
                if (
                    stream.get("architecture") == "amd64"
                    and stream.get("kind") in {"ci", "nightly"}
                    and stream.get("end_of_life") is False
                    and stream.get("name") in index_streams
                ):
                    registry_streams[str(stream.get("release"))].add(
                        str(stream.get("name"))
                    )

        validated: dict[str, list[str]] = defaultdict(list)
        validation_failures: list[tuple[str, str]] = []
        for release, streams in registry_streams.items():
            for stream in sorted(streams):
                tags = current_tags.get(stream)
                current_tag = tags[0] if isinstance(tags, list) and tags else None
                try:
                    matches_release = (
                        isinstance(current_tag, str)
                        and payload_release(current_tag) == release
                    )
                except ReportError:
                    matches_release = False
                if not matches_release:
                    # Future streams can be present in the index before the
                    # current-tag catalog contains a payload. They are excluded
                    # rather than promoted to N. In-scope gaps fail closed below.
                    validation_failures.append((release, stream))
                    continue
                validated[release].append(stream)

        releases = sorted(validated, key=release_key, reverse=True)[:5]
        if len(releases) != 5:
            raise ReportError(
                f"expected five validated releases, found {len(releases)}"
            )
        main_targets = {
            str((job.get("presubmit") or {}).get("target_release"))
            for job in jobs
            if job.get("repository") == REPOSITORY
            and job.get("type") == "presubmit"
            and (job.get("presubmit") or {}).get("required") is True
            and (job.get("presubmit") or {}).get("target_branch") == "main"
            and (job.get("presubmit") or {}).get("target_release")
        }
        if len(main_targets) != 1 or next(iter(main_targets)) != releases[0]:
            raise ReportError(
                "validated current release disagrees with required main-branch target"
            )
        selected_releases = set(releases)
        material_failures = [
            stream
            for release, stream in validation_failures
            if release in selected_releases
        ]
        if material_failures:
            raise ReportError(
                "release stream validation failed within supported scope: "
                + ", ".join(sorted(material_failures))
            )
        return releases, {release: sorted(validated[release]) for release in releases}

    def _configured_blockers(
        self,
        jobs: list[dict[str, Any]],
        releases: list[str],
    ) -> list[dict[str, Any]]:
        selected = []
        release_set = set(releases)
        for job in jobs:
            if job.get("repository") != REPOSITORY or job.get("type") != "periodic":
                continue
            for participation in job.get("release_controller") or []:
                stream = participation.get("stream") or {}
                verification = participation.get("verification") or {}
                if (
                    stream.get("release") in release_set
                    and verification.get("role") == "blocking"
                    and verification.get("optional") is False
                    and verification.get("disabled") is False
                ):
                    release_status_url = str(stream.get("release_status_url") or "")
                    if release_status_url:
                        try:
                            validate_public_url(release_status_url)
                        except ReportError as exc:
                            self.source_failures.append(
                                f"registry release-status URL for {job.get('id')}: {exc}"
                            )
                            release_status_url = ""
                    selected.append(
                        {
                            "job_id": str(job.get("id")),
                            "release": str(stream.get("release")),
                            "stream": str(stream.get("name")),
                            "architecture": str(
                                stream.get("architecture") or "unknown"
                            ),
                            "stream_kind": str(stream.get("kind") or "unknown"),
                            "release_status_url": release_status_url,
                            "verification": str(verification.get("name")),
                            "platforms": list(job.get("platforms") or []),
                            "framework": str(job.get("e2e_framework") or "none"),
                        }
                    )
        return sorted(
            selected,
            key=lambda item: (release_key(item["release"]), item["job_id"]),
            reverse=True,
        )

    def _required_presubmits(
        self,
        jobs: list[dict[str, Any]],
        releases: list[str],
    ) -> dict[str, list[dict[str, Any]]]:
        branches = ["main", *[f"release-{release}" for release in releases]]
        grouped: dict[str, list[dict[str, Any]]] = {branch: [] for branch in branches}
        for job in jobs:
            presubmit = job.get("presubmit") or {}
            branch = presubmit.get("target_branch")
            if (
                job.get("repository") == REPOSITORY
                and job.get("type") == "presubmit"
                and presubmit.get("required") is True
                and branch in grouped
            ):
                grouped[str(branch)].append(job)
        for branch_jobs in grouped.values():
            branch_jobs.sort(key=lambda job: str(job.get("id")))
        return grouped

    def _payload_statuses(
        self,
        blockers: list[dict[str, Any]],
        tag_catalogs: dict[str, dict[str, Any]],
    ) -> dict[tuple[str, str], dict[str, Any]]:
        statuses: dict[tuple[str, str, str, str], dict[str, Any]] = {}
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = {}
            scheduled_streams = set()
            for blocker in blockers:
                stream = blocker["stream"]
                controller = (
                    MULTI_RELEASE_CONTROLLER_BASE
                    if blocker["architecture"] == "multi"
                    else RELEASE_CONTROLLER_BASE
                )
                schedule_key = (controller, stream, blocker["release"])
                if schedule_key in scheduled_streams:
                    continue
                scheduled_streams.add(schedule_key)
                tags = tag_catalogs.get(controller, {}).get(stream)
                if not isinstance(tags, list) or not tags:
                    statuses[(controller, stream, blocker["release"], "")] = {
                        "state": "unknown",
                        "uncertainty": "current tag unavailable",
                    }
                    continue
                tag = str(tags[0])
                try:
                    tag_release = payload_release(tag)
                except ReportError as exc:
                    tag_release = ""
                    tag_error = str(exc)
                else:
                    tag_error = ""
                if tag_release != blocker["release"]:
                    uncertainty = tag_error or (
                        f"current tag release {tag_release} does not match configured "
                        f"release {blocker['release']}"
                    )
                    self.source_failures.append(
                        f"release controller {stream}: {uncertainty}"
                    )
                    statuses[(controller, stream, blocker["release"], tag)] = {
                        "state": "unknown",
                        "uncertainty": uncertainty,
                    }
                    continue
                url = (
                    f"{controller}/api/v1/releasestream/{urllib.parse.quote(stream)}"
                    f"/release/{urllib.parse.quote(tag)}?format=short"
                )
                futures[executor.submit(self.transport.get_json, url)] = (
                    controller,
                    stream,
                    blocker["release"],
                    tag,
                )
            for future, (controller, stream, release, tag) in futures.items():
                try:
                    detail = future.result()
                    if not isinstance(detail, dict):
                        raise ReportError("release detail is not an object")
                    if detail.get("name") != tag:
                        raise ReportError("release detail tag mismatch")
                    _grammar_string(detail.get("name"), "release detail tag", PAYLOAD_TAG_RE)
                    _grammar_string(
                        detail.get("phase") or "Unknown", "release detail phase", PHASE_RE
                    )
                    if not isinstance(detail.get("results") or {}, dict):
                        raise ReportError("release detail results is not an object")
                    statuses[(controller, stream, release, tag)] = detail
                except (AttributeError, ReportError) as exc:
                    self.source_failures.append(f"release controller {stream}: {exc}")
                    statuses[(controller, stream, release, tag)] = {
                        "state": "unknown",
                        "uncertainty": str(exc),
                    }

        result: dict[tuple[str, str], dict[str, Any]] = {}
        for blocker in blockers:
            stream = blocker["stream"]
            controller = (
                MULTI_RELEASE_CONTROLLER_BASE
                if blocker["architecture"] == "multi"
                else RELEASE_CONTROLLER_BASE
            )
            tags = tag_catalogs.get(controller, {}).get(stream)
            tag = str(tags[0]) if isinstance(tags, list) and tags else ""
            detail = statuses.get(
                (controller, stream, blocker["release"], tag),
                statuses.get((controller, stream, blocker["release"], ""), {}),
            )
            phase = detail.get("phase")
            verification_status = None
            verification_group = None
            results = detail.get("results") or {}
            for group in ("blockingJobs", "pendingJobs", "informingJobs", "asyncJobs"):
                group_results = results.get(group) or {}
                if not isinstance(group_results, dict):
                    self.source_failures.append(
                        f"release controller {stream}: {group} is not an object"
                    )
                    continue
                value = group_results.get(blocker["verification"])
                if value:
                    if not isinstance(value, dict):
                        self.source_failures.append(
                            f"release controller {stream}: verification result is not an object"
                        )
                        continue
                    verification_status = value
                    verification_group = group
                    break
            state = (verification_status or {}).get("state")
            if state is not None and not isinstance(state, str):
                self.source_failures.append(
                    f"release controller {stream}: verification state is not a string"
                )
                state = None
            raw_verification_url = (verification_status or {}).get("url") or ""
            if not isinstance(raw_verification_url, str):
                self.source_failures.append(
                    f"release controller {stream}: verification URL is not a string"
                )
                raw_verification_url = ""
            verification_url = raw_verification_url
            verification_uncertainty = str(detail.get("uncertainty") or "")
            if verification_status is not None and verification_group != "blockingJobs":
                verification_uncertainty = (
                    f"configured blocking verification appeared in {verification_group}"
                )
            if verification_url:
                try:
                    validate_verification_url(verification_url)
                except ReportError as exc:
                    verification_url = ""
                    verification_uncertainty = f"verification URL rejected: {exc}"
                    self.source_failures.append(
                        f"release controller {stream}: {verification_uncertainty}"
                    )
            if (
                verification_group == "blockingJobs"
                and state in {"Failed", "Pending"}
                and phase not in {"Accepted", "Rejected"}
            ):
                impact = "verified_blocker"
            elif verification_group == "blockingJobs" and state == "Succeeded":
                impact = "passing"
            else:
                impact = "unknown"
            result[(blocker["job_id"], stream)] = {
                "tag": tag,
                "phase": phase or "Unknown",
                "verification_state": state or "Unknown",
                "verification_url": verification_url,
                "impact": "unknown" if verification_uncertainty else impact,
                "uncertainty": verification_uncertainty,
                "release_status_url": blocker["release_status_url"],
            }
        return result

    def _periodic_runs(
        self, job_id: str, release: str
    ) -> tuple[list[dict[str, Any]], list[str]]:
        lower = self.as_of - dt.timedelta(days=8, seconds=1)
        filter_value = {
            "items": [
                {"columnField": "name", "operatorValue": "contains", "value": job_id},
                {
                    "columnField": "timestamp",
                    "operatorValue": ">",
                    "value": format_rfc3339(lower),
                },
                {
                    "columnField": "timestamp",
                    "operatorValue": "<",
                    "value": format_rfc3339(self.as_of),
                },
            ],
            "linkOperator": "and",
        }
        rows: list[dict[str, Any]] = []
        page = 0
        while page < 5 and len(rows) < MAX_SIPPY_ROWS:
            params = urllib.parse.urlencode(
                {
                    "release": release,
                    "filter": json.dumps(filter_value, separators=(",", ":")),
                    "perPage": "500",
                    "page": str(page),
                    "sortField": "timestamp",
                    "sort": "desc",
                }
            )
            data = self.transport.get_json(f"{SIPPY_RUNS_URL}?{params}")
            if not isinstance(data, dict):
                raise ReportError("Sippy response is not an object")
            page_rows = data.get("rows")
            if not isinstance(page_rows, list):
                raise ReportError("Sippy rows is not a list")
            if any(not isinstance(row, dict) for row in page_rows):
                raise ReportError("Sippy rows must contain only objects")
            for row in page_rows:
                for field in ("job", "timestamp", "overall_result"):
                    if not isinstance(row.get(field), str):
                        raise ReportError(f"Sippy row {field} must be a string")
                if row.get("annotations") is not None and not isinstance(
                    row["annotations"], dict
                ):
                    raise ReportError("Sippy row annotations must be an object")
                for field in ("url", "test_grid_url"):
                    if row.get(field) is not None and not isinstance(row[field], str):
                        raise ReportError(f"Sippy row {field} must be a string")
                run_url = str(row.get("url") or row.get("test_grid_url") or "")
                if run_url:
                    run_id = str(row.get("prow_id") or row.get("id") or "")
                    validate_run_evidence_url(run_url, run_id)
            rows.extend(page_rows)
            try:
                total_rows = int(data.get("total_rows", len(rows)))
            except (TypeError, ValueError) as exc:
                raise ReportError("Sippy total_rows is not an integer") from exc
            if len(rows) >= total_rows or not page_rows:
                break
            page += 1
        uncertainty = []
        if len(rows) >= MAX_SIPPY_ROWS or page >= 5:
            uncertainty.append("Sippy pagination cap reached")
        exact = []
        window_start = self.as_of - dt.timedelta(days=8)
        for row in rows:
            if row.get("job") != job_id:
                continue
            try:
                timestamp = parse_rfc3339(str(row.get("timestamp")))
            except ReportError:
                uncertainty.append("run with invalid timestamp omitted")
                continue
            if not window_start <= timestamp < self.as_of:
                continue
            if normalize_state(row.get("overall_result")) not in {
                "SUCCESS",
                "FAILURE",
                "ABORTED",
                "ERROR",
            }:
                uncertainty.append("run with unrecognized result omitted")
                continue
            exact.append({**row, "_timestamp": timestamp})
        exact.sort(key=lambda row: row["_timestamp"], reverse=True)
        return exact, sorted(set(uncertainty))

    def _presubmit_history(self, job: dict[str, Any]) -> dict[str, Any]:
        url = str(job.get("prow_job_history_url") or "")
        if not url:
            raise ReportError("registry has no Prow history URL")
        rows: list[dict[str, Any]] = []
        page_url = url
        window_start = self.as_of - dt.timedelta(hours=12)
        uncertainty: list[str] = []
        for _ in range(MAX_HISTORY_PAGES):
            try:
                text = self.transport.get_text(page_url)
            except NoDataError:
                if not rows:
                    return {
                        "candidate_trigger": None,
                        "runs": [],
                        "uncertainties": ["Prow history has no runs yet"],
                        "recent_status": "no_data",
                    }
                uncertainty.append("older Prow history page has no data")
                break
            page_rows, older, page_uncertainties = parse_prow_history(text, page_url)
            uncertainty.extend(page_uncertainties)
            rows.extend(page_rows)
            starts = [
                row["_started"]
                for row in page_rows
                if isinstance(row.get("_started"), dt.datetime)
            ]
            if not older or (starts and min(starts) < window_start):
                break
            page_url = older
        else:
            uncertainty.append("Prow history pagination cap reached")

        relevant = []
        for row in rows:
            state = normalize_state(row.get("Result"))
            if state not in {"SUCCESS", "FAILURE", "ERROR", "ABORTED"}:
                continue
            completion = row.get("_completion")
            if (
                not isinstance(completion, dt.datetime)
                or not window_start <= completion < self.as_of
            ):
                continue
            refs = row.get("Refs") or {}
            pulls = refs.get("pulls") or []
            sha = str(pulls[0].get("sha") or "") if pulls else ""
            if not GIT_SHA_RE.fullmatch(sha):
                uncertainty.append(
                    f"run {row.get('ID') or '<unknown>'} lacks a canonical head SHA"
                )
                sha = ""
            relevant.append(
                {
                    "id": str(row.get("ID") or ""),
                    "started": format_rfc3339(row["_started"]),
                    "completed": format_rfc3339(completion),
                    "state": state,
                    "head_sha": sha,
                    "url": urllib.parse.urljoin(
                        f"https://{PROW_HOST}", str(row.get("SpyglassLink") or "")
                    ),
                }
            )
        relevant.sort(key=lambda row: parse_rfc3339(row["completed"]), reverse=True)
        refined = []
        for run in relevant[:3]:
            prowjob = self.transport.get_json(public_prowjob_url(run["url"]))
            if not isinstance(prowjob, dict):
                raise ReportError(f"public prowjob for {run['id']} is not an object")
            status = prowjob.get("status")
            spec = prowjob.get("spec")
            if not isinstance(status, dict) or not isinstance(spec, dict):
                raise ReportError(f"invalid public prowjob shape for {run['id']}")
            refs = spec.get("refs")
            if not isinstance(refs, dict):
                raise ReportError(f"invalid public prowjob refs for {run['id']}")
            pulls = refs.get("pulls")
            if not isinstance(pulls, list) or any(
                not isinstance(pull, dict) for pull in pulls
            ):
                raise ReportError(f"invalid public prowjob refs for {run['id']}")
            try:
                started = parse_rfc3339(str(status.get("startTime")))
            except ReportError as exc:
                raise ReportError(
                    f"invalid public prowjob timing for {run['id']}"
                ) from exc
            state = normalize_state(status.get("state"))
            if state not in {"SUCCESS", "FAILURE", "ABORTED", "ERROR"}:
                raise ReportError(f"invalid public prowjob state for {run['id']}")
            completion_value = status.get("completionTime")
            if completion_value:
                try:
                    completed = parse_rfc3339(str(completion_value))
                except ReportError as exc:
                    raise ReportError(
                        f"invalid public prowjob timing for {run['id']}"
                    ) from exc
            elif state == "ABORTED":
                completed = parse_rfc3339(run["completed"])
                uncertainty.append(
                    f"aborted run {run['id']} lacks completionTime; used history-derived completion"
                )
            else:
                uncertainty.append(
                    f"run {run['id']} lacks completionTime and was omitted"
                )
                continue
            run = {
                **run,
                "started": format_rfc3339(started),
                "completed": format_rfc3339(completed),
                "state": state,
                "head_sha": str(pulls[0].get("sha") or run["head_sha"])
                if pulls
                else run["head_sha"],
            }
            if not GIT_SHA_RE.fullmatch(run["head_sha"]):
                uncertainty.append(
                    f"run {run['id']} lacks a canonical head SHA"
                )
                run["head_sha"] = ""
            if window_start <= completed < self.as_of:
                refined.append(run)
        relevant = refined + relevant[3:]
        relevant.sort(key=lambda row: parse_rfc3339(row["completed"]), reverse=True)
        if not relevant:
            uncertainty.append("Prow history has no completed runs in the 12-hour window")
        candidate_trigger, recent_status = select_presubmit_trigger(relevant)
        return {
            "candidate_trigger": candidate_trigger,
            "runs": relevant,
            "uncertainties": sorted(set(uncertainty)),
            "recent_status": recent_status,
        }

    def _build_candidates(
        self,
        blockers: list[dict[str, Any]],
        payloads: dict[tuple[str, str], dict[str, Any]],
        trends: dict[tuple[str, str], dict[str, Any]],
        required: dict[str, list[dict[str, Any]]],
        presubmit_evidence: dict[str, dict[str, Any]],
        health_rows: dict[str, dict[str, Any]],
    ) -> list[dict[str, Any]]:
        candidates = []
        seen_periodics = set()
        for blocker in blockers:
            key = (blocker["job_id"], blocker["release"])
            payload = payloads[(blocker["job_id"], blocker["stream"])]
            evidence = trends[key]
            trend = evidence["trend"]
            trigger = None
            if payload["impact"] == "verified_blocker":
                trigger = "live_payload_blocking_verification"
            elif payload["impact"] == "unknown":
                trigger = "payload_impact_unknown"
            elif trend["current"]["FAILURE"]:
                trigger = "current_window_failures"
            elif trend["classification"] == "degrading":
                trigger = "degrading_exact_window_trend"
            if not trigger or key in seen_periodics:
                continue
            seen_periodics.add(key)
            samples = [
                serialize_sippy_run(run) for run in evidence["runs"][:MAX_SAMPLE_RUNS]
            ]
            candidates.append(
                {
                    "candidate_id": candidate_id(
                        "periodic", blocker["job_id"], blocker["release"]
                    ),
                    "kind": "periodic",
                    "job_id": blocker["job_id"],
                    "release": blocker["release"],
                    "branch": None,
                    "deterministic_trigger": trigger,
                    "configured_role": {
                        "verification": blocker["verification"],
                        "stream": blocker["stream"],
                        "architecture": blocker["architecture"],
                        "stream_kind": blocker["stream_kind"],
                        "platforms": blocker["platforms"],
                        "framework": blocker["framework"],
                    },
                    "live_payload": payload,
                    "dashboard_1w": summarize_health(
                        health_rows.get(blocker["job_id"])
                    ),
                    "trend": trend,
                    "runs": samples,
                    "coverage_uncertainties": evidence["uncertainties"],
                }
            )

        for branch, jobs in required.items():
            for job in jobs:
                evidence = presubmit_evidence[job["id"]]
                trigger = evidence["candidate_trigger"]
                if not trigger:
                    continue
                candidates.append(
                    {
                        "candidate_id": candidate_id("presubmit", job["id"], branch),
                        "kind": "presubmit",
                        "job_id": job["id"],
                        "release": (job.get("presubmit") or {}).get("target_release"),
                        "branch": branch,
                        "deterministic_trigger": trigger,
                        "configured_role": {
                            "required": True,
                            "platforms": list(job.get("platforms") or []),
                            "framework": job.get("e2e_framework") or "none",
                        },
                        "live_payload": None,
                        "dashboard_1w": summarize_health(health_rows.get(job["id"])),
                        "trend": None,
                        "runs": evidence["runs"][:MAX_SAMPLE_RUNS],
                        "coverage_uncertainties": evidence["uncertainties"],
                    }
                )
        return sorted(
            candidates,
            key=lambda item: (
                0 if item["kind"] == "periodic" else 1,
                tuple(-part for part in release_key(str(item.get("release") or "0.0"))),
                item["job_id"],
            ),
        )

    def _render_stage_one(
        self,
        releases: list[str],
        blockers: list[dict[str, Any]],
        payloads: dict[tuple[str, str], dict[str, Any]],
        trends: dict[tuple[str, str], dict[str, Any]],
        required: dict[str, list[dict[str, Any]]],
        presubmit_evidence: dict[str, dict[str, Any]],
        candidates: list[dict[str, Any]],
        dashboard_healthy: int,
        dashboard_total: int,
        candidate_overflow: dict[str, Any] | None,
        evidence_set: str,
    ) -> str:
        trend_counts: dict[str, int] = defaultdict(int)
        for evidence in trends.values():
            trend_counts[evidence["trend"]["classification"]] += 1
        live_count = sum(
            1
            for payload in payloads.values()
            if payload["impact"] == "verified_blocker"
        )
        unknown_count = (
            len(self.source_failures)
            + len(self.coverage_uncertainties)
            + sum(
                1 for payload in payloads.values() if payload["impact"] == "unknown"
            )
        )
        if unknown_count:
            emoji, overall = "⚪", "Unknown — public source or coverage gaps remain"
        elif live_count:
            emoji, overall = "🔴", f"{live_count} live blocking verification(s)"
        elif candidates:
            emoji, overall = (
                "🟡",
                f"{len(candidates)} candidate(s) require LLM judgment",
            )
        else:
            emoji, overall = "🟢", "No live blockers or merge-gate candidates"
        if candidate_overflow:
            overall += (
                f"; candidate coverage {candidate_overflow['published']}/"
                f"{candidate_overflow['total']}"
            )

        lines = [
            f"*HyperShift CI Daily Health Report* — as of {format_rfc3339(self.as_of)} · evidence {evidence_set}",
            "",
            f"{emoji} *Overall*: {overall} | Dashboard 1w: {dashboard_healthy}/{dashboard_total} healthy",
            "",
            "*Trend — exact 24h vs preceding 7d*",
            (
                f"• 📈 {trend_counts['improving']} · 📉 {trend_counts['degrading']} · "
                f"➡️ {trend_counts['stable']} · ⚠️ {trend_counts['low_confidence']} · "
                f"⚪ {trend_counts['no_data']}"
            ),
            "",
            "*Release blockers — N through N-4*",
        ]
        by_release: dict[str, list[dict[str, Any]]] = defaultdict(list)
        for blocker in blockers:
            by_release[blocker["release"]].append(blocker)
        for release in releases:
            release_blockers = by_release[release]
            live = [
                blocker
                for blocker in release_blockers
                if payloads[(blocker["job_id"], blocker["stream"])]["impact"]
                == "verified_blocker"
            ]
            unknown = [
                blocker
                for blocker in release_blockers
                if payloads[(blocker["job_id"], blocker["stream"])]["impact"]
                == "unknown"
            ]
            tag = "No current configured blocker payload"
            url = ""
            if release_blockers:
                sample = payloads[
                    (release_blockers[0]["job_id"], release_blockers[0]["stream"])
                ]
                safe_tag = escape_slack(sample["tag"] or "Unknown", 255)
                safe_phase = escape_slack(sample["phase"], 64)
                tag = f"{safe_tag} {safe_phase}"
                url = sample["release_status_url"]
            label = f"<{url}|{tag}>" if url else tag
            lines.append(
                f"• *OCP {escape_slack(release, 20)}* · {label}: {len(live)} blocker(s), {len(unknown)} unknown"
            )

        lines.extend(["", "*Merge-gate candidates — by branch*"])
        candidate_jobs = {
            item["job_id"] for item in candidates if item["kind"] == "presubmit"
        }
        for branch, jobs in required.items():
            target = (
                str(
                    (jobs[0].get("presubmit") or {}).get("target_release")
                    or releases[0]
                )
                if branch == "main"
                else branch.removeprefix("release-")
            )
            count = sum(1 for job in jobs if job["id"] in candidate_jobs)
            no_data = sum(
                1 for job in jobs if not presubmit_evidence[job["id"]]["runs"]
            )
            lines.append(
                f"• *{escape_slack(branch, 80)} → OCP {escape_slack(target, 20)}*: "
                f"{count} candidate(s), {no_data} no-data gate(s)"
            )

        lines.extend(
            [
                "",
                "*Action items*",
                f"• Judge {len(candidates)} bounded candidate(s); do not infer branch impact from one PR",
            ]
        )
        if candidate_overflow:
            lines.append(
                "• Coverage overflow: "
                f"{candidate_overflow['omitted']} of {candidate_overflow['total']} candidate(s) "
                "omitted after deterministic periodic/newest-release/job-ID priority; state is Unknown"
            )
        if self.coverage_uncertainties:
            lines.append(
                f"• Coverage uncertainties: {len(self.coverage_uncertainties)}; "
                "preserved separately from transport/source failures"
            )
        lines.append(
            "• Tracking: verify an existing OCPBUGS/CNTRLPLANE issue only for a deduplicated incident; no automated writes"
        )
        return bound_text("\n".join(lines), STAGE_ONE_LIMIT)


def parse_prow_history(
    text: str, page_url: str
) -> tuple[list[dict[str, Any]], str | None, list[str]]:
    match = ALL_BUILDS_RE.search(text)
    if not match:
        raise ReportError("Prow history page lacks allBuilds data")
    try:
        raw_rows = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        raise ReportError("Prow allBuilds is invalid JSON") from exc
    if not isinstance(raw_rows, list):
        raise ReportError("Prow allBuilds is not a list")
    rows = []
    nonterminal = 0
    for index, row in enumerate(raw_rows[:20]):
        if not isinstance(row, dict):
            raise ReportError(f"Prow allBuilds row {index} is not an object")
        for field in ("ID", "Started", "Duration", "Result", "SpyglassLink", "Refs"):
            if field not in row:
                raise ReportError(f"Prow allBuilds row {index} lacks {field}")
        refs = row.get("Refs")
        if not isinstance(refs, dict):
            raise ReportError(f"Prow allBuilds row {index} Refs is not an object")
        pulls = refs.get("pulls")
        if not isinstance(pulls, list) or any(not isinstance(pull, dict) for pull in pulls):
            raise ReportError(f"Prow allBuilds row {index} pulls is not a list of objects")
        state = normalize_state(row.get("Result"))
        if state not in {"SUCCESS", "FAILURE", "ABORTED", "ERROR"}:
            nonterminal += 1
            continue
        try:
            started = parse_rfc3339(str(row.get("Started")))
            duration_ns = int(row.get("Duration") or 0)
        except (ReportError, TypeError, ValueError):
            raise ReportError(f"Prow allBuilds row {index} has invalid timing")
        if not 0 <= duration_ns <= MAX_PROW_DURATION_NS:
            raise ReportError(f"Prow allBuilds row {index} duration is out of range")
        run_url = urllib.parse.urljoin(
            f"https://{PROW_HOST}", str(row.get("SpyglassLink") or "")
        )
        validate_public_url(run_url)
        rows.append(
            {
                **row,
                "_started": started,
                "_completion": started + dt.timedelta(microseconds=duration_ns / 1000),
            }
        )
    older_match = OLDER_LINK_RE.search(text)
    older = (
        urllib.parse.urljoin(page_url, html.unescape(older_match.group(1)))
        if older_match
        else None
    )
    if older:
        validate_public_url(older)
    uncertainties = (
        [f"Prow history omitted {nonterminal} nonterminal run(s)"] if nonterminal else []
    )
    return rows, older, uncertainties


def public_prowjob_url(prow_url: str) -> str:
    validate_run_evidence_url(prow_url)
    parsed = _parse_url(prow_url)
    prefix = "/view/gs/"
    if not parsed.path.startswith(prefix):
        raise ReportError("unexpected Prow run path")
    remainder = parsed.path[len(prefix) :]
    bucket, separator, object_path = remainder.partition("/")
    if not separator or bucket not in {"test-platform-results", PUBLIC_RESULTS_BUCKET}:
        raise ReportError("unexpected Prow results bucket")
    url = f"https://storage.googleapis.com/{PUBLIC_RESULTS_BUCKET}/{object_path}/prowjob.json"
    validate_public_url(url)
    return url


def validate_verification_url(url: str) -> None:
    """Release verification evidence must be a canonical public Prow run."""
    validate_public_url(url)
    parsed = urllib.parse.urlparse(url)
    if parsed.hostname != PROW_HOST or not parsed.path.startswith(
        "/view/gs/test-platform-results-public/"
    ):
        raise ReportError("verification URL is not a public Prow result")


def validate_run_evidence_url(url: str, expected_id: str | None = None) -> None:
    """Run evidence must identify one canonical public Prow execution."""
    validate_public_url(url)
    parsed = _parse_url(url)
    if parsed.hostname != PROW_HOST or not re.fullmatch(
        r"/view/gs/(?:test-platform-results|test-platform-results-public)/"
        r"(?:logs|pr-logs)/.+/[0-9]+",
        parsed.path,
    ):
        raise ReportError("run URL is not a canonical public Prow result")
    run_id = parsed.path.rsplit("/", 1)[-1]
    if expected_id is not None and run_id != expected_id:
        raise ReportError("run URL does not match its run ID")


def serialize_sippy_run(run: dict[str, Any]) -> dict[str, Any]:
    annotations = run.get("annotations") or {}
    if not isinstance(annotations, dict):
        raise ReportError("Sippy run annotations is not an object")
    url = str(run.get("url") or run.get("test_grid_url") or "")
    if url:
        validate_run_evidence_url(url, str(run.get("prow_id") or run.get("id") or ""))
    payload_tag = str(annotations.get("release.openshift.io/tag") or "")
    if payload_tag:
        _grammar_string(payload_tag, "Sippy payload tag", PAYLOAD_TAG_RE)
    return {
        "id": str(run.get("prow_id") or run.get("id") or ""),
        "timestamp": format_rfc3339(run["_timestamp"]),
        "state": normalize_state(run.get("overall_result")),
        "head_sha": str(run.get("pull_request_sha") or ""),
        "url": url,
        "payload_tag": payload_tag,
    }


def summarize_health(row: dict[str, Any] | None) -> dict[str, Any]:
    if not row:
        return {"status": "no_dashboard_data"}
    return {
        "status": "available",
        "rate": row.get("rate"),
        "runs": row.get("runs"),
        "fails": row.get("fails"),
        "test_fails": row.get("test_fails"),
        "infra_fails": row.get("infra_fails"),
    }


def _validate_string_list(
    value: Any, label: str, max_items: int, max_length: int
) -> list[str]:
    if not isinstance(value, list) or len(value) > max_items:
        raise ReportError(f"{label} must be a list of at most {max_items} strings")
    for item in value:
        if not isinstance(item, str) or len(item) > max_length:
            raise ReportError(f"{label} contains an invalid string")
    return value


def _validate_candidate_run(run: Any, kind: str, label: str) -> None:
    if not isinstance(run, dict):
        raise ReportError(f"{label} must be an object")
    identifier = _bounded_string(run.get("id"), f"{label}.id", 255)
    if not re.fullmatch(r"[A-Za-z0-9._-]+", identifier):
        raise ReportError(f"{label}.id has an invalid format")
    if run.get("state") not in {"SUCCESS", "FAILURE", "ABORTED", "ERROR"}:
        raise ReportError(f"{label}.state is invalid")
    timestamp_fields = ("timestamp",) if kind == "periodic" else ("started", "completed")
    for field in timestamp_fields:
        parse_rfc3339(run.get(field))
    head_sha = run.get("head_sha") or ""
    if not isinstance(head_sha, str) or (head_sha and not GIT_SHA_RE.fullmatch(head_sha)):
        raise ReportError(f"{label}.head_sha is invalid")
    url = run.get("url") or ""
    if not isinstance(url, str):
        raise ReportError(f"{label}.url must be a string")
    if url:
        validate_run_evidence_url(url, identifier)
    payload_tag = run.get("payload_tag") or ""
    if not isinstance(payload_tag, str):
        raise ReportError(f"{label}.payload_tag must be a string")
    if payload_tag:
        _grammar_string(payload_tag, f"{label}.payload_tag", PAYLOAD_TAG_RE)


def _validate_trend(trend: Any, label: str) -> None:
    if not isinstance(trend, dict):
        raise ReportError(f"{label} must be an object")
    for window in ("current", "baseline"):
        counts = trend.get(window)
        if not isinstance(counts, dict):
            raise ReportError(f"{label}.{window} must be an object")
        for state in ("SUCCESS", "FAILURE", "ABORTED", "ERROR", "denominator"):
            if not isinstance(counts.get(state), int) or counts[state] < 0:
                raise ReportError(f"{label}.{window}.{state} must be a non-negative integer")
        if counts["denominator"] != counts["SUCCESS"] + counts["FAILURE"]:
            raise ReportError(f"{label}.{window}.denominator is inconsistent")
        rate = counts.get("rate")
        expected_rate = (
            counts["SUCCESS"] / counts["denominator"]
            if counts["denominator"]
            else None
        )
        if expected_rate is None:
            if rate is not None:
                raise ReportError(f"{label}.{window}.rate must be null")
        elif (
            isinstance(rate, bool)
            or not isinstance(rate, (int, float))
            or not math.isfinite(rate)
            or not 0 <= rate <= 1
            or abs(rate - expected_rate) > 1e-9
        ):
            raise ReportError(f"{label}.{window}.rate is inconsistent")
    if trend.get("classification") not in {
        "no_data",
        "low_confidence",
        "improving",
        "degrading",
        "stable",
    }:
        raise ReportError(f"{label}.classification is invalid")
    change = trend.get("change_points")
    if change is not None and (
        isinstance(change, bool)
        or not isinstance(change, (int, float))
        or not math.isfinite(change)
        or not -100 <= change <= 100
    ):
        raise ReportError(f"{label}.change_points must be finite and ranged")


def validate_candidates_document(document: Any) -> dict[str, Any]:
    try:
        document_bytes = len(
            json.dumps(document, separators=(",", ":")).encode("utf-8")
        )
    except (TypeError, ValueError) as exc:
        raise ReportError("candidates document is not valid JSON data") from exc
    if document_bytes > MAX_CANDIDATE_DOCUMENT_BYTES:
        raise ReportError(
            f"candidates document exceeds {MAX_CANDIDATE_DOCUMENT_BYTES} bytes"
        )
    if (
        not isinstance(document, dict)
        or document.get("schema_version") != SCHEMA_VERSION
    ):
        raise ReportError(f"candidates schema_version must be {SCHEMA_VERSION}")
    if not isinstance(document.get("candidates"), list):
        raise ReportError("candidates must be a list")
    if len(document["candidates"]) > MAX_CANDIDATES:
        raise ReportError(f"candidates must contain at most {MAX_CANDIDATES} entries")
    parse_rfc3339(document.get("generated_at"))
    collection_identifier = _grammar_string(
        document.get("collection_id"), "collection_id", COLLECTION_ID_RE
    )
    scope = document.get("scope")
    if not isinstance(scope, dict) or scope.get("state") not in {"complete", "unknown"}:
        raise ReportError("candidate scope must be a complete or unknown object")
    _validate_string_list(
        scope.get("source_failures", []), "scope.source_failures", MAX_SOURCE_FAILURES, 1000
    )
    _validate_string_list(
        scope.get("coverage_uncertainties", []),
        "scope.coverage_uncertainties",
        MAX_SOURCE_FAILURES,
        1000,
    )
    overflow = scope.get("candidate_overflow")
    if overflow is not None:
        if not isinstance(overflow, dict) or any(
            not isinstance(overflow.get(field), int) or overflow[field] < 0
            for field in ("total", "published", "omitted", "count_limit", "byte_limit")
        ):
            raise ReportError("scope.candidate_overflow has an unexpected shape")
        if (
            overflow["published"] != len(document["candidates"])
            or overflow["total"] - overflow["published"] != overflow["omitted"]
            or overflow["omitted"] <= 0
        ):
            raise ReportError("scope.candidate_overflow counts are inconsistent")
        _bounded_string(overflow.get("priority"), "scope.candidate_overflow.priority", 200)
        if scope["state"] != "unknown":
            raise ReportError("candidate overflow requires unknown scope")

    ids: list[str] = []
    for index, item in enumerate(document["candidates"]):
        label = f"candidate {index}"
        if not isinstance(item, dict):
            raise ReportError(f"{label} must be an object")
        identifier = _grammar_string(
            item.get("candidate_id"), f"{label}.candidate_id", CANDIDATE_ID_RE
        )
        ids.append(identifier)
        kind = item.get("kind")
        if kind not in KIND_JUDGMENTS:
            raise ReportError(f"{label}.kind is invalid")
        _grammar_string(item.get("job_id"), f"{label}.job_id", JOB_ID_RE)
        _grammar_string(item.get("release"), f"{label}.release", RELEASE_RE)
        branch = item.get("branch")
        if kind == "presubmit":
            _grammar_string(branch, f"{label}.branch", BRANCH_RE)
        elif branch is not None:
            raise ReportError(f"{label}.branch must be null for a periodic")
        expected_identifier = candidate_id(
            kind,
            item["job_id"],
            branch if kind == "presubmit" else item["release"],
        )
        if identifier != expected_identifier:
            raise ReportError(f"{label}.candidate_id does not match its identity")
        _bounded_string(
            item.get("deterministic_trigger"), f"{label}.deterministic_trigger", 100
        )
        role = item.get("configured_role")
        if not isinstance(role, dict):
            raise ReportError(f"{label}.configured_role must be an object")
        if kind == "periodic":
            for field in ("verification", "stream", "architecture", "stream_kind", "framework"):
                _bounded_string(role.get(field), f"{label}.configured_role.{field}", 255)
        else:
            if role.get("required") is not True:
                raise ReportError(f"{label}.configured_role.required must be true")
            _bounded_string(role.get("framework"), f"{label}.configured_role.framework", 255)
        _validate_string_list(
            role.get("platforms"), f"{label}.configured_role.platforms", 20, 100
        )
        dashboard = item.get("dashboard_1w")
        if not isinstance(dashboard, dict) or dashboard.get("status") not in {
            "available",
            "no_dashboard_data",
        }:
            raise ReportError(f"{label}.dashboard_1w has an unexpected shape")
        if dashboard["status"] == "available":
            rate = dashboard.get("rate")
            if rate is not None:
                _validate_metric(rate, f"{label}.dashboard_1w.rate", 0, 100)
            for field in ("runs", "fails", "test_fails", "infra_fails"):
                if dashboard.get(field) is not None and (
                    isinstance(dashboard[field], bool)
                    or not isinstance(dashboard[field], int)
                    or not 0 <= dashboard[field] <= MAX_METRIC_COUNT
                ):
                    raise ReportError(f"{label}.dashboard_1w.{field} is invalid")
        if kind == "periodic":
            payload = item.get("live_payload")
            if not isinstance(payload, dict):
                raise ReportError(f"{label}.live_payload must be an object")
            tag = payload.get("tag") or ""
            if not isinstance(tag, str):
                raise ReportError(f"{label}.live_payload.tag must be a string")
            if tag:
                _grammar_string(tag, f"{label}.live_payload.tag", PAYLOAD_TAG_RE)
            _grammar_string(payload.get("phase"), f"{label}.live_payload.phase", PHASE_RE)
            for field in ("verification_state", "impact"):
                _bounded_string(payload.get(field), f"{label}.live_payload.{field}", 100)
            for field in ("verification_url", "release_status_url"):
                url = payload.get(field) or ""
                if not isinstance(url, str):
                    raise ReportError(f"{label}.live_payload.{field} must be a string")
                if url:
                    (
                        validate_verification_url(url)
                        if field == "verification_url"
                        else validate_public_url(url)
                    )
            uncertainty = payload.get("uncertainty") or ""
            if not isinstance(uncertainty, str) or len(uncertainty) > 1000:
                raise ReportError(f"{label}.live_payload.uncertainty is invalid")
            _validate_trend(item.get("trend"), f"{label}.trend")
        elif item.get("live_payload") is not None or item.get("trend") is not None:
            raise ReportError(f"{label} presubmit payload and trend must be null")
        runs = item.get("runs")
        if not isinstance(runs, list) or len(runs) > MAX_SAMPLE_RUNS:
            raise ReportError(f"{label}.runs must be a bounded list")
        for run_index, run in enumerate(runs):
            _validate_candidate_run(run, kind, f"{label}.runs[{run_index}]")
        _validate_string_list(
            item.get("coverage_uncertainties"),
            f"{label}.coverage_uncertainties",
            20,
            500,
        )
    if len(ids) != len(set(ids)):
        raise ReportError("candidate IDs must be present and unique")
    if collection_identifier != collection_id(document):
        raise ReportError("collection_id does not match the candidate evidence")
    return document


def validate_judgments(
    document: Any, candidate_kinds: dict[str, str]
) -> dict[str, dict[str, Any]]:
    if (
        not isinstance(document, dict)
        or document.get("schema_version") != SCHEMA_VERSION
    ):
        raise ReportError(f"judgments schema_version must be {SCHEMA_VERSION}")
    judgments = document.get("judgments")
    if not isinstance(judgments, list):
        raise ReportError("judgments must be a list")
    by_id: dict[str, dict[str, Any]] = {}
    for judgment in judgments:
        if not isinstance(judgment, dict):
            raise ReportError("each judgment must be an object")
        identifier = judgment.get("candidate_id")
        if not isinstance(identifier, str) or identifier in by_id:
            raise ReportError("judgment candidate IDs must be present and unique")
        classification = judgment.get("classification")
        kind = candidate_kinds.get(identifier)
        if classification not in set().union(*KIND_JUDGMENTS.values()):
            raise ReportError(f"unsupported classification for {identifier}")
        if kind and classification not in KIND_JUDGMENTS[kind]:
            raise ReportError(f"unsupported {kind} classification for {identifier}")
        for field in ("summary", "signature", "next_action"):
            if not isinstance(judgment.get(field), str) or len(judgment[field]) > 1000:
                raise ReportError(
                    f"{field} must be a string of at most 1000 characters"
                )
        evidence = judgment.get("recurring_evidence")
        if (
            not isinstance(evidence, list)
            or len(evidence) > 5
            or any(not isinstance(item, str) or len(item) > 300 for item in evidence)
        ):
            raise ReportError("recurring_evidence must contain at most five strings")
        if classification in {"incident_candidate", "permafail_candidate"}:
            if any(not judgment[field].strip() for field in ("summary", "signature", "next_action")):
                raise ReportError(
                    f"{classification} requires summary, signature, and next_action evidence"
                )
            if not evidence or any(not item.strip() for item in evidence):
                raise ReportError(
                    f"{classification} requires non-empty recurring_evidence"
                )
        tracking = judgment.get("tracking")
        if not isinstance(tracking, dict) or tracking.get("status") not in {
            "existing",
            "gap",
            "none",
        }:
            raise ReportError("tracking status must be existing, gap, or none")
        if tracking["status"] == "existing":
            if tracking.get("verified") is not True or not TRACKING_KEY_RE.fullmatch(
                str(tracking.get("key") or "")
            ):
                raise ReportError(
                    "existing tracking requires a verified OCPBUGS/CNTRLPLANE key"
                )
        elif tracking.get("key"):
            raise ReportError("tracking gaps and none must not include an issue key")
        if tracking["status"] != "none" and classification != "incident_candidate":
            raise ReportError(
                "tracking existing/gap is legal only for an incident_candidate"
            )
        by_id[identifier] = judgment
    expected_ids = set(candidate_kinds)
    if set(by_id) != expected_ids:
        missing = sorted(expected_ids - set(by_id))
        extra = sorted(set(by_id) - expected_ids)
        raise ReportError(
            f"judgment IDs do not match candidates; missing={missing}, extra={extra}"
        )
    return by_id


def _format_rate(window: dict[str, Any]) -> str:
    rate = window["rate"]
    percentage = "No data" if rate is None else f"{rate * 100:.1f}%"
    return (
        f"{window['SUCCESS']}/{window['denominator']} ({percentage}); "
        f"ABORTED {window['ABORTED']}, ERROR {window['ERROR']}"
    )


def _candidate_lines(candidate: dict[str, Any], judgment: dict[str, Any]) -> list[str]:
    classification = judgment["classification"].replace("_", " ").title()
    role = candidate["configured_role"]
    platforms = ", ".join(escape_slack(item, 100) for item in role["platforms"]) or "none"
    lines = [
        f"• *Job:* {escape_slack(candidate['job_id'], 255)} — *{classification}*",
        (
            f"  ◦ Candidate: {candidate['candidate_id']} · deterministic trigger: "
            f"{escape_slack(candidate['deterministic_trigger'], 100)}"
        ),
    ]
    if candidate["kind"] == "periodic":
        lines.append(
            "  ◦ Configured role: blocking verification "
            f"{escape_slack(role['verification'], 255)} · stream "
            f"{escape_slack(role['stream'], 255)} ({escape_slack(role['architecture'], 50)} "
            f"{escape_slack(role['stream_kind'], 50)})"
        )
    else:
        lines.append(
            f"  ◦ Configured role: required presubmit for {escape_slack(candidate['branch'], 80)}"
        )
    lines.append(
        f"  ◦ Platform/framework: {platforms} / {escape_slack(role['framework'], 255)}"
    )
    dashboard = candidate["dashboard_1w"]
    if dashboard["status"] == "available":
        dashboard_rate = dashboard.get("rate")
        rate_text = "No testable data" if dashboard_rate is None else f"{dashboard_rate:.1f}%"
        lines.append(
            f"  ◦ Dashboard 1w: {rate_text} · runs {dashboard.get('runs')} · "
            f"fails {dashboard.get('fails')} (test {dashboard.get('test_fails')}, "
            f"infra {dashboard.get('infra_fails')})"
        )
    else:
        lines.append("  ◦ Dashboard 1w: No dashboard data")
    if candidate["kind"] == "periodic":
        trend = candidate["trend"]
        change = trend["change_points"]
        change_text = "n/a" if change is None else f"{change:+.1f} pp"
        lines.append(
            f"  ◦ Exact trend ({escape_slack(trend['classification'], 40)}, {change_text}): "
            f"current {_format_rate(trend['current'])}; baseline {_format_rate(trend['baseline'])}"
        )
        payload = candidate["live_payload"]
        payload_text = (
            f"{escape_slack(payload['tag'] or 'Unknown', 255)} · "
            f"phase {escape_slack(payload['phase'], 64)} · verification "
            f"{escape_slack(payload['verification_state'], 100)} · impact "
            f"{escape_slack(payload['impact'], 100)}"
        )
        payload_links = []
        if payload["release_status_url"]:
            payload_links.append(f"<{payload['release_status_url']}|release status>")
        if payload["verification_url"]:
            payload_links.append(f"<{payload['verification_url']}|verification run>")
        if payload_links:
            payload_text += " · " + ", ".join(payload_links)
        lines.append(f"  ◦ Live payload: {payload_text}")
        if payload.get("uncertainty"):
            lines.append(
                f"  ◦ Payload uncertainty: {escape_slack(payload['uncertainty'], 1000)}"
            )
    lines.append(f"  ◦ Summary: {escape_slack(judgment['summary'])}")
    lines.append(
        f"  ◦ Verified signature: {escape_slack(judgment['signature']) or 'None verified'}"
    )
    for evidence in judgment["recurring_evidence"]:
        lines.append(f"  ◦ Recurring evidence: {escape_slack(evidence, 300)}")
    if candidate["runs"]:
        lines.append("  ◦ Ordered timestamped runs:")
        for run in candidate["runs"]:
            timestamp = run.get("timestamp") or run.get("completed")
            run_label = f"{escape_slack(run['id'], 255)} {escape_slack(run['state'], 40)}"
            run_url = run.get("url") or ""
            run_identity = f"<{run_url}|{run_label}>" if run_url else run_label
            suffix = f" · {timestamp}"
            if run.get("head_sha"):
                suffix += f" · head {escape_slack(run['head_sha'], 64)}"
            if run.get("payload_tag"):
                suffix += f" · payload {escape_slack(run['payload_tag'], 255)}"
            lines.append(f"    ◦ {run_identity}{suffix}")
    else:
        lines.append("  ◦ Ordered timestamped runs: None available")
    uncertainties = list(candidate["coverage_uncertainties"])
    if uncertainties:
        for uncertainty in uncertainties:
            lines.append(f"  ◦ Coverage uncertainty: {escape_slack(uncertainty, 500)}")
    else:
        lines.append("  ◦ Coverage uncertainty: None recorded")
    tracking = judgment["tracking"]
    if tracking["status"] == "existing":
        tracking_text = f"{tracking['key']} (verified existing)"
    elif tracking["status"] == "gap":
        tracking_text = "Tracking issue needed — human follow-up only"
    else:
        tracking_text = "None"
    lines.append(f"  ◦ Tracking: {escape_slack(tracking_text)}")
    lines.append(f"  ◦ Next action: {escape_slack(judgment['next_action'])}")
    return lines


def _pack_replies(key: str, entries: list[list[str]]) -> list[str]:
    header = f"*{escape_slack(key)}*"
    continuation = f"*{escape_slack(key)}* _(continued)_"
    replies: list[str] = []
    lines = [header, ""]
    for entry in entries:
        for line in [*entry, ""]:
            candidate = "\n".join([*lines, line]).rstrip()
            if len(candidate) <= THREAD_REPLY_LIMIT:
                lines.append(line)
                continue
            reply = "\n".join(lines).rstrip()
            if not reply or len(reply) > THREAD_REPLY_LIMIT:
                raise ReportError(f"thread evidence line exceeds {THREAD_REPLY_LIMIT} characters")
            replies.append(reply)
            lines = [continuation, "", line]
            if len("\n".join(lines).rstrip()) > THREAD_REPLY_LIMIT:
                raise ReportError(f"thread evidence line exceeds {THREAD_REPLY_LIMIT} characters")
    final = "\n".join(lines).rstrip()
    if final:
        replies.append(final)
    return replies


def render_report(stage_one: str, candidates_doc: Any, judgments_doc: Any) -> str:
    if len(stage_one) > STAGE_ONE_LIMIT:
        raise ReportError(f"stage-one message exceeds {STAGE_ONE_LIMIT} characters")
    candidates_doc = validate_candidates_document(candidates_doc)
    if f"evidence {candidates_doc['collection_id']}" not in stage_one:
        raise ReportError("stage-one message does not match the candidate evidence set")
    candidates = candidates_doc["candidates"]
    judgments = validate_judgments(
        judgments_doc,
        {item["candidate_id"]: item["kind"] for item in candidates},
    )
    if not candidates:
        if candidates_doc["scope"]["state"] == "complete":
            summary = "*No candidate judgments required* — deterministic collection found no candidates."
        else:
            summary = (
                "*No candidate judgments requested* — collector scope is Unknown; "
                "review the parent coverage warning."
            )
        return stage_one + "\n\n---THREAD_DETAILS---\n" + summary

    grouped: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]] = defaultdict(list)
    order = []
    for candidate in candidates:
        key = (
            f"OCP {candidate['release']}"
            if candidate["kind"] == "periodic"
            else f"{candidate['branch']} → OCP {candidate['release']}"
        )
        if key not in grouped:
            order.append(key)
        grouped[key].append((candidate, judgments[candidate["candidate_id"]]))

    replies = []
    overflow = candidates_doc["scope"].get("candidate_overflow")
    if overflow:
        replies.append(
            "*Coverage Unknown* — rendered "
            f"{overflow['published']} of {overflow['total']} candidates; "
            f"{overflow['omitted']} omitted by the explicit {escape_slack(overflow['priority'], 200)} policy."
        )
    for key in order:
        entries = [
            _candidate_lines(candidate, judgment)
            for candidate, judgment in grouped[key]
        ]
        replies.extend(_pack_replies(key, entries))
    if any(len(reply) > THREAD_REPLY_LIMIT for reply in replies):
        raise ReportError("renderer produced an oversized thread reply")
    return (
        stage_one
        + "\n\n---THREAD_DETAILS---\n"
        + "\n\n---THREAD_BREAK---\n".join(replies)
    )


def load_json(path: Path, label: str) -> Any:
    try:
        return json.loads(path.read_text())
    except OSError as exc:
        raise ReportError(f"unable to read {label}: {path}") from exc
    except json.JSONDecodeError as exc:
        raise ReportError(f"invalid JSON in {label}: {path}") from exc


def collect_command(args: argparse.Namespace) -> int:
    as_of = parse_rfc3339(args.as_of)
    transport: HTTPTransport | FixtureTransport
    if args.fixture_dir:
        transport = FixtureTransport(Path(args.fixture_dir))
    else:
        transport = HTTPTransport(timeout=args.timeout)
    stage_one, candidates = Collector(
        transport,
        as_of,
        deadline_seconds=args.collection_deadline,
        max_requests=args.max_requests,
    ).collect()
    atomic_write(Path(args.slack_out), stage_one + "\n")
    atomic_write(
        Path(args.candidates_out),
        json.dumps(candidates, indent=2, sort_keys=True) + "\n",
    )
    return 0


def render_command(args: argparse.Namespace) -> int:
    try:
        stage_one = Path(args.stage_one).read_text().rstrip("\n")
    except OSError as exc:
        raise ReportError(f"unable to read stage-one file: {args.stage_one}") from exc
    candidates = load_json(Path(args.candidates), "candidates")
    judgments = load_json(Path(args.judgments), "judgments")
    report = render_report(stage_one, candidates, judgments)
    atomic_write(Path(args.slack_out), report + "\n")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    collect = subparsers.add_parser(
        "collect", help="collect deterministic public CI evidence"
    )
    collect.add_argument(
        "--as-of", required=True, help="report time as timezone-aware RFC3339"
    )
    collect.add_argument(
        "--slack-out", required=True, help="stage-one Slack text output"
    )
    collect.add_argument(
        "--candidates-out", required=True, help="bounded candidate JSON output"
    )
    collect.add_argument(
        "--timeout", type=float, default=20.0, help="per-request timeout in seconds"
    )
    collect.add_argument(
        "--collection-deadline",
        type=float,
        default=COLLECTION_DEADLINE_SECONDS,
        help="overall collection deadline in seconds",
    )
    collect.add_argument(
        "--max-requests",
        type=int,
        default=MAX_COLLECTION_REQUESTS,
        help="overall public request budget",
    )
    collect.add_argument(
        "--fixture-dir", help="offline regex-manifest fixture directory"
    )
    collect.set_defaults(func=collect_command)

    render = subparsers.add_parser(
        "render", help="render validated LLM judgments as thread replies"
    )
    render.add_argument(
        "--stage-one", required=True, help="stage-one Slack text from collect"
    )
    render.add_argument(
        "--candidates", required=True, help="candidate JSON from collect"
    )
    render.add_argument(
        "--judgments", required=True, help="schema-versioned LLM judgments JSON"
    )
    render.add_argument(
        "--slack-out", required=True, help="combined Slack/thread text output"
    )
    render.set_defaults(func=render_command)
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return int(args.func(args))
    except ReportError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
