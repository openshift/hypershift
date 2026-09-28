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
import json
import os
import re
import sys
import tempfile
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

ALLOWED_JUDGMENTS = {
    "not_permafailing",
    "flaky",
    "permafail_candidate",
    "infrastructure_triage",
    "one_off_failure",
    "incident_candidate",
    "payload_impact_unknown",
    "no_data",
}
TRACKING_KEY_RE = re.compile(r"^(?:OCPBUGS|CNTRLPLANE)-[1-9][0-9]*$")
STREAM_LINK_RE = re.compile(r'href=["\']/releasestream/([^"\'/]+)')
ALL_BUILDS_RE = re.compile(r"var\s+allBuilds\s*=\s*(\[.*?\]);\s*</script>", re.DOTALL)
OLDER_LINK_RE = re.compile(r'href="([^"]+\?buildId=\d+)">&lt;- Older Runs')


class ReportError(RuntimeError):
    """A fail-closed data or validation error."""


class HTTPTransport:
    """Small bounded HTTP client restricted to the report's public hosts."""

    def __init__(self, timeout: float = 20.0, max_bytes: int = MAX_HTTP_BYTES):
        self.timeout = timeout
        self.max_bytes = max_bytes

    def get_text(self, url: str) -> str:
        validate_public_url(url)
        request = urllib.request.Request(
            url,
            headers={"User-Agent": "hypershift-ci-daily-health/1"},
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                data = response.read(self.max_bytes + 1)
        except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError) as exc:
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
        for item in manifest.get("responses", []):
            self.entries.append((re.compile(item["pattern"]), item))

    def get_text(self, url: str) -> str:
        validate_public_url(url)
        for pattern, item in self.entries:
            if pattern.search(url):
                status = int(item.get("status", 200))
                if status < 200 or status >= 300:
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
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != "https" or parsed.hostname not in ALLOWED_HOSTS:
        raise ReportError(
            f"refusing non-public or unknown host: {parsed.hostname or '<missing>'}"
        )
    if parsed.username or parsed.password or parsed.fragment:
        raise ReportError("refusing URL with credentials or fragment")
    if parsed.hostname == "storage.googleapis.com":
        segments = [part for part in parsed.path.split("/") if part]
        if not segments or segments[0] != PUBLIC_RESULTS_BUCKET:
            raise ReportError("refusing non-public Prow results bucket")


