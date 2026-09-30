#!/usr/bin/env python3
"""Build the deterministic portions of the HyperShift CI daily health report.

The CLI is the deterministic tool chaibot drives; it never writes Slack or Jira:

* ``collect`` reads the CI Health dashboard (and the release controller only for the
  live payload phase), classifies every job on two axes, and writes a bounded,
  schema-versioned data document plus an optional HTML trend report.
* ``report`` re-renders the HTML trend report from a previously written data document.

Axis A is the permafailing state (presubmit streaks from Prow; binary periodic
permafail from the dashboard sparkline) that drives blockers and the proposed
incident set; Axis B is the job-health SLO (rate vs the threshold) plus the
week-over-week trend. Only presubmit candidates need LLM judgment; chaibot composes
the Slack messages and does all Jira after approval.

All tunable thresholds live in config.toml next to this script.
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
import string
import subprocess
import sys
import tempfile
import threading
import time
import tomllib
import urllib.error
import urllib.parse
import urllib.request
from collections import defaultdict
from collections.abc import Iterable
from pathlib import Path
from typing import Any

def _load_config() -> dict[str, Any]:
    """Load and validate the single-source-of-truth config beside this script.

    Every tunable (sources, scope, classification knobs, trend, job-health SLO,
    incident/Jira criteria, limits) is defined in config.toml so a change is a one-file
    PR. Uses the stdlib tomllib (no extra dependency). Only the knobs the deterministic
    script itself reads are validated here; the [incident] knobs, the fix-or-retire
    horizon, and [jira] are chaibot's (read from config.toml by the prompt), so the
    Jira-blind script neither reads nor validates them.
    """
    path = Path(__file__).resolve().with_name("config.toml")
    try:
        with path.open("rb") as handle:
            cfg = tomllib.load(handle)
    except (OSError, tomllib.TOMLDecodeError) as exc:  # pragma: no cover - startup
        raise RuntimeError(f"unable to load config {path}: {exc}") from exc
    _validate_config(cfg, path)
    return cfg


_INT: tuple[type, ...] = (int,)
_NUM: tuple[type, ...] = (int, float)
_STR: tuple[type, ...] = (str,)

# Section -> {key: accepted types}. Only what the script reads (see _load_config).
_CONFIG_SCHEMA: dict[str, dict[str, tuple[type, ...]]] = {
    "sources": {
        "dashboard_base": _STR,
        "release_controller_base": _STR,
        "multi_release_controller_base": _STR,
        "amd64_release_status_base": _STR,
        "sippy_runs_url": _STR,
        "prow_host": _STR,
        "public_results_bucket": _STR,
    },
    "scope": {"supported_predecessors": _INT},
    "presubmit": {
        "post_green_red_tolerance": _INT,
        "flake_horizon_hours": _NUM,
        "permafail_duration_hours": _NUM,
        "permafail_volume": _INT,
    },
    "periodic": {
        "permafail_duration_hours": _NUM,
        "permafail_volume": _INT,
        "flaky_threshold_percent": _NUM,
    },
    "trend": {"min_window_runs": _INT, "change_threshold_points": _NUM},
    "job_health": {"slo_pass_rate_percent": _NUM},
    "limits": {
        "max_http_bytes": _INT,
        "max_history_pages": _INT,
        "max_candidates": _INT,
        "max_sample_runs": _INT,
        "max_source_failures": _INT,
        "max_workers": _INT,
        "max_collection_requests": _INT,
        "collection_deadline_seconds": _NUM,
        "max_source_jobs": _INT,
        "max_source_string": _INT,
        "max_candidate_document_bytes": _INT,
        "max_prow_duration_ns": _INT,
        "max_metric_count": _INT,
    },
}
_CONFIG_TOP: dict[str, tuple[type, ...]] = {"schema_version": _INT, "repository": _STR}


def _validate_config(cfg: dict[str, Any], path: Path) -> None:
    """Fail fast with a clear message naming the offending key (never a raw traceback)."""

    def check(value: Any, where: str, types: tuple[type, ...]) -> None:
        # bool is an int subclass; reject it for every scalar knob.
        if isinstance(value, bool) or not isinstance(value, types):
            wanted = " or ".join(t.__name__ for t in types)
            raise RuntimeError(f"config {path}: {where} must be {wanted}")

    for key, types in _CONFIG_TOP.items():
        if key not in cfg:
            raise RuntimeError(f"config {path}: {key} is missing")
        check(cfg[key], key, types)
    for section, keys in _CONFIG_SCHEMA.items():
        block = cfg.get(section)
        if not isinstance(block, dict):
            raise RuntimeError(f"config {path}: [{section}] is missing or not a table")
        for key, types in keys.items():
            if key not in block:
                raise RuntimeError(f"config {path}: [{section}].{key} is missing")
            check(block[key], f"[{section}].{key}", types)


_CONFIG = _load_config()
_SOURCES = _CONFIG["sources"]
_LIMITS = _CONFIG["limits"]
_PRESUBMIT = _CONFIG["presubmit"]
_PERIODIC = _CONFIG["periodic"]
_TREND = _CONFIG["trend"]
_JOB_HEALTH = _CONFIG["job_health"]

SCHEMA_VERSION = _CONFIG["schema_version"]
REPOSITORY = _CONFIG["repository"]

DASHBOARD_BASE = _SOURCES["dashboard_base"]
RELEASE_CONTROLLER_BASE = _SOURCES["release_controller_base"]
MULTI_RELEASE_CONTROLLER_BASE = _SOURCES["multi_release_controller_base"]
AMD64_RELEASE_STATUS_BASE = _SOURCES["amd64_release_status_base"]
SIPPY_RUNS_URL = _SOURCES["sippy_runs_url"]
PROW_HOST = _SOURCES["prow_host"]
PUBLIC_RESULTS_BUCKET = _SOURCES["public_results_bucket"]

MAX_HTTP_BYTES = _LIMITS["max_http_bytes"]
MAX_HISTORY_PAGES = _LIMITS["max_history_pages"]
MAX_CANDIDATES = _LIMITS["max_candidates"]
MAX_SAMPLE_RUNS = _LIMITS["max_sample_runs"]
MAX_SOURCE_FAILURES = _LIMITS["max_source_failures"]
MAX_WORKERS = _LIMITS["max_workers"]
MAX_COLLECTION_REQUESTS = _LIMITS["max_collection_requests"]
COLLECTION_DEADLINE_SECONDS = _LIMITS["collection_deadline_seconds"]
MAX_SOURCE_JOBS = _LIMITS["max_source_jobs"]
MAX_SOURCE_STRING = _LIMITS["max_source_string"]
MAX_CANDIDATE_DOCUMENT_BYTES = _LIMITS["max_candidate_document_bytes"]
MAX_PROW_DURATION_NS = _LIMITS["max_prow_duration_ns"]
MAX_METRIC_COUNT = _LIMITS["max_metric_count"]

# Supported release window. Scope discovery uses the dashboard's own ascending release
# list and N-x ordering; this is how many predecessors past N (the dev release) we still
# support (N-4 => 4). Jira label/component and the incident knobs are chaibot's (read from
# config.toml by the prompt), so the Jira-blind script does not load them.
SUPPORTED_PREDECESSORS = _CONFIG["scope"]["supported_predecessors"]

# Presubmit merge-queue classification knobs (Design A).
POST_GREEN_RED_TOLERANCE = _PRESUBMIT["post_green_red_tolerance"]
FLAKE_HORIZON_HOURS = _PRESUBMIT["flake_horizon_hours"]
PERMAFAIL_DURATION_HOURS = _PRESUBMIT["permafail_duration_hours"]
PERMAFAIL_VOLUME = _PRESUBMIT["permafail_volume"]

# Periodic permafail knobs (Design B, Axis A) — binary; permafailing = failures since the
# last pass >= volume AND streak span >= duration. No candidate stage, no veto knob.
PERIODIC_PERMAFAIL_DURATION_HOURS = _PERIODIC["permafail_duration_hours"]
PERIODIC_PERMAFAIL_VOLUME = _PERIODIC["permafail_volume"]
PERIODIC_FLAKY_THRESHOLD_PERCENT = _PERIODIC["flaky_threshold_percent"]

# Week-over-week trend knobs.
TREND_MIN_WINDOW_RUNS = _TREND["min_window_runs"]
TREND_CHANGE_THRESHOLD_POINTS = _TREND["change_threshold_points"]

# Job-health SLO (Axis B). fix_or_retire_horizon_days and the [incident]/[jira] knobs are
# chaibot's (read from config.toml by the prompt); the Jira-blind script does not load them.
SLO_PASS_RATE_PERCENT = _JOB_HEALTH["slo_pass_rate_percent"]

ALLOWED_HOSTS = {
    urllib.parse.urlparse(DASHBOARD_BASE).hostname,
    urllib.parse.urlparse(RELEASE_CONTROLLER_BASE).hostname,
    urllib.parse.urlparse(MULTI_RELEASE_CONTROLLER_BASE).hostname,
    urllib.parse.urlparse(SIPPY_RUNS_URL).hostname,
    PROW_HOST,
    urllib.parse.urlparse(AMD64_RELEASE_STATUS_BASE).hostname,
    *_SOURCES.get("extra_allowed_hosts", []),
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

JOB_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$")
PAYLOAD_TAG_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$")
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
        raise ReportError(
            f"{label} must be a non-empty string of at most {limit} characters"
        )
    if any(character in value for character in ("\r", "\n", "\x00")):
        raise ReportError(f"{label} contains a control character")
    return value


def _grammar_string(value: Any, label: str, pattern: re.Pattern[str]) -> str:
    text = _bounded_string(value, label, 255)
    if not pattern.fullmatch(text):
        raise ReportError(f"{label} has an invalid format")
    return text


def _bounded_freeform(value: Any, label: str, limit: int) -> str:
    """Bounded multi-line free-form text (newlines/tabs allowed; escaped on render)."""
    if not isinstance(value, str):
        raise ReportError(f"{label} must be a string")
    if len(value) > limit:
        raise ReportError(f"{label} exceeds {limit} characters")
    if "\x00" in value or any(ord(ch) < 32 and ch not in "\n\t" for ch in value):
        raise ReportError(f"{label} contains a control character")
    return value.replace("\r\n", "\n").replace("\r", "\n")


def validate_source_revision(value: Any) -> str:
    """Require the canonical commit identity bound into both report stages."""
    return _grammar_string(value, "source_revision", GIT_SHA_RE)


def checked_out_source_revision() -> str:
    """Return the commit containing the companion script, without accepting aliases."""
    repository = Path(__file__).resolve().parents[3]
    try:
        completed = subprocess.run(
            ["git", "rev-parse", "--verify", "HEAD^{commit}"],
            cwd=repository,
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        raise ReportError("unable to resolve checked-out source revision") from exc
    if completed.returncode != 0:
        raise ReportError("unable to resolve checked-out source revision")
    return validate_source_revision(completed.stdout.strip())


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
            self.deadline - time.monotonic()
            if self.deadline is not None
            else self.timeout
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
                            raise ReportError(
                                f"missing fixture {item['file']}"
                            ) from exc
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
    decoded_path = urllib.parse.unquote(path)
    segments = [part for part in decoded_path.split("/") if part]
    if any(part in {".", ".."} for part in segments) or "\\" in decoded_path:
        raise ReportError("refusing noncanonical public-source path")
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
        allowed_path = (
            path == "/"
            or path == "/api/v1/releasestreams/all"
            or path.startswith(("/api/v1/releasestream/", "/releasestream/"))
        )
    elif parsed.hostname == urllib.parse.urlparse(SIPPY_RUNS_URL).hostname:
        allowed_path = path == "/api/jobs/runs" or path.startswith("/sippy-ng/")
    elif parsed.hostname == PROW_HOST:
        allowed_path = path.startswith(
            "/job-history/gs/test-platform-results/"
        ) or bool(
            re.match(
                r"^/view/gs/(?:test-platform-results|test-platform-results-public)/",
                path,
            )
        )
    elif parsed.hostname == "openshift-release.apps.ci.l2s4.p1.openshiftapps.com":
        allowed_path = path.startswith("/releasestream/")
    if parsed.hostname == "storage.googleapis.com":
        if not segments or segments[0] != PUBLIC_RESULTS_BUCKET:
            raise ReportError("refusing non-public Prow results bucket")
        allowed_path = True
    if not allowed_path:
        raise ReportError(f"refusing unexpected public-source path: {path}")


def validate_release_status_url(url: str, status_base: str, stream: str) -> None:
    """Bind a release-status link to one controller and exact stream."""
    validate_public_url(url)
    expected = f"{status_base}/releasestream/{urllib.parse.quote(stream, safe='')}"
    if url != expected:
        raise ReportError("release-status URL does not match its controller stream")


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


def parse_sparkline_slot(value: Any) -> dt.datetime:
    """Parse a dashboard sparkline slot label ('YYYY-MM-DD HH:MM', UTC).

    The dashboard formats slot timestamps this way (not RFC3339); they label the
    per-slot buckets aligned to each job's sparkline.
    """
    if not isinstance(value, str):
        raise ReportError("sparkline slot must be a string")
    match = re.fullmatch(r"(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2})", value)
    if not match:
        raise ReportError("sparkline slot must be 'YYYY-MM-DD HH:MM'")
    year, month, day, hour, minute = (int(part) for part in match.groups())
    try:
        return dt.datetime(year, month, day, hour, minute, tzinfo=dt.timezone.utc)
    except ValueError as exc:
        raise ReportError(f"invalid sparkline slot: {value}") from exc


def release_key(value: str) -> tuple[int, int]:
    match = re.fullmatch(r"(\d+)\.(\d+)", value or "")
    if not match:
        raise ReportError(f"invalid release major/minor: {value!r}")
    return int(match.group(1)), int(match.group(2))


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


def load_json(path: Path, label: str) -> Any:
    """Read and parse a bounded JSON file, raising ReportError on any problem."""
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise ReportError(f"unable to read {label} {path}: {exc}") from exc
    if len(raw) > MAX_CANDIDATE_DOCUMENT_BYTES:
        raise ReportError(
            f"{label} {path} exceeds the {MAX_CANDIDATE_DOCUMENT_BYTES}-byte bound"
        )
    try:
        return json.loads(raw)
    except (ValueError, UnicodeDecodeError) as exc:
        raise ReportError(f"{label} {path} is not valid JSON: {exc}") from exc


def _validate_metric(value: Any, label: str, minimum: float, maximum: float) -> None:
    if (
        isinstance(value, bool)
        or not isinstance(value, (int, float))
        or not math.isfinite(value)
        or not minimum <= value <= maximum
    ):
        raise ReportError(
            f"{label} must be finite and between {minimum:g} and {maximum:g}"
        )


def _validate_health_row(row: Any, label: str, nested: bool) -> None:
    """Validate one health-report/v2 row (presubmit or nested periodic)."""
    if not isinstance(row, dict):
        raise ReportError(f"{label} is not an object")
    for identity in ("id", "prow"):
        if row.get(identity) is not None and not isinstance(row[identity], str):
            raise ReportError(f"{label} {identity} must be a string")
    for percent in ("rate", "prev", "trend"):
        value = row.get(percent)
        if value is not None:
            low, high = (-100, 100) if percent == "trend" else (0, 100)
            _validate_metric(value, f"{label} {percent}", low, high)
    for metric in ("runs", "fails", "test_fails", "infra_fails", "prev_runs", "spark_runs"):
        value = row.get(metric)
        if value is not None and (
            isinstance(value, bool)
            or not isinstance(value, int)
            or not 0 <= value <= MAX_METRIC_COUNT
        ):
            raise ReportError(f"{label} {metric} is out of range")
    sparkline = row.get("sparkline")
    if sparkline is not None:
        if not isinstance(sparkline, list) or len(sparkline) > MAX_SOURCE_JOBS:
            raise ReportError(f"{label} sparkline is not a bounded list")
        for slot in sparkline:
            if slot is None:
                continue
            if (
                not isinstance(slot, list)
                or len(slot) != 4
                or any(
                    isinstance(v, bool)
                    or not isinstance(v, int)
                    or not 0 <= v <= MAX_METRIC_COUNT
                    for v in slot
                )
            ):
                raise ReportError(f"{label} sparkline slot has an unexpected shape")
    if nested:
        periodics = row.get("periodics")
        if periodics is not None:
            if not isinstance(periodics, list) or len(periodics) > MAX_SOURCE_JOBS:
                raise ReportError(f"{label} periodics is not a bounded list")
            for index, periodic in enumerate(periodics):
                _validate_health_row(periodic, f"{label} periodics[{index}]", nested=False)


def validate_health(health: Any) -> dict[str, Any]:
    """Validate the live health-report/v2 envelope and its four data sections."""
    if not isinstance(health, dict) or not isinstance(health.get("data"), dict):
        raise ReportError("dashboard health data is not an object")
    releases = health.get("releases")
    if releases is not None and (
        not isinstance(releases, list) or len(releases) > MAX_SOURCE_JOBS
    ):
        raise ReportError("dashboard health releases is not a bounded list")
    data = health["data"]
    slots = data.get("sparkline_slots") or []
    if not isinstance(slots, list) or len(slots) > MAX_SOURCE_JOBS:
        raise ReportError("dashboard health sparkline_slots is not a bounded list")
    for slot in slots:
        if not isinstance(slot, str) or len(slot) > 64:
            raise ReportError("dashboard health sparkline_slots must contain strings")
    for section, nested in (
        ("jobs", True),
        ("payload_blocking_jobs", False),
        ("component_readiness_jobs", False),
    ):
        rows = data.get(section) or []
        if not isinstance(rows, list) or len(rows) > MAX_SOURCE_JOBS:
            raise ReportError(f"dashboard health {section} is not a bounded list")
        for index, row in enumerate(rows):
            _validate_health_row(
                row, f"dashboard health {section} row {index}", nested=nested
            )
    alerts = data.get("alerts") or []
    if not isinstance(alerts, list) or len(alerts) > MAX_SOURCE_JOBS:
        raise ReportError("dashboard health alerts is not a bounded list")
    return health


def health_sparkline_slots(health: dict[str, Any]) -> list[str]:
    slots = (health.get("data") or {}).get("sparkline_slots") or []
    return [slot for slot in slots if isinstance(slot, str)]


def normalize_state(value: Any) -> str:
    return RESULT_STATES.get(str(value or "").upper(), str(value or "UNKNOWN").upper())


# Note: the old Sippy 24h/7d calculate_trend and the select_presubmit_trigger
# heuristic were replaced by classify_wow_trend and classify_presubmit_streak
# (Design A/B), defined below.


# --- Deterministic classification (Design A/B + job-health SLO) --------------
# These pure helpers turn ordered run history and dashboard week-over-week rates
# into deterministic verdicts. The LLM only resolves the presubmit CANDIDATE
# bucket; every other verdict here is code, not language. All thresholds come
# from config.toml (see the module constants loaded at import).


def wilson_interval(successes: int, total: int, z: float = 1.96) -> tuple[float, float]:
    """Two-sided Wilson score interval for a binomial proportion (fraction 0..1)."""
    if total <= 0:
        return (0.0, 1.0)
    successes = min(max(int(successes), 0), int(total))
    proportion = successes / total
    denominator = 1.0 + z * z / total
    center = (proportion + z * z / (2 * total)) / denominator
    margin = (
        z * math.sqrt(proportion * (1 - proportion) / total + z * z / (4 * total * total))
    ) / denominator
    return (max(0.0, center - margin), min(1.0, center + margin))


def _streak_length(ordered: list[dict[str, Any]], state_of) -> list[dict[str, Any]]:
    """Consecutive FAILURE runs from newest; ERROR/ABORTED are neutral (skipped)."""
    streak: list[dict[str, Any]] = []
    for run in ordered:
        state = state_of(run)
        if state in {"ERROR", "ABORTED"}:
            continue
        if state == "FAILURE":
            streak.append(run)
        else:  # SUCCESS breaks the streak
            break
    return streak


def classify_presubmit_streak(
    runs: Iterable[dict[str, Any]], as_of: dt.datetime
) -> dict[str, Any]:
    """Streak-based merge-queue class (Design A): the impact, deterministically.

    r = consecutive reds since the last SUCCESS (ERROR/ABORTED neutral).
      no testable runs             -> unknown
      r <= S                       -> not_flagged
      r >= V and span >= D hours   -> permafailing (merge-queue blocker)
      otherwise                    -> candidate (LLM resolves the cause)
    """
    ordered = sorted(
        runs, key=lambda run: parse_rfc3339(run["completed"]), reverse=True
    )
    state_of = lambda run: str(run.get("state") or "")  # noqa: E731
    testable = [run for run in ordered if state_of(run) in {"SUCCESS", "FAILURE"}]
    infra_aborts = sum(1 for run in ordered if state_of(run) in {"ERROR", "ABORTED"})
    if not testable:
        return {
            "classification": "unknown",
            "streak": 0,
            "span_hours": 0.0,
            "distinct_heads": 0,
            "shade": None,
            "testable": 0,
            "infra_aborts": infra_aborts,
        }
    streak = _streak_length(ordered, state_of)
    span_hours = (
        (as_of - parse_rfc3339(streak[-1]["completed"])).total_seconds() / 3600.0
        if streak
        else 0.0
    )
    distinct_heads = len(
        {run.get("head_sha") for run in streak if run.get("head_sha")}
    )
    length = len(streak)
    shade = None
    if length <= POST_GREEN_RED_TOLERANCE:
        classification = "not_flagged"
    elif length >= PERMAFAIL_VOLUME and span_hours >= PERMAFAIL_DURATION_HOURS:
        classification = "permafailing"
    else:
        classification = "candidate"
        shade = "fresh" if span_hours < FLAKE_HORIZON_HOURS else "persistent"
    return {
        "classification": classification,
        "streak": length,
        "span_hours": round(span_hours, 2),
        "distinct_heads": distinct_heads,
        "shade": shade,
        "testable": len(testable),
        "infra_aborts": infra_aborts,
    }


def classify_periodic_sparkline(
    sparkline: Any, slots: list[str], as_of: dt.datetime
) -> dict[str, Any]:
    """Binary periodic permafail (Design B, Axis A) from the ordered run list.

    Each slot is [runs, passes, test_fails, infra_fails]; infra_fails are neutral
    (a /retest tax), so slot-testable = passes + test_fails. Walk the sparkline aligned
    to its slot timestamps; r = failures since the last pass (a slot with any pass ends
    the failing streak), span = as_of - the oldest failing slot. Decoupled from payload
    status; this is not the SLO/health axis.
      r >= V_periodic AND span >= D_periodic hours -> permafailing
      otherwise (with testable history)            -> not_permafailing
    Fail-safe: a missing/empty sparkline, a sparkline whose length does not match its
    slot timestamps, unparseable slots, or no testable history -> unknown (never green).
    """
    unknown = {
        "classification": "unknown",
        "streak_runs": 0,
        "span_hours": 0.0,
        "testable": 0,
        "passes": 0,
    }
    if not isinstance(sparkline, list) or not sparkline:
        return unknown
    if not isinstance(slots, list) or len(sparkline) != len(slots):
        return unknown  # cannot trust an unaligned sparkline; fail to Unknown
    dated: list[tuple[dt.datetime, int, int]] = []
    total_passes = 0
    total_testable = 0
    for slot, stamp in zip(sparkline, slots):
        if slot is None:
            continue  # gap bucket: no data for this slot
        if not isinstance(slot, list) or len(slot) != 4:
            return unknown
        _runs, passes, test_fails, _infra = slot
        testable = passes + test_fails
        total_passes += passes
        total_testable += testable
        try:
            when = parse_sparkline_slot(stamp)
        except ReportError:
            return unknown
        dated.append((when, passes, testable))
    if not dated or total_testable == 0:
        return unknown
    dated.sort(key=lambda entry: entry[0], reverse=True)
    streak_runs = 0
    oldest_red: dt.datetime | None = None
    for _when, passes, testable in dated:
        if testable == 0:
            continue  # infra-only / empty slot is neutral
        if passes == 0:
            streak_runs += testable
            oldest_red = _when
        else:
            break  # any pass ends the failing streak
    span_hours = (as_of - oldest_red).total_seconds() / 3600.0 if oldest_red else 0.0
    permafailing = (
        streak_runs >= PERIODIC_PERMAFAIL_VOLUME
        and span_hours >= PERIODIC_PERMAFAIL_DURATION_HOURS
    )
    return {
        "classification": "permafailing" if permafailing else "not_permafailing",
        "streak_runs": streak_runs,
        "span_hours": round(span_hours, 2),
        "testable": total_testable,
        "passes": total_passes,
    }


def classify_wow_trend(
    rate: float | None,
    prev: float | None,
    runs: Any,
    prev_runs: Any,
) -> dict[str, Any]:
    """Confidence-aware week-over-week trend from dashboard rate vs prev (percent).

    Improving/degrading only when the two weekly Wilson intervals are disjoint AND
    the change exceeds the threshold; otherwise stable. Sparse windows -> insufficient.
    """
    change = None if rate is None or prev is None else rate - prev
    result = {
        "classification": "insufficient",
        "change_points": None if change is None else round(change, 2),
        "rate": rate,
        "prev": prev,
        "runs": runs if isinstance(runs, (int, float)) else None,
        "prev_runs": prev_runs if isinstance(prev_runs, (int, float)) else None,
    }
    if (
        rate is None
        or prev is None
        or not isinstance(runs, (int, float))
        or not isinstance(prev_runs, (int, float))
        or runs < TREND_MIN_WINDOW_RUNS
        or prev_runs < TREND_MIN_WINDOW_RUNS
    ):
        return result
    now_lo, now_hi = wilson_interval(round(rate / 100.0 * runs), int(runs))
    prev_lo, prev_hi = wilson_interval(round(prev / 100.0 * prev_runs), int(prev_runs))
    disjoint = now_lo > prev_hi or prev_lo > now_hi
    if disjoint and change >= TREND_CHANGE_THRESHOLD_POINTS:
        result["classification"] = "improving"
    elif disjoint and change <= -TREND_CHANGE_THRESHOLD_POINTS:
        result["classification"] = "degrading"
    else:
        result["classification"] = "stable"
    return result


def job_below_slo(rate: Any) -> bool:
    """True when a job's dashboard pass rate is below the job-health SLO."""
    return (
        isinstance(rate, (int, float))
        and not isinstance(rate, bool)
        and rate < SLO_PASS_RATE_PERCENT
    )


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