def safe_url_label(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    return f"{parsed.hostname}{parsed.path}"


def parse_rfc3339(value: str) -> dt.datetime:
    if not isinstance(value, str) or not value:
        raise ReportError("timestamp must be a non-empty RFC3339 string")
    normalized = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        result = dt.datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise ReportError(f"invalid RFC3339 timestamp: {value}") from exc
    if result.tzinfo is None:
        raise ReportError(f"timestamp lacks timezone: {value}")
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
    heads = {row["head_sha"] for row in streak if row["head_sha"]}
    if len(streak) >= 3 and len(heads) >= 2:
        return "repeated_failure_review", "recent_results"
    return None, "recent_results"


def candidate_id(*parts: str) -> str:
    digest = hashlib.sha256("\x00".join(parts).encode()).hexdigest()[:16]
    return f"hci-{digest}"


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
    text = html.escape(text, quote=False)
    return text[:limit]


class Collector:
    def __init__(self, transport: HTTPTransport | FixtureTransport, as_of: dt.datetime):
        self.transport = transport
        self.as_of = as_of
        self.source_failures: list[str] = []

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
            jobs = registry["jobs"]
            if not isinstance(jobs, list):
                raise ReportError("dashboard registry jobs is not a list")
            if not isinstance(health.get("data"), dict) or not isinstance(
                all_tags, dict
            ):
                raise ReportError("required public response has an unexpected shape")
        except (KeyError, TypeError, ReportError) as exc:
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
                tag_catalogs[MULTI_RELEASE_CONTROLLER_BASE] = self.transport.get_json(
                    f"{MULTI_RELEASE_CONTROLLER_BASE}/api/v1/releasestreams/all"
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
                        self.source_failures.append(f"Sippy {key[0]}: {item}")
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
                        self.source_failures.append(f"Prow {job['id']}: {item}")
                    presubmit_evidence[job["id"]] = evidence
                except ReportError as exc:
                    self.source_failures.append(f"Prow {job['id']}: {exc}")
                    presubmit_evidence[job["id"]] = {
                        "candidate_trigger": None,
                        "runs": [],
                        "uncertainties": ["ordered presubmit history unavailable"],
                        "recent_status": "no_data",
                    }

        candidates = self._build_candidates(
            blockers,
            payloads,
            trend_by_job,
            required,
            presubmit_evidence,
            health_rows,
        )
        candidates = candidates[:MAX_CANDIDATES]
        self.source_failures = sorted(set(self.source_failures))[:MAX_SOURCE_FAILURES]

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
                "state": "unknown" if self.source_failures else "complete",
                "releases": releases,
                "branches": ["main", *[f"release-{release}" for release in releases]],
                "validated_streams": validated_streams,
                "source_failures": self.source_failures,
            },
            "candidates": candidates,
        }
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
        )
        return stage_one, document

    def _unknown_report(self, failure: str) -> tuple[str, dict[str, Any]]:
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
            },
            "candidates": [],
        }
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
        statuses: dict[tuple[str, str], dict[str, Any]] = {}
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
                if (controller, stream) in scheduled_streams:
                    continue
                scheduled_streams.add((controller, stream))
                tags = tag_catalogs.get(controller, {}).get(stream)
                if not isinstance(tags, list) or not tags:
                    statuses[(stream, "")] = {
                        "state": "unknown",
                        "uncertainty": "current tag unavailable",
                    }
                    continue
                tag = str(tags[0])
                url = (
                    f"{controller}/api/v1/releasestream/{urllib.parse.quote(stream)}"
                    f"/release/{urllib.parse.quote(tag)}?format=short"
                )
                futures[executor.submit(self.transport.get_json, url)] = (stream, tag)
            for future, (stream, tag) in futures.items():
                try:
                    detail = future.result()
                    if detail.get("name") != tag:
                        raise ReportError("release detail tag mismatch")
                    statuses[(stream, tag)] = detail
                except (AttributeError, ReportError) as exc:
                    self.source_failures.append(f"release controller {stream}: {exc}")
                    statuses[(stream, tag)] = {
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
            detail = statuses.get((stream, tag), statuses.get((stream, ""), {}))
            phase = detail.get("phase")
            verification_status = None
            results = detail.get("results") or {}
            for group in ("blockingJobs", "pendingJobs", "informingJobs", "asyncJobs"):
                value = (results.get(group) or {}).get(blocker["verification"])
                if value:
                    verification_status = value
                    break
            state = (verification_status or {}).get("state")
            if state in {"Failed", "Pending"} and phase not in {"Accepted", "Rejected"}:
                impact = "verified_blocker"
            elif state == "Succeeded":
                impact = "passing"
            else:
                impact = "unknown"
            result[(blocker["job_id"], stream)] = {
                "tag": tag,
                "phase": phase or "Unknown",
                "verification_state": state or "Unknown",
                "verification_url": str((verification_status or {}).get("url") or ""),
                "impact": impact,
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
            page_rows = data.get("rows")
            if not isinstance(page_rows, list):
                raise ReportError("Sippy rows is not a list")
            rows.extend(page_rows)
            total_rows = int(data.get("total_rows", len(rows)))
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
            text = self.transport.get_text(page_url)
            page_rows, older = parse_prow_history(text, page_url)
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
            status = prowjob.get("status") or {}
            spec = prowjob.get("spec") or {}
            refs = spec.get("refs") or {}
            pulls = refs.get("pulls") or []
            try:
                started = parse_rfc3339(str(status.get("startTime")))
                completed = parse_rfc3339(str(status.get("completionTime")))
            except ReportError as exc:
                raise ReportError(
                    f"invalid public prowjob timing for {run['id']}"
                ) from exc
            run = {
                **run,
                "started": format_rfc3339(started),
                "completed": format_rfc3339(completed),
                "state": normalize_state(status.get("state")),
                "head_sha": str(pulls[0].get("sha") or run["head_sha"])
                if pulls
                else run["head_sha"],
            }
            if window_start <= completed < self.as_of:
                refined.append(run)
        relevant = refined + relevant[3:]
        relevant.sort(key=lambda row: parse_rfc3339(row["completed"]), reverse=True)
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
    ) -> str:
        trend_counts: dict[str, int] = defaultdict(int)
        for evidence in trends.values():
            trend_counts[evidence["trend"]["classification"]] += 1
        live_count = sum(
            1
            for payload in payloads.values()
            if payload["impact"] == "verified_blocker"
        )
        unknown_count = len(self.source_failures) + sum(
            1 for payload in payloads.values() if payload["impact"] == "unknown"
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

        lines = [
            f"*HyperShift CI Daily Health Report* — as of {format_rfc3339(self.as_of)}",
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
                tag = f"{sample['tag'] or 'Unknown'} {sample['phase']}"
                url = sample["release_status_url"]
            label = f"<{url}|{tag}>" if url else tag
            lines.append(
                f"• *OCP {release}* · {label}: {len(live)} blocker(s), {len(unknown)} unknown"
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
                f"• *{branch} → OCP {target}*: {count} candidate(s), {no_data} no-data gate(s)"
            )

        lines.extend(
            [
                "",
                "*Action items*",
                f"• Judge {len(candidates)} bounded candidate(s); do not infer branch impact from one PR",
                "• Tracking: verify an existing OCPBUGS/CNTRLPLANE issue or record an explicit gap; no automated writes",
            ]
        )
        return bound_text("\n".join(lines), STAGE_ONE_LIMIT)


def parse_prow_history(
    text: str, page_url: str
) -> tuple[list[dict[str, Any]], str | None]:
    match = ALL_BUILDS_RE.search(text)
    if not match:
        raise ReportError("Prow history page lacks allBuilds data")
    try:
        raw_rows = json.loads(match.group(1))
    except json.JSONDecodeError as exc:
        raise ReportError("Prow allBuilds is invalid JSON") from exc
    rows = []
    for row in raw_rows[:20]:
        try:
            started = parse_rfc3339(str(row.get("Started")))
            duration_ns = int(row.get("Duration") or 0)
        except (ReportError, TypeError, ValueError):
            continue
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
    return rows, older


def public_prowjob_url(prow_url: str) -> str:
    parsed = urllib.parse.urlparse(prow_url)
    if parsed.scheme != "https" or parsed.hostname != PROW_HOST:
        raise ReportError("unexpected Prow run host")
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


def serialize_sippy_run(run: dict[str, Any]) -> dict[str, Any]:
    annotations = run.get("annotations") or {}
    url = str(run.get("url") or run.get("test_grid_url") or "")
    if url:
        validate_public_url(url)
    return {
        "id": str(run.get("prow_id") or run.get("id") or ""),
        "timestamp": format_rfc3339(run["_timestamp"]),
        "state": normalize_state(run.get("overall_result")),
        "head_sha": str(run.get("pull_request_sha") or ""),
        "url": url,
        "payload_tag": str(annotations.get("release.openshift.io/tag") or ""),
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


def validate_candidates_document(document: Any) -> dict[str, Any]:
    if (
        not isinstance(document, dict)
        or document.get("schema_version") != SCHEMA_VERSION
    ):
        raise ReportError(f"candidates schema_version must be {SCHEMA_VERSION}")
    if not isinstance(document.get("candidates"), list):
        raise ReportError("candidates must be a list")
    ids = [
        item.get("candidate_id")
        for item in document["candidates"]
        if isinstance(item, dict)
    ]
    if (
        len(ids) != len(document["candidates"])
        or len(ids) != len(set(ids))
        or any(not value for value in ids)
    ):
        raise ReportError("candidate IDs must be present and unique")
    return document


def validate_judgments(
    document: Any, expected_ids: set[str]
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
        if judgment.get("classification") not in ALLOWED_JUDGMENTS:
            raise ReportError(f"unsupported classification for {identifier}")
        for field in ("summary", "signature", "next_action"):
            if not isinstance(judgment.get(field), str) or len(judgment[field]) > 1000:
                raise ReportError(
                    f"{field} must be a string of at most 1000 characters"
                )
        evidence = judgment.get("recurring_evidence")
        if (
            not isinstance(evidence, list)
            or len(evidence) > 5
            or any(not isinstance(item, str) for item in evidence)
        ):
            raise ReportError("recurring_evidence must contain at most five strings")
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
        by_id[identifier] = judgment
    if set(by_id) != expected_ids:
        missing = sorted(expected_ids - set(by_id))
        extra = sorted(set(by_id) - expected_ids)
        raise ReportError(
            f"judgment IDs do not match candidates; missing={missing}, extra={extra}"
        )
    return by_id


def render_report(stage_one: str, candidates_doc: Any, judgments_doc: Any) -> str:
    if len(stage_one) > STAGE_ONE_LIMIT:
        raise ReportError(f"stage-one message exceeds {STAGE_ONE_LIMIT} characters")
    candidates_doc = validate_candidates_document(candidates_doc)
    candidates = candidates_doc["candidates"]
    judgments = validate_judgments(
        judgments_doc, {item["candidate_id"] for item in candidates}
    )
    if not candidates:
        return stage_one

    grouped: dict[str, list[tuple[dict[str, Any], dict[str, Any]]]] = defaultdict(list)
    order = []
    for candidate in candidates:
        key = (
            f"OCP {candidate.get('release')}"
            if candidate.get("kind") == "periodic"
            else f"{candidate.get('branch')} → OCP {candidate.get('release')}"
        )
        if key not in grouped:
            order.append(key)
        grouped[key].append((candidate, judgments[candidate["candidate_id"]]))

    replies = []
    for key in order:
        lines = [f"*{escape_slack(key)}*", ""]
        for candidate, judgment in grouped[key]:
            classification = judgment["classification"].replace("_", " ").title()
            lines.append(f"• `{candidate['job_id']}` — *{classification}*")
            lines.append(f"  ◦ Summary: {escape_slack(judgment['summary'])}")
            lines.append(
                f"  ◦ Signature: {escape_slack(judgment['signature']) or 'None verified'}"
            )
            if judgment["recurring_evidence"]:
                evidence = "; ".join(
                    escape_slack(item, 300) for item in judgment["recurring_evidence"]
                )
                lines.append(f"  ◦ Recurring evidence: {evidence}")
            run_links = []
            for run in candidate.get("runs", [])[:3]:
                url = str(run.get("url") or "")
                if url:
                    validate_public_url(url)
                    run_links.append(
                        f"<{url}|{escape_slack(run.get('id'), 80)} {escape_slack(run.get('state'), 40)}>"
                    )
            if run_links:
                lines.append(f"  ◦ Runs: {', '.join(run_links)}")
            tracking = judgment["tracking"]
            if tracking["status"] == "existing":
                tracking_text = f"{tracking['key']} (verified existing)"
            elif tracking["status"] == "gap":
                tracking_text = "Tracking issue needed — human follow-up only"
            else:
                tracking_text = "None"
            lines.append(f"  ◦ Tracking: {escape_slack(tracking_text)}")
            lines.append(f"  ◦ Next action: {escape_slack(judgment['next_action'])}")
            lines.append("")
        replies.append(bound_text("\n".join(lines).rstrip(), THREAD_REPLY_LIMIT))
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
    stage_one, candidates = Collector(transport, as_of).collect()
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