def bound_candidates(
    candidates: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], dict[str, Any] | None]:
    """Apply deterministic count/byte priority without concealing overflow."""
    selected = list(candidates[:MAX_CANDIDATES])
    # Reserve room for bounded scope failures/uncertainties and envelope fields.
    byte_limit = MAX_CANDIDATE_DOCUMENT_BYTES - 224 * 1024
    while (
        selected
        and len(
            json.dumps(
                {"presubmit_candidates": selected}, separators=(",", ":")
            ).encode("utf-8")
        )
        > byte_limit
    ):
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
        "priority": "configured branch order, then job ID",
    }


def bound_messages(messages: Iterable[str], label: str) -> list[str]:
    unique = sorted({str(message) for message in messages})
    bounded = []
    for message in unique[: MAX_SOURCE_FAILURES - 1]:
        if len(message) > 1000:
            message = (
                message[:960].rstrip() + " … message truncated by 1000-character bound"
            )
        bounded.append(message)
    omitted = len(unique) - len(bounded)
    if omitted:
        bounded.append(
            f"{omitted} additional {label} omitted by the explicit count bound"
        )
    return bounded


def _dashboard_has_data(row: dict[str, Any]) -> bool:
    """True when a dashboard row carries real Sippy-backed data, not a 0/no-data stub.

    Presubmit rows use a non-pointer rate (0 when there is no data), so 0 is ambiguous
    with a genuine 0%; disambiguate on Sippy ingestion / runs / trend. Periodic rows use
    a nullable rate, so None already means no data.
    """
    if row.get("sippy_ingestion_enabled") is True:
        return True
    runs = row.get("runs")
    if isinstance(runs, int) and not isinstance(runs, bool) and runs > 0:
        return True
    return row.get("trend") is not None


PAYLOAD_PHASES = frozenset({"Ready", "Accepted", "Rejected", "Pending"})
STREAM_NAME_RE = re.compile(r"[0-9A-Za-z._-]{1,64}")


class Collector:
    def __init__(
        self,
        transport: HTTPTransport | FixtureTransport,
        as_of: dt.datetime,
        source_revision: str | None = None,
        deadline_seconds: float = COLLECTION_DEADLINE_SECONDS,
        max_requests: int = MAX_COLLECTION_REQUESTS,
    ):
        self.transport = transport
        self.as_of = as_of
        self.source_revision = validate_source_revision(
            source_revision or checked_out_source_revision()
        )
        self.source_failures: list[str] = []
        self.coverage_uncertainties: list[str] = []
        self.html_report: str = ""
        if deadline_seconds <= 0 or max_requests <= 0:
            raise ReportError("collection deadline and request budget must be positive")
        self.transport.configure_collection_limits(deadline_seconds, max_requests)

    def collect(self) -> dict[str, Any]:
        """Dashboard-driven deterministic collection (two axes; no Jira, no Slack).

        Reads only the CI Health dashboard health window. Scope, presubmit inventory,
        payload-blocking and component-readiness periodics, per-test flake alerts, pass
        rate and week-over-week trend all come from that one envelope; presubmit run
        order comes from Prow. Returns the data document chaibot consumes.
        """
        try:
            health = self.transport.get_json(
                f"{DASHBOARD_BASE}/_dashboard/health/windows/1w"
            )
            health = validate_health(health)
        except ReportError as exc:
            return self._unknown_report(str(exc))
        try:
            releases_all, releases = self._scope(health)
        except ReportError as exc:
            return self._unknown_report(str(exc))

        data = health.get("data") or {}
        slots = health_sparkline_slots(health)
        dev_release = releases_all[-1] if releases_all else None
        # The dev release (N) is developed on main; predecessors have release-* branches.
        branches = [
            "main",
            *[f"release-{release}" for release in releases if release != dev_release],
        ]
        release_set = set(releases)

        payload_rows = data.get("payload_blocking_jobs") or []
        phase_map = self._fetch_payload_phases(payload_rows, release_set)
        periodics = self._collect_periodics(data, release_set, slots, phase_map)

        grouped = self._dashboard_presubmits(data, branches)
        evidence = self._presubmit_evidence(grouped)
        presubmits: dict[str, list[dict[str, Any]]] = {}
        all_candidates: list[dict[str, Any]] = []
        for branch in branches:
            rows = []
            for job in grouped.get(branch, []):
                ev = evidence[job["id"]]
                cls = ev["classification"]
                rows.append(
                    {
                        "job_id": job["id"],
                        "name": job.get("name") or abbreviated_job(job["id"]),
                        "target_release": job.get("target_release"),
                        "permafail": {
                            "class": cls["classification"],
                            "streak": cls["streak"],
                            "span_hours": cls["span_hours"],
                            "distinct_heads": cls["distinct_heads"],
                            "shade": cls.get("shade") or "",
                        },
                        "slo": self._slo_axis(job, _dashboard_has_data(job)),
                    }
                )
                if cls["classification"] == "candidate":
                    all_candidates.append(self._candidate(job, branch, ev))
            presubmits[branch] = rows

        candidates, candidate_overflow = bound_candidates(all_candidates)
        if candidate_overflow:
            self.source_failures.append(
                "candidate coverage overflow: published "
                f"{candidate_overflow['published']} of {candidate_overflow['total']} "
                "by configured-branch, job-ID priority"
            )

        release_blockers = [
            {
                "release": p["release"],
                "job_id": p["job_id"],
                "name": p["name"],
                "gate": p["gate"],
            }
            for p in periodics
            if p["permafail"]["class"] == "permafailing"
        ]
        merge_queue_blockers = [
            {"branch": branch, "job_id": row["job_id"], "name": row["name"]}
            for branch in branches
            for row in presubmits[branch]
            if row["permafail"]["class"] == "permafailing"
        ]
        # Jobs already flagged as permafailing blockers (release or merge-queue) are tracked
        # there, not under periodics-health; mark them so chaibot excludes them deterministically.
        flagged_blocker_ids = {b["job_id"] for b in release_blockers} | {
            b["job_id"] for b in merge_queue_blockers
        }
        job_health: list[dict[str, Any]] = []
        for branch in branches:
            for row in presubmits[branch]:
                if row["slo"].get("below_slo"):
                    job_health.append(
                        {
                            "job_id": row["job_id"],
                            "name": row["name"],
                            "kind": "presubmit",
                            "branch_or_release": branch,
                            "rate": row["slo"].get("rate"),
                            "trend": (row["slo"].get("trend") or {}).get("classification"),
                            "flagged_blocker": row["job_id"] in flagged_blocker_ids,
                        }
                    )
        for p in periodics:
            if p["slo"].get("below_slo"):
                job_health.append(
                    {
                        "job_id": p["job_id"],
                        "name": p["name"],
                        "kind": "periodic",
                        "branch_or_release": p["release"],
                        "rate": p["slo"].get("rate"),
                        "trend": (p["slo"].get("trend") or {}).get("classification"),
                        "flagged_blocker": p["job_id"] in flagged_blocker_ids,
                    }
                )
        flaky_tests = self._flaky_tests(data)

        self.source_failures = bound_messages(
            self.source_failures, "source failure(s)"
        )
        self.coverage_uncertainties = bound_messages(
            self.coverage_uncertainties, "coverage uncertainty item(s)"
        )
        document = {
            "schema_version": SCHEMA_VERSION,
            "source_revision": self.source_revision,
            "generated_at": format_rfc3339(self.as_of),
            "scope": {
                "state": (
                    "unknown"
                    if self.source_failures or self.coverage_uncertainties
                    else "complete"
                ),
                "releases": releases,
                "dev_release": releases_all[-1] if releases_all else None,
                "branches": branches,
                "source_failures": self.source_failures,
                "coverage_uncertainties": self.coverage_uncertainties,
                "candidate_overflow": candidate_overflow,
            },
            "presubmits": presubmits,
            "presubmit_candidates": {
                branch: [c for c in candidates if c["branch"] == branch]
                for branch in branches
            },
            "periodics": periodics,
            "incident_set": {
                "release_blockers": release_blockers,
                "merge_queue_blockers": merge_queue_blockers,
            },
            "flaky_tests": flaky_tests,
            "job_health_below_slo": job_health,
        }
        document["collection_id"] = collection_id(document)
        self.html_report = render_html_report(document, None)
        try:
            validate_candidates_document(document)
        except ReportError as exc:
            return self._unknown_report(str(exc))
        return document

    def _unknown_report(self, failure: str) -> dict[str, Any]:
        """Fail-closed document: scope Unknown, no green verdicts, still valid + charted."""
        failure = bound_messages([failure], "source failure(s)")[0]
        document = {
            "schema_version": SCHEMA_VERSION,
            "source_revision": self.source_revision,
            "generated_at": format_rfc3339(self.as_of),
            "scope": {
                "state": "unknown",
                "releases": [],
                "dev_release": None,
                "branches": [],
                "source_failures": [failure],
                "coverage_uncertainties": [],
                "candidate_overflow": None,
            },
            "presubmits": {},
            "presubmit_candidates": {},
            "periodics": [],
            "incident_set": {"release_blockers": [], "merge_queue_blockers": []},
            "flaky_tests": [],
            "job_health_below_slo": [],
        }
        document["collection_id"] = collection_id(document)
        self.html_report = render_html_report(document, None)
        return document

    def _scope(self, health: dict[str, Any]) -> tuple[list[str], list[str]]:
        """Supported releases from the dashboard envelope's own ascending release list.

        In-scope = the last supported_predecessors+1 (N plus that many predecessors),
        returned newest-first. No hardcoded list; the dashboard owns the N-x ordering.
        """
        releases_all = health.get("releases")
        if not isinstance(releases_all, list) or not releases_all:
            raise ReportError("dashboard envelope has no releases")
        clean: list[str] = []
        for release in releases_all:
            if not isinstance(release, str) or not RELEASE_RE.fullmatch(release):
                raise ReportError("dashboard releases contains an invalid entry")
            clean.append(release)
        scope = clean[-(SUPPORTED_PREDECESSORS + 1):]
        releases = sorted(scope, key=release_key, reverse=True)
        return clean, releases

    def _slo_axis(self, row: dict[str, Any], has_data: bool) -> dict[str, Any]:
        """Axis B: pass rate vs the SLO plus the confidence-aware WoW trend.

        rate is the dashboard passes/total (infra counted). No data -> Unknown (rate
        None, below_slo None), never a green 0%.
        """
        if not has_data:
            return {
                "rate": None,
                "below_slo": None,
                "trend": classify_wow_trend(None, None, None, None),
            }
        rate = row.get("rate")
        return {
            "rate": rate,
            "below_slo": (
                job_below_slo(rate) if isinstance(rate, (int, float)) else None
            ),
            "trend": classify_wow_trend(
                row.get("rate"),
                row.get("prev"),
                row.get("runs"),
                row.get("prev_runs"),
            ),
        }

    def _fetch_payload_phases(
        self, payload_rows: list[Any], release_set: set[str]
    ) -> dict[tuple[str, str], dict[str, Any]]:
        """Fetch the current payload phase per unique stream (context only).

        The dashboard carries no live payload phase, so this is the one release-controller
        call: GET /api/v1/releasestream/{stream}/tags, whose newest tag carries the phase.
        Deduped per (controller, stream). A per-stream failure yields phase "unknown" for
        that stream and never fails the report — phase is context, not an Axis-A/B input,
        so a phase gap does not flip scope state.
        """
        targets: set[tuple[str, str]] = set()
        for row in payload_rows:
            if not isinstance(row, dict) or str(row.get("release") or "") not in release_set:
                continue
            for part in row.get("participations") or []:
                if not isinstance(part, dict):
                    continue
                stream = str(part.get("stream_name") or "")
                arch = str(part.get("architecture") or "")
                if not STREAM_NAME_RE.fullmatch(stream) or arch not in {"amd64", "multi"}:
                    continue
                base = (
                    MULTI_RELEASE_CONTROLLER_BASE
                    if arch == "multi"
                    else RELEASE_CONTROLLER_BASE
                )
                targets.add((base, stream))

        def fetch(base: str, stream: str) -> dict[str, Any]:
            payload = self.transport.get_json(
                f"{base}/api/v1/releasestream/{stream}/tags"
            )
            if not isinstance(payload, dict):
                raise ReportError("release tags payload is not an object")
            tags = payload.get("tags")
            if not isinstance(tags, list) or not tags or not isinstance(tags[0], dict):
                raise ReportError("release tags payload carries no tags")
            phase = str(tags[0].get("phase") or "")
            if phase not in PAYLOAD_PHASES:
                raise ReportError(f"unexpected payload phase {phase!r}")
            return {"phase": phase, "tag": _bounded_string(tags[0].get("name"), "tag", 255)}

        phases: dict[tuple[str, str], dict[str, Any]] = {}
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = {
                executor.submit(fetch, base, stream): (base, stream)
                for base, stream in targets
            }
            for future, key in futures.items():
                try:
                    phases[key] = future.result()
                except ReportError:
                    phases[key] = {"phase": "unknown", "tag": None}
        return phases

    def _payload_context(
        self,
        payload_row: dict[str, Any] | None,
        phase_map: dict[tuple[str, str], dict[str, Any]],
    ) -> dict[str, Any]:
        """Live payload phase + status link for a blocking periodic (context only)."""
        if not isinstance(payload_row, dict):
            return {"phase": "unknown", "tag": None, "release_status_url": ""}
        fallback_url = ""
        for part in payload_row.get("participations") or []:
            if not isinstance(part, dict):
                continue
            valid_url = ""
            url = str(part.get("release_status_url") or "")
            if url:
                try:
                    validate_public_url(url)
                    valid_url = url
                except ReportError:
                    valid_url = ""
            if valid_url and not fallback_url:
                fallback_url = valid_url
            stream = str(part.get("stream_name") or "")
            arch = str(part.get("architecture") or "")
            if not STREAM_NAME_RE.fullmatch(stream) or arch not in {"amd64", "multi"}:
                continue
            base = (
                MULTI_RELEASE_CONTROLLER_BASE
                if arch == "multi"
                else RELEASE_CONTROLLER_BASE
            )
            phase = phase_map.get((base, stream))
            if phase and phase["phase"] != "unknown":
                return {
                    "phase": phase["phase"],
                    "tag": phase["tag"],
                    "release_status_url": valid_url or fallback_url,
                }
        return {"phase": "unknown", "tag": None, "release_status_url": fallback_url}

    def _collect_periodics(
        self,
        data: dict[str, Any],
        release_set: set[str],
        slots: list[str],
        phase_map: dict[tuple[str, str], dict[str, Any]],
    ) -> list[dict[str, Any]]:
        """Blocking periodics = payload_blocking_jobs union component_readiness_jobs.

        The authoritative payload-blocker list; the nested periodics[] under presubmits
        are only a human-mapped subset and are deliberately not used here.
        """
        payload = {
            str(r.get("id")): r
            for r in (data.get("payload_blocking_jobs") or [])
            if isinstance(r, dict) and r.get("id")
        }
        comp = {
            str(r.get("id")): r
            for r in (data.get("component_readiness_jobs") or [])
            if isinstance(r, dict) and r.get("id")
        }
        ordered_ids: list[str] = []
        seen: set[str] = set()
        for job_id in list(payload) + list(comp):
            if job_id not in seen:
                seen.add(job_id)
                ordered_ids.append(job_id)
        result: list[dict[str, Any]] = []
        for job_id in ordered_ids:
            row = payload.get(job_id) or comp[job_id]
            release = str(row.get("release") or "")
            if release not in release_set:
                continue
            if job_id in payload and job_id in comp:
                gate = "both"
            elif job_id in payload:
                gate = "release-payload"
            else:
                gate = "component-readiness"
            cls = classify_periodic_sparkline(row.get("sparkline"), slots, self.as_of)
            if cls["classification"] == "unknown":
                self.coverage_uncertainties.append(
                    f"Dashboard {job_id}: periodic run history unavailable; "
                    "permafail class Unknown"
                )
            result.append(
                {
                    "job_id": job_id,
                    "name": row.get("name") or abbreviated_job(job_id),
                    "release": release,
                    "gate": gate,
                    "permafail": {
                        "class": cls["classification"],
                        "streak_runs": cls["streak_runs"],
                        "span_hours": cls["span_hours"],
                    },
                    "slo": self._slo_axis(row, _dashboard_has_data(row)),
                    "payload": self._payload_context(payload.get(job_id), phase_map),
                    "series": sparkline_series(row.get("sparkline"), slots),
                }
            )
        result.sort(
            key=lambda item: (
                tuple(-part for part in release_key(item["release"])),
                item["name"],
                item["job_id"],
            )
        )
        return result

    def _dashboard_presubmits(
        self,
        data: dict[str, Any],
        branches: list[str],
    ) -> dict[str, list[dict[str, Any]]]:
        """Group the dashboard presubmit rows by branch (already required/e2e upstream)."""
        grouped: dict[str, list[dict[str, Any]]] = {branch: [] for branch in branches}
        for row in data.get("jobs") or []:
            if not isinstance(row, dict) or not row.get("id"):
                continue
            branch = row.get("target_branch")
            if branch in grouped:
                grouped[str(branch)].append(row)
        for rows in grouped.values():
            rows.sort(key=lambda row: str(row.get("id") or ""))
        return grouped

    def _presubmit_evidence(
        self,
        grouped: dict[str, list[dict[str, Any]]],
    ) -> dict[str, dict[str, Any]]:
        """Fetch and classify Prow run history for every presubmit (Axis A)."""
        evidence: dict[str, dict[str, Any]] = {}
        all_jobs = [job for rows in grouped.values() for job in rows]
        with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS) as executor:
            futures = {
                executor.submit(self._presubmit_history, job): job for job in all_jobs
            }
            for future, job in futures.items():
                try:
                    result = future.result()
                    for item in result["uncertainties"]:
                        self.coverage_uncertainties.append(f"Prow {job['id']}: {item}")
                    evidence[job["id"]] = result
                except ReportError as exc:
                    self.source_failures.append(f"Prow {job['id']}: {exc}")
                    evidence[job["id"]] = {
                        "classification": classify_presubmit_streak([], self.as_of),
                        "runs": [],
                        "uncertainties": ["ordered presubmit history unavailable"],
                    }
        return evidence

    def _candidate(
        self,
        job: dict[str, Any],
        branch: str,
        evidence: dict[str, Any],
    ) -> dict[str, Any]:
        """Bounded presubmit candidate for LLM triage (only the ambiguous middle)."""
        cls = evidence["classification"]
        rate = job.get("rate") if _dashboard_has_data(job) else None
        return {
            "candidate_id": candidate_id("presubmit", job["id"], branch),
            "kind": "presubmit",
            "job_id": job["id"],
            "release": job.get("target_release"),
            "branch": branch,
            "deterministic_trigger": cls.get("shade") or "candidate",
            "classification": cls["classification"],
            "streak": cls["streak"],
            "span_hours": cls["span_hours"],
            "shade": cls.get("shade") or "",
            "distinct_heads": cls["distinct_heads"],
            "below_slo": job_below_slo(rate) if isinstance(rate, (int, float)) else False,
            "dashboard_rate": rate,
            "runs": evidence["runs"][:MAX_SAMPLE_RUNS],
            "coverage_uncertainties": evidence["uncertainties"],
        }

    def _flaky_tests(self, data: dict[str, Any]) -> list[dict[str, Any]]:
        """Per-test flake signal straight from the dashboard alerts section."""
        result: list[dict[str, Any]] = []
        for alert in data.get("alerts") or []:
            if not isinstance(alert, dict):
                continue
            name = alert.get("test_name")
            count = alert.get("failure_count")
            jobs = alert.get("jobs")
            if not isinstance(name, str) or not name:
                continue
            if isinstance(count, bool) or not isinstance(count, int) or count < 0:
                continue
            if not isinstance(jobs, list):
                jobs = []
            result.append(
                {
                    "test_name": name[:MAX_SOURCE_STRING],
                    "failure_count": count,
                    "jobs": [str(j)[:255] for j in jobs[:50] if isinstance(j, str)],
                }
            )
            if len(result) >= MAX_SOURCE_JOBS:
                break
        return result

    def _presubmit_history(self, job: dict[str, Any]) -> dict[str, Any]:
        url = str(job.get("prow_job_history_url") or "")
        if not url:
            raise ReportError("registry has no Prow history URL")
        rows: list[dict[str, Any]] = []
        page_url = url
        # Fetch strictly wider than D so a >= D permafail span is observable; a window
        # exactly D wide can never contain a streak whose span reaches D.
        window_start = self.as_of - dt.timedelta(
            hours=PERMAFAIL_DURATION_HOURS + FLAKE_HORIZON_HOURS
        )
        uncertainty: list[str] = []
        for _ in range(MAX_HISTORY_PAGES):
            try:
                text = self.transport.get_text(page_url)
            except NoDataError:
                if not rows:
                    return {
                        "classification": classify_presubmit_streak([], self.as_of),
                        "runs": [],
                        "uncertainties": ["Prow history has no runs yet"],
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
                    "url": canonical_public_prow_run_url(
                        urllib.parse.urljoin(
                            f"https://{PROW_HOST}",
                            str(row.get("SpyglassLink") or ""),
                        )
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
                uncertainty.append(f"run {run['id']} lacks a canonical head SHA")
                run["head_sha"] = ""
            if window_start <= completed < self.as_of:
                refined.append(run)
        relevant = refined + relevant[3:]
        relevant.sort(key=lambda row: parse_rfc3339(row["completed"]), reverse=True)
        if not relevant:
            uncertainty.append(
                "Prow history has no completed runs in the evaluation window"
            )
        return {
            "classification": classify_presubmit_streak(relevant, self.as_of),
            "runs": relevant,
            "uncertainties": sorted(set(uncertainty)),
        }


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
        if not isinstance(pulls, list) or any(
            not isinstance(pull, dict) for pull in pulls
        ):
            raise ReportError(
                f"Prow allBuilds row {index} pulls is not a list of objects"
            )
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
        run_url = canonical_public_prow_run_url(run_url)
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
        [f"Prow history omitted {nonterminal} nonterminal run(s)"]
        if nonterminal
        else []
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
    if not separator or bucket != PUBLIC_RESULTS_BUCKET:
        raise ReportError("unexpected Prow results bucket")
    url = f"https://storage.googleapis.com/{PUBLIC_RESULTS_BUCKET}/{object_path}/prowjob.json"
    validate_public_url(url)
    return url


def validate_verification_url(url: str) -> None:
    """Release verification evidence must be a canonical public Prow run."""
    validate_run_evidence_url(url)


def canonical_public_prow_run_url(url: str) -> str:
    """Normalize Prow's legacy public alias before emitting run evidence."""
    validate_public_url(url)
    parsed = _parse_url(url)
    alias = "/view/gs/test-platform-results/"
    if parsed.hostname == PROW_HOST and parsed.path.startswith(alias):
        parsed = parsed._replace(
            path=(f"/view/gs/{PUBLIC_RESULTS_BUCKET}/{parsed.path.removeprefix(alias)}")
        )
        url = urllib.parse.urlunparse(parsed)
    validate_run_evidence_url(url)
    return url


def validate_run_evidence_url(url: str, expected_id: str | None = None) -> None:
    """Run evidence must identify one canonical public Prow execution."""
    validate_public_url(url)
    parsed = _parse_url(url)
    decoded_path = urllib.parse.unquote(parsed.path)
    if decoded_path != parsed.path or parsed.query:
        raise ReportError("run URL is not a canonical public Prow result")
    logs_path = re.fullmatch(
        rf"/view/gs/{PUBLIC_RESULTS_BUCKET}/logs/"
        r"[A-Za-z0-9][A-Za-z0-9._-]{0,254}/[1-9][0-9]*",
        parsed.path,
    )
    presubmit_path = re.fullmatch(
        rf"/view/gs/{PUBLIC_RESULTS_BUCKET}/pr-logs/pull/"
        r"[A-Za-z0-9][A-Za-z0-9._-]{0,254}/[1-9][0-9]*/"
        r"[A-Za-z0-9][A-Za-z0-9._-]{0,254}/[1-9][0-9]*",
        parsed.path,
    )
    if parsed.hostname != PROW_HOST or not (logs_path or presubmit_path):
        raise ReportError("run URL is not a canonical public Prow result")
    run_id = parsed.path.rsplit("/", 1)[-1]
    if expected_id is not None and (
        not re.fullmatch(r"[1-9][0-9]*", expected_id) or run_id != expected_id
    ):
        raise ReportError("run URL does not match its run ID")


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
    timestamp_fields = (
        ("timestamp",) if kind == "periodic" else ("started", "completed")
    )
    for field in timestamp_fields:
        parse_rfc3339(run.get(field))
    head_sha = run.get("head_sha") or ""
    if not isinstance(head_sha, str) or (
        head_sha and not GIT_SHA_RE.fullmatch(head_sha)
    ):
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


def flatten_presubmit_candidates(document: dict[str, Any]) -> list[dict[str, Any]]:
    groups = document.get("presubmit_candidates")
    if not isinstance(groups, dict):
        raise ReportError("presubmit_candidates must be an object grouped by branch")
    candidates: list[dict[str, Any]] = []
    for branch, items in groups.items():
        _grammar_string(branch, "presubmit candidate branch", BRANCH_RE)
        if not isinstance(items, list):
            raise ReportError(f"presubmit_candidates.{branch} must be a list")
        candidates.extend(items)
    return candidates


def _validate_slo(slo: Any, label: str) -> None:
    """Validate an Axis-B block: rate (or null), below_slo (bool or null), trend."""
    if not isinstance(slo, dict):
        raise ReportError(f"{label} must be an object")
    rate = slo.get("rate")
    if rate is not None:
        _validate_metric(rate, f"{label}.rate", 0, 100)
    below = slo.get("below_slo")
    if below is not None and not isinstance(below, bool):
        raise ReportError(f"{label}.below_slo must be boolean or null")
    trend = slo.get("trend")
    if not isinstance(trend, dict) or trend.get("classification") not in {
        "improving",
        "degrading",
        "stable",
        "insufficient",
    }:
        raise ReportError(f"{label}.trend.classification is invalid")


def validate_candidates_document(document: Any) -> dict[str, Any]:
    """Validate the deterministic data document collect() emits (chaibot consumes it).

    Enforces JSON-serializability, a byte bound, the envelope identity, the two-axis
    class enums, and grammar/bounds on ids/branches/releases. Structural, not semantic:
    it never asserts a health verdict.
    """
    try:
        document_bytes = len(json.dumps(document, separators=(",", ":")).encode("utf-8"))
    except (TypeError, ValueError) as exc:
        raise ReportError("data document is not valid JSON data") from exc
    if document_bytes > MAX_CANDIDATE_DOCUMENT_BYTES:
        raise ReportError(f"data document exceeds {MAX_CANDIDATE_DOCUMENT_BYTES} bytes")
    if not isinstance(document, dict) or document.get("schema_version") != SCHEMA_VERSION:
        raise ReportError(f"data schema_version must be {SCHEMA_VERSION}")
    validate_source_revision(document.get("source_revision"))
    parse_rfc3339(document.get("generated_at"))
    _grammar_string(document.get("collection_id"), "collection_id", COLLECTION_ID_RE)

    scope = document.get("scope")
    if not isinstance(scope, dict) or scope.get("state") not in {"complete", "unknown"}:
        raise ReportError("scope must be a complete or unknown object")
    releases = _validate_string_list(scope.get("releases", []), "scope.releases", 20, 16)
    for release in releases:
        _grammar_string(release, "scope release", RELEASE_RE)
    branches = _validate_string_list(scope.get("branches", []), "scope.branches", 21, 100)
    for branch in branches:
        _grammar_string(branch, "scope branch", BRANCH_RE)
    _validate_string_list(
        scope.get("source_failures", []), "scope.source_failures", MAX_SOURCE_FAILURES, 1000
    )
    _validate_string_list(
        scope.get("coverage_uncertainties", []),
        "scope.coverage_uncertainties",
        MAX_SOURCE_FAILURES,
        1000,
    )

    presubmits = document.get("presubmits")
    if not isinstance(presubmits, dict) or set(presubmits) != set(branches):
        raise ReportError("presubmits groups must exactly match scope branches")
    for branch, rows in presubmits.items():
        if not isinstance(rows, list) or len(rows) > MAX_SOURCE_JOBS:
            raise ReportError(f"presubmits.{branch} must be a bounded list")
        for index, row in enumerate(rows):
            label = f"presubmits.{branch}[{index}]"
            if not isinstance(row, dict):
                raise ReportError(f"{label} must be an object")
            _grammar_string(row.get("job_id"), f"{label}.job_id", JOB_ID_RE)
            permafail = row.get("permafail")
            if not isinstance(permafail, dict) or permafail.get("class") not in {
                "permafailing",
                "candidate",
                "not_flagged",
                "unknown",
            }:
                raise ReportError(f"{label}.permafail.class is invalid")
            _validate_slo(row.get("slo"), f"{label}.slo")

    candidates = flatten_presubmit_candidates(document)
    if set(document["presubmit_candidates"]) != set(branches):
        raise ReportError("presubmit candidate groups must exactly match scope branches")
    if len(candidates) > MAX_CANDIDATES:
        raise ReportError(f"presubmit candidates exceed {MAX_CANDIDATES}")
    for index, candidate in enumerate(candidates):
        label = f"presubmit candidate {index}"
        if not isinstance(candidate, dict):
            raise ReportError(f"{label} must be an object")
        _grammar_string(
            candidate.get("candidate_id"), f"{label}.candidate_id", CANDIDATE_ID_RE
        )
        _grammar_string(candidate.get("job_id"), f"{label}.job_id", JOB_ID_RE)
        _grammar_string(candidate.get("branch"), f"{label}.branch", BRANCH_RE)
        runs = candidate.get("runs")
        if not isinstance(runs, list) or len(runs) > MAX_SAMPLE_RUNS:
            raise ReportError(f"{label}.runs must be a bounded list")
        for run_index, run in enumerate(runs):
            _validate_candidate_run(run, "presubmit", f"{label}.runs[{run_index}]")
        _validate_string_list(
            candidate.get("coverage_uncertainties", []),
            f"{label}.coverage_uncertainties",
            20,
            500,
        )

    periodics = document.get("periodics")
    if not isinstance(periodics, list) or len(periodics) > MAX_SOURCE_JOBS:
        raise ReportError("periodics must be a bounded list")
    identities: list[str] = []
    for index, item in enumerate(periodics):
        label = f"periodics[{index}]"
        if not isinstance(item, dict):
            raise ReportError(f"{label} must be an object")
        identities.append(_grammar_string(item.get("job_id"), f"{label}.job_id", JOB_ID_RE))
        _grammar_string(item.get("release"), f"{label}.release", RELEASE_RE)
        if item.get("gate") not in {"release-payload", "component-readiness", "both"}:
            raise ReportError(f"{label}.gate is invalid")
        permafail = item.get("permafail")
        if not isinstance(permafail, dict) or permafail.get("class") not in {
            "permafailing",
            "not_permafailing",
            "unknown",
        }:
            raise ReportError(f"{label}.permafail.class is invalid")
        _validate_slo(item.get("slo"), f"{label}.slo")
        payload = item.get("payload")
        if not isinstance(payload, dict):
            raise ReportError(f"{label}.payload must be an object")
        status_url = payload.get("release_status_url") or ""
        if not isinstance(status_url, str):
            raise ReportError(f"{label}.payload.release_status_url must be a string")
        if status_url:
            validate_public_url(status_url)
    if len(identities) != len(set(identities)):
        raise ReportError("periodics job identities must be unique")

    incident = document.get("incident_set")
    if not isinstance(incident, dict):
        raise ReportError("incident_set must be an object")
    for key in ("release_blockers", "merge_queue_blockers"):
        entries = incident.get(key)
        if not isinstance(entries, list) or len(entries) > MAX_SOURCE_JOBS:
            raise ReportError(f"incident_set.{key} must be a bounded list")
        for entry in entries:
            if not isinstance(entry, dict):
                raise ReportError(f"incident_set.{key} entry must be an object")
            _grammar_string(entry.get("job_id"), f"incident_set.{key} job_id", JOB_ID_RE)

    flaky = document.get("flaky_tests")
    if not isinstance(flaky, list) or len(flaky) > MAX_SOURCE_JOBS:
        raise ReportError("flaky_tests must be a bounded list")
    for index, alert in enumerate(flaky):
        if not isinstance(alert, dict) or not isinstance(alert.get("test_name"), str):
            raise ReportError(f"flaky_tests[{index}] is invalid")

    job_health = document.get("job_health_below_slo")
    if not isinstance(job_health, list) or len(job_health) > 2 * MAX_SOURCE_JOBS:
        raise ReportError("job_health_below_slo must be a bounded list")
    for index, entry in enumerate(job_health):
        if not isinstance(entry, dict) or entry.get("kind") not in {
            "presubmit",
            "periodic",
        }:
            raise ReportError(f"job_health_below_slo[{index}] is invalid")
        _grammar_string(
            entry.get("job_id"), f"job_health_below_slo[{index}].job_id", JOB_ID_RE
        )
    return document


def _load_asset(name: str) -> str:
    path = (
        Path(__file__).resolve().parents[1] / "assets" / name
    )
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:  # pragma: no cover - packaging error
        raise RuntimeError(f"unable to load asset {path}: {exc}") from exc


def sparkline_series(sparkline: Any, slots: list[str]) -> list[dict[str, Any]]:
    """Per-slot pass rate for charting (infra_fails excluded from testable)."""
    series: list[dict[str, Any]] = []
    if not isinstance(sparkline, list):
        return series
    for index, slot in enumerate(sparkline):
        timestamp = slots[index] if index < len(slots) else None
        if not isinstance(slot, list) or len(slot) != 4:
            series.append({"t": timestamp, "rate": None})
            continue
        _runs, passes, test_fails, _infra = slot
        testable = passes + test_fails
        rate = round(100.0 * passes / testable, 1) if testable else None
        series.append({"t": timestamp, "rate": rate})
    return series


def build_chart_svg(
    name: str,
    series: list[dict[str, Any]],
    slo: float,
    width: int = 380,
    height: int = 120,
) -> str:
    """Inline SVG sparkline of pass rate over time, with an SLO reference line."""
    margin = 24
    plot_w = width - 2 * margin
    plot_h = height - 2 * margin

    def y_for(rate: float) -> float:
        return margin + plot_h * (1 - rate / 100.0)

    count = len(series)
    points: list[tuple[float, float]] = []
    for index, slot in enumerate(series):
        rate = slot.get("rate")
        if isinstance(rate, (int, float)):
            x = margin + (plot_w * index / (count - 1) if count > 1 else plot_w / 2)
            points.append((x, y_for(rate)))
    polyline = (
        '<polyline fill="none" stroke="#1a73e8" stroke-width="2" points="'
        + " ".join(f"{x:.1f},{y:.1f}" for x, y in points)
        + '" />'
        if len(points) > 1
        else ""
    )
    dots = "".join(
        f'<circle cx="{x:.1f}" cy="{y:.1f}" r="2.5" fill="#1a73e8" />' for x, y in points
    )
    slo_y = y_for(slo)
    title = html.escape(name)
    return (
        f'<svg width="{width}" height="{height}" viewBox="0 0 {width} {height}" '
        f'role="img" aria-label="pass rate for {title}">'
        f'<rect x="{margin}" y="{margin}" width="{plot_w}" height="{plot_h}" '
        f'fill="#fafafa" stroke="#e2e2e2" />'
        f'<line x1="{margin}" y1="{slo_y:.1f}" x2="{margin + plot_w}" y2="{slo_y:.1f}" '
        f'stroke="#b26a00" stroke-dasharray="4 3" stroke-width="1" />'
        f"{polyline}{dots}</svg>"
    )


def _ann_text(text: Any) -> str:
    """Escape chaibot's free-form annotation text, preserving line breaks."""
    return "<br>".join(html.escape(line) for line in str(text or "").splitlines())


def _status_pill(pf_class: Any, below_slo: Any, rate: Any) -> str:
    if pf_class == "permafailing":
        return '<span class="pill red">permafailing</span>'
    if pf_class == "candidate":
        return '<span class="pill amber">candidate</span>'
    if below_slo:
        return '<span class="pill amber">below SLO</span>'
    if isinstance(rate, (int, float)) and not isinstance(rate, bool):
        return '<span class="pill green">meeting SLO</span>'
    return '<span class="pill slate">unknown</span>'


def _trend_label(trend: Any) -> str:
    classification = trend.get("classification") if isinstance(trend, dict) else None
    return {
        "improving": "improving ↑",
        "degrading": "degrading ↓",
        "stable": "stable →",
        "insufficient": "insufficient",
    }.get(classification, "—")


def _rate_bar(rate: Any) -> str:
    if not isinstance(rate, (int, float)) or isinstance(rate, bool):
        return '<span class="mut">no data</span>'
    pct = max(0.0, min(100.0, float(rate)))
    color = (
        "var(--green)"
        if rate >= SLO_PASS_RATE_PERCENT
        else "var(--amber)" if rate >= 50 else "var(--red)"
    )
    return (
        '<span class="flex"><span class="bar">'
        f'<span style="width:{pct:.0f}%;background:{color}"></span></span>{rate:.0f}%</span>'
    )


def _release_sort_key(release: str) -> tuple[int, int]:
    return (
        tuple(-part for part in release_key(release))
        if RELEASE_RE.fullmatch(release)
        else (0, 0)
    )


def _presubmit_has_signal(row: dict[str, Any]) -> bool:
    """A presubmit row worth a table line: a real Axis-A verdict, below-SLO, or a rate.

    The dashboard emits a row for every planned presubmit across all branches, but only the
    development branch has Sippy ingestion, so release-branch presubmits come back with no
    rate/trend and often no Prow runs in the window. Those empty stubs are collapsed into a
    per-branch count rather than tabled, so the actionable rows are not drowned out.
    """
    pf = row.get("permafail") or {}
    slo = row.get("slo") or {}
    if pf.get("class") in ("permafailing", "candidate"):
        return True
    if slo.get("below_slo"):
        return True
    rate = slo.get("rate")
    if isinstance(rate, (int, float)) and not isinstance(rate, bool):
        return True
    streak = pf.get("streak")
    return isinstance(streak, int) and not isinstance(streak, bool) and streak > 0


def render_html_report(
    document: dict[str, Any], annotations: dict[str, Any] | None = None
) -> str:
    """Rich, deterministic HTML report from the data document (+ chaibot annotations).

    The layout is fixed here (readable per-release tables with pass-rate bars, status
    pills, payload phase, and collapsible trend charts, plus presubmit and flaky-test
    tables). The only free-form content is chaibot's optional annotations: an overall
    summary, the incident narrative, and per-job notes -- everything else is deterministic.
    """
    ann = annotations or {}
    notes = ann.get("job_notes") or {}
    scope = document.get("scope") or {}
    periodics = document.get("periodics") or []
    presubmits = document.get("presubmits") or {}
    incident = document.get("incident_set") or {}
    flaky = document.get("flaky_tests") or []
    parts: list[str] = []

    if isinstance(ann.get("summary"), str) and ann["summary"].strip():
        parts.append(
            f'<section><h2>Summary</h2><div class="callout">{_ann_text(ann["summary"])}</div></section>'
        )

    release_blockers = incident.get("release_blockers") or []
    merge_blockers = incident.get("merge_queue_blockers") or []
    if release_blockers or merge_blockers:
        items = [
            f'<li>Release blocker · OCP {html.escape(str(b.get("release")))} · '
            f'<code>{html.escape(str(b.get("name") or b.get("job_id")))}</code> '
            f'({html.escape(str(b.get("gate") or ""))})</li>'
            for b in release_blockers
        ] + [
            f'<li>Merge-queue blocker · {html.escape(str(b.get("branch")))} · '
            f'<code>{html.escape(str(b.get("name") or b.get("job_id")))}</code></li>'
            for b in merge_blockers
        ]
        narrative = (
            f'<p>{_ann_text(ann["incident"])}</p>'
            if isinstance(ann.get("incident"), str) and ann["incident"].strip()
            else ""
        )
        parts.append(
            '<section><h2>Proposed incident '
            '<span class="count">humans declare / bridge / SA</span></h2>'
            f'<div class="callout red"><ul class="notes">{"".join(items)}</ul>{narrative}</div></section>'
        )
    else:
        parts.append(
            '<section><h2>Proposed incident</h2>'
            '<div class="callout green">No permafailing release or merge-queue blockers.</div></section>'
        )

    by_release: dict[str, list[dict[str, Any]]] = {}
    for periodic in periodics:
        by_release.setdefault(str(periodic.get("release") or "?"), []).append(periodic)
    for release in sorted(by_release, key=_release_sort_key):
        rows: list[str] = []
        charts: list[str] = []
        for periodic in sorted(
            by_release[release], key=lambda item: str(item.get("name") or "")
        ):
            pf = periodic.get("permafail") or {}
            slo = periodic.get("slo") or {}
            payload = periodic.get("payload") or {}
            phase = str(payload.get("phase") or "—")
            status_url = str(payload.get("release_status_url") or "")
            phase_cell = (
                f'<a href="{html.escape(status_url)}">{html.escape(phase)}</a>'
                if status_url
                else html.escape(phase)
            )
            rows.append(
                "<tr>"
                f'<td><code>{html.escape(str(periodic.get("name") or periodic.get("job_id")))}</code></td>'
                f'<td class="mut">{html.escape(str(periodic.get("gate") or ""))}</td>'
                f'<td>{_rate_bar(slo.get("rate"))}</td>'
                f'<td>{_status_pill(pf.get("class"), slo.get("below_slo"), slo.get("rate"))}</td>'
                f'<td class="mut">{html.escape(_trend_label(slo.get("trend")))}</td>'
                f'<td class="mut">{phase_cell}</td>'
                f'<td class="mut">{_ann_text(notes.get(str(periodic.get("job_id"))))}</td>'
                "</tr>"
            )
            charts.append(
                build_chart_svg(
                    str(periodic.get("name") or "job"),
                    periodic.get("series") or [],
                    SLO_PASS_RATE_PERCENT,
                )
            )
        table = (
            "<table><thead><tr><th>Job</th><th>Gate</th><th>Pass rate</th><th>Status</th>"
            "<th>Trend</th><th>Payload</th><th>Note</th></tr></thead><tbody>"
            + "".join(rows)
            + "</tbody></table>"
        )
        chart_block = (
            f'<details><summary>Trend charts ({len(charts)})</summary>{"".join(charts)}</details>'
            if charts
            else ""
        )
        parts.append(
            f'<section><h2>OCP {html.escape(release)} — release payloads '
            f'<span class="count">{len(rows)} periodics</span></h2>{table}{chart_block}</section>'
        )

    branch_blocks: list[str] = []
    for branch in scope.get("branches") or []:
        branch_rows = presubmits.get(branch) or []
        if not branch_rows:
            continue
        signal_rows = [row for row in branch_rows if _presubmit_has_signal(row)]
        quiet_rows = [row for row in branch_rows if not _presubmit_has_signal(row)]
        block = f"<h3>{html.escape(branch)}</h3>"
        if signal_rows:
            trs = []
            for row in signal_rows:
                pf = row.get("permafail") or {}
                slo = row.get("slo") or {}
                trs.append(
                    "<tr>"
                    f'<td><code>{html.escape(str(row.get("name") or row.get("job_id")))}</code></td>'
                    f'<td>{_status_pill(pf.get("class"), slo.get("below_slo"), slo.get("rate"))}</td>'
                    f'<td class="mut">{html.escape(str(pf.get("streak", "")))}</td>'
                    f'<td>{_rate_bar(slo.get("rate"))}</td>'
                    f'<td class="mut">{_ann_text(notes.get(str(row.get("job_id"))))}</td>'
                    "</tr>"
                )
            block += (
                "<table><thead><tr><th>Job</th><th>Status</th><th>Streak</th>"
                "<th>Pass rate</th><th>Note</th></tr></thead><tbody>"
                + "".join(trs)
                + "</tbody></table>"
            )
        else:
            block += '<p class="mut">No presubmits with window data.</p>'
        if quiet_rows:
            names = ", ".join(
                f'<code>{html.escape(str(row.get("name") or row.get("job_id")))}</code>'
                for row in quiet_rows
            )
            block += (
                f"<details><summary>{len(quiet_rows)} presubmit(s) with no window data</summary>"
                '<p class="mut">No Sippy-backed pass rate/trend (Sippy ingests the development '
                "branch only) and no runs in the Prow window; shown for completeness.</p>"
                f"<p>{names}</p></details>"
            )
        branch_blocks.append(block)
    if branch_blocks:
        parts.append(
            '<section><h2>Merge queue — required presubmits</h2>'
            + "".join(branch_blocks)
            + "</section>"
        )

    if flaky:
        trs = "".join(
            "<tr>"
            f'<td><code>{html.escape(str(alert.get("test_name")))}</code></td>'
            f'<td>{html.escape(str(alert.get("failure_count")))}</td>'
            f'<td class="mut">{html.escape(", ".join(str(j) for j in (alert.get("jobs") or [])))}</td>'
            "</tr>"
            for alert in flaky
        )
        parts.append(
            f'<section><h2>Flaky tests <span class="count">{len(flaky)}</span></h2>'
            "<table><thead><tr><th>Test</th><th>Failures</th><th>Jobs</th></tr></thead>"
            f"<tbody>{trs}</tbody></table></section>"
        )

    body = "\n".join(parts) if parts else "<section><p>No data.</p></section>"
    meta = (
        f'as of {html.escape(str(document.get("generated_at") or ""))} · '
        f'source {html.escape(str(document.get("source_revision") or ""))} · '
        f'evidence {html.escape(str(document.get("collection_id") or ""))} · '
        f'scope {html.escape(str(scope.get("state") or ""))} · '
        f'releases {html.escape(", ".join(scope.get("releases") or []))}'
    )
    template = string.Template(_load_asset("report.html.tmpl"))
    return template.safe_substitute(
        generated_at=html.escape(str(document.get("generated_at") or "")),
        meta=meta,
        slo=f"{SLO_PASS_RATE_PERCENT:g}",
        body=body,
    )


def collect_command(args: argparse.Namespace) -> int:
    as_of = parse_rfc3339(args.as_of)
    source_revision = validate_source_revision(args.source_revision)
    if source_revision != checked_out_source_revision():
        raise ReportError("collect source revision does not match checked-out HEAD")
    transport: HTTPTransport | FixtureTransport
    if args.fixture_dir:
        transport = FixtureTransport(Path(args.fixture_dir))
    else:
        transport = HTTPTransport(timeout=args.timeout)
    collector = Collector(
        transport,
        as_of,
        source_revision,
        deadline_seconds=args.collection_deadline,
        max_requests=args.max_requests,
    )
    document = collector.collect()
    atomic_write(
        Path(args.data_out),
        json.dumps(document, indent=2, sort_keys=True) + "\n",
    )
    if args.html_out:
        atomic_write(Path(args.html_out), collector.html_report + "\n")
    return 0


def validate_annotations(annotations: Any) -> dict[str, Any]:
    """Validate chaibot's optional free-form report annotations (bounded; escaped on render)."""
    if annotations is None:
        return {}
    if not isinstance(annotations, dict):
        raise ReportError("annotations must be a JSON object")
    result: dict[str, Any] = {}
    for key in ("summary", "incident"):
        value = annotations.get(key)
        if value is not None:
            result[key] = _bounded_freeform(value, f"annotations.{key}", 4000)
    job_notes = annotations.get("job_notes")
    if job_notes is not None:
        if not isinstance(job_notes, dict) or len(job_notes) > MAX_SOURCE_JOBS:
            raise ReportError("annotations.job_notes must be a bounded object")
        result["job_notes"] = {
            str(job_id): _bounded_freeform(note, "annotations.job_notes value", 1000)
            for job_id, note in job_notes.items()
        }
    return result


def report_command(args: argparse.Namespace) -> int:
    """Render the rich HTML report from a data document plus optional chaibot annotations."""
    document = validate_candidates_document(load_json(Path(args.data), "data document"))
    annotations = (
        validate_annotations(load_json(Path(args.annotations), "annotations"))
        if args.annotations
        else {}
    )
    atomic_write(Path(args.html_out), render_html_report(document, annotations) + "\n")
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
        "--source-revision",
        required=True,
        help="canonical checked-out 40-hex Git commit",
    )
    collect.add_argument(
        "--data-out", required=True, help="deterministic data document JSON output"
    )
    collect.add_argument(
        "--html-out", help="deterministic HTML trend report (charts) output"
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

    report = subparsers.add_parser(
        "report", help="render the HTML trend report from a saved data document"
    )
    report.add_argument("--data", required=True, help="data document JSON from collect")
    report.add_argument(
        "--annotations",
        help="optional chaibot annotations JSON (summary, incident, job_notes)",
    )
    report.add_argument("--html-out", required=True, help="HTML report output")
    report.set_defaults(func=report_command)
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
