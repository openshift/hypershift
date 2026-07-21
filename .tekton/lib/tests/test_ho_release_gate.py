"""Tests for ho_release_gate module."""

import json
import sys
import os
import unittest
from datetime import datetime, timezone, timedelta
from unittest.mock import patch, MagicMock, call

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from ho_release_gate import (
    extract_component_image,
    trigger_all_jobs, resolve_all_urls, poll_until_complete,
    print_run_summary, build_results_json, evaluate_gate,
    build_gate_notification, build_error_notification,
    check_failure_streak, build_stale_notification,
    check_and_build_stale_payload,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _snapshot_json(components):
    return json.dumps({"components": components})


def _component(name="hypershift-operator-main",
               image="quay.io/img@sha256:abc123",
               revision="deadbeef"):
    comp = {"name": name, "containerImage": image}
    if revision:
        comp["source"] = {"git": {"revision": revision}}
    return comp


def _job(name="job-a", job_type="blocking", job_id="1",
         url="https://prow/1", result="pending"):
    return {"name": name, "type": job_type, "job_id": job_id,
            "url": url, "result": result}


def _results_json(jobs_data):
    results = []
    for j in jobs_data:
        results.append({
            "job": j.get("name", "job"),
            "result": j.get("result", "passed"),
            "url": j.get("url", ""),
            "type": j.get("type", "blocking"),
        })
    return json.dumps(results, separators=(",", ":"))


# ---------------------------------------------------------------------------
# extract_component_image
# ---------------------------------------------------------------------------

class TestExtractComponentImage(unittest.TestCase):
    """Tests for extract_component_image."""

    def test_valid_snapshot(self):
        snap = _snapshot_json([_component()])
        result = extract_component_image(snap)

        self.assertEqual(result["image"], "quay.io/img@sha256:abc123")
        self.assertEqual(result["digest"], "sha256:abc123")
        self.assertEqual(result["revision"], "deadbeef")

    def test_component_not_found(self):
        snap = _snapshot_json([_component(name="other")])

        with self.assertRaises(ValueError) as ctx:
            extract_component_image(snap)
        self.assertIn("not found", str(ctx.exception))

    def test_empty_container_image(self):
        snap = _snapshot_json([_component(image="")])

        with self.assertRaises(ValueError) as ctx:
            extract_component_image(snap)
        self.assertIn("no containerImage", str(ctx.exception))

    def test_invalid_json(self):
        with self.assertRaises(ValueError) as ctx:
            extract_component_image("not json{")
        self.assertIn("not valid", str(ctx.exception))

    def test_image_without_digest(self):
        snap = _snapshot_json([_component(image="quay.io/img:latest")])
        result = extract_component_image(snap)

        self.assertEqual(result["digest"], "N/A")

    def test_custom_component_name(self):
        comp = _component(name="custom-comp", image="img@sha256:fff")
        snap = _snapshot_json([comp])

        result = extract_component_image(snap, component_name="custom-comp")
        self.assertEqual(result["image"], "img@sha256:fff")

    def test_missing_source_revision(self):
        comp = {"name": "hypershift-operator-main",
                "containerImage": "img@sha256:abc"}
        snap = _snapshot_json([comp])
        result = extract_component_image(snap)

        self.assertEqual(result["revision"], "N/A")


# ---------------------------------------------------------------------------
# trigger_all_jobs
# ---------------------------------------------------------------------------

class TestTriggerAllJobs(unittest.TestCase):
    """Tests for trigger_all_jobs."""

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.trigger_prow_job")
    def test_blocking_only(self, mock_trigger, mock_sleep):
        mock_trigger.return_value = "id-1"

        jobs = trigger_all_jobs(
            ["blocking-job"], [], "https://gangway", "tok", {},
            trigger_delay=0)

        self.assertEqual(len(jobs), 1)
        self.assertEqual(jobs[0]["type"], "blocking")
        self.assertEqual(jobs[0]["job_id"], "id-1")
        self.assertEqual(jobs[0]["result"], "pending")

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.trigger_prow_job")
    def test_blocking_and_informing(self, mock_trigger, mock_sleep):
        mock_trigger.return_value = "id-1"

        jobs = trigger_all_jobs(
            ["block-1"], ["inform-1"], "https://gangway", "tok", {},
            trigger_delay=0)

        self.assertEqual(len(jobs), 2)
        self.assertEqual(jobs[0]["type"], "blocking")
        self.assertEqual(jobs[1]["type"], "informing")

    def test_empty_blocking_raises(self):
        with self.assertRaises(ValueError):
            trigger_all_jobs([], ["inform"], "https://gangway", "tok", {})

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.trigger_prow_job")
    def test_trigger_failure(self, mock_trigger, mock_sleep):
        mock_trigger.return_value = None

        jobs = trigger_all_jobs(
            ["block-1"], [], "https://gangway", "tok", {},
            trigger_delay=0)

        self.assertEqual(jobs[0]["job_id"], None)
        self.assertEqual(jobs[0]["result"], "error")


# ---------------------------------------------------------------------------
# resolve_all_urls
# ---------------------------------------------------------------------------

class TestResolveAllUrls(unittest.TestCase):
    """Tests for resolve_all_urls."""

    @patch("ho_release_gate.resolve_prow_url")
    def test_resolves_url(self, mock_resolve):
        mock_resolve.return_value = "https://prow/view/1"
        jobs = [_job(job_id="1", url="")]

        resolve_all_urls(jobs, "https://gangway", "tok")

        self.assertEqual(jobs[0]["url"], "https://prow/view/1")

    @patch("ho_release_gate.resolve_prow_url")
    def test_skips_none_job_id(self, mock_resolve):
        jobs = [_job(job_id=None, url="")]

        resolve_all_urls(jobs, "https://gangway", "tok")

        mock_resolve.assert_not_called()


# ---------------------------------------------------------------------------
# poll_until_complete
# ---------------------------------------------------------------------------

class TestPollUntilComplete(unittest.TestCase):
    """Tests for poll_until_complete."""

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.time.monotonic")
    @patch("ho_release_gate.get_prow_job_status")
    def test_all_pass(self, mock_status, mock_mono, mock_sleep):
        mock_mono.side_effect = [0, 0, 100]
        mock_status.return_value = ("passed", "SUCCESS")

        jobs = [_job(result="pending")]
        poll_until_complete(
            jobs, "https://gangway", "tok",
            initial_delay=0, poll_interval=0, timeout=300)

        self.assertEqual(jobs[0]["result"], "passed")

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.time.monotonic")
    @patch("ho_release_gate.get_prow_job_status")
    def test_mixed_results(self, mock_status, mock_mono, mock_sleep):
        mock_mono.side_effect = [0, 0, 100]
        mock_status.side_effect = [
            ("passed", "SUCCESS"),
            ("failed", "FAILURE"),
        ]

        jobs = [_job(name="a", result="pending"),
                _job(name="b", result="pending")]
        poll_until_complete(
            jobs, "https://gangway", "tok",
            initial_delay=0, poll_interval=0, poll_stagger=0, timeout=300)

        self.assertEqual(jobs[0]["result"], "passed")
        self.assertEqual(jobs[1]["result"], "failed")

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.time.monotonic")
    @patch("ho_release_gate.get_prow_job_status")
    def test_timeout(self, mock_status, mock_mono, mock_sleep):
        mock_mono.side_effect = [0, 0, 50, 99999]
        mock_status.return_value = ("pending", "TRIGGERED")

        jobs = [_job(result="pending")]
        poll_until_complete(
            jobs, "https://gangway", "tok",
            initial_delay=0, poll_interval=0, timeout=100)

        self.assertEqual(jobs[0]["result"], "error")

    @patch("ho_release_gate.time.sleep")
    @patch("ho_release_gate.time.monotonic")
    @patch("ho_release_gate.get_prow_job_status")
    def test_rate_limited_skips(self, mock_status, mock_mono, mock_sleep):
        mock_mono.side_effect = [0, 0, 10, 100, 110]
        mock_status.side_effect = [
            ("rate_limited", "HTTP 429"),
            ("passed", "SUCCESS"),
        ]

        jobs = [_job(result="pending")]
        poll_until_complete(
            jobs, "https://gangway", "tok",
            initial_delay=0, poll_interval=0, poll_stagger=0, timeout=500)

        self.assertEqual(jobs[0]["result"], "passed")


# ---------------------------------------------------------------------------
# print_run_summary
# ---------------------------------------------------------------------------

class TestPrintRunSummary(unittest.TestCase):
    """Tests for print_run_summary."""

    def test_no_crash(self):
        jobs = [
            _job(name="block-1", job_type="blocking", result="passed"),
            _job(name="inform-1", job_type="informing", result="failed"),
        ]
        print_run_summary(jobs)


# ---------------------------------------------------------------------------
# build_results_json
# ---------------------------------------------------------------------------

class TestBuildResultsJson(unittest.TestCase):
    """Tests for build_results_json."""

    def test_compact_json(self):
        jobs = [_job(name="j1", url="https://prow/1", result="passed")]
        result = build_results_json(jobs)

        parsed = json.loads(result)
        self.assertEqual(len(parsed), 1)
        self.assertEqual(parsed[0]["job"], "j1")
        self.assertEqual(parsed[0]["result"], "passed")
        self.assertEqual(parsed[0]["url"], "https://prow/1")
        self.assertEqual(parsed[0]["type"], "blocking")
        self.assertNotIn(" ", result)


# ---------------------------------------------------------------------------
# evaluate_gate
# ---------------------------------------------------------------------------

class TestEvaluateGate(unittest.TestCase):
    """Tests for evaluate_gate."""

    def test_all_blocking_passed(self):
        rj = _results_json([{"name": "b1", "result": "passed", "type": "blocking"}])
        self.assertTrue(evaluate_gate(rj))

    def test_one_blocking_failed(self):
        rj = _results_json([
            {"name": "b1", "result": "passed", "type": "blocking"},
            {"name": "b2", "result": "failed", "type": "blocking"},
        ])
        self.assertFalse(evaluate_gate(rj))

    def test_empty_blocking(self):
        rj = _results_json([{"name": "i1", "result": "passed", "type": "informing"}])
        self.assertFalse(evaluate_gate(rj))

    def test_invalid_json(self):
        self.assertFalse(evaluate_gate("not json"))

    def test_informing_failure_does_not_affect(self):
        rj = _results_json([
            {"name": "b1", "result": "passed", "type": "blocking"},
            {"name": "i1", "result": "failed", "type": "informing"},
        ])
        self.assertTrue(evaluate_gate(rj))


# ---------------------------------------------------------------------------
# build_gate_notification
# ---------------------------------------------------------------------------

class TestBuildGateNotification(unittest.TestCase):
    """Tests for build_gate_notification."""

    def _call(self, gate_passed=True, release="rel-1"):
        rj = _results_json([{"name": "b1", "result": "passed", "type": "blocking"}])
        return build_gate_notification(
            gate_passed, "ARO HCP", rj,
            release, "snap-1", "pr-1",
            "https://konflux-ui.example.com/ns/t/applications/app")

    def test_passed_with_release_green(self):
        payload = self._call(gate_passed=True, release="rel-1")
        self.assertEqual(payload["attachments"][0]["color"], "#2E7D32")

    def test_passed_no_release_orange(self):
        payload = self._call(gate_passed=True, release="N/A")
        self.assertEqual(payload["attachments"][0]["color"], "#F57C00")

    def test_failed_red(self):
        payload = self._call(gate_passed=False, release="N/A")
        self.assertEqual(payload["attachments"][0]["color"], "#D32F2F")

    def test_payload_has_blocks(self):
        payload = self._call()
        self.assertIn("blocks", payload["attachments"][0])
        self.assertGreater(len(payload["attachments"][0]["blocks"]), 0)


# ---------------------------------------------------------------------------
# build_error_notification
# ---------------------------------------------------------------------------

class TestBuildErrorNotification(unittest.TestCase):
    """Tests for build_error_notification."""

    def test_always_red(self):
        payload = build_error_notification(
            "pr-1", "https://konflux-ui.example.com/ns/t/applications/app",
            "ARO HCP")

        self.assertEqual(payload["attachments"][0]["color"], "#D32F2F")

    def test_contains_pipelinerun_link(self):
        payload = build_error_notification(
            "pr-1", "https://konflux-ui.example.com/ns/t/applications/app",
            "ARO HCP")

        blocks_text = json.dumps(payload["attachments"][0]["blocks"])
        self.assertIn("pr-1", blocks_text)

    def test_contains_gate_label(self):
        payload = build_error_notification(
            "pr-1", "https://konflux-ui.example.com/ns/t/applications/app",
            "ARO HCP")

        blocks_text = json.dumps(payload["attachments"][0]["blocks"])
        self.assertIn("ARO HCP", blocks_text)


# ---------------------------------------------------------------------------
# check_failure_streak
# ---------------------------------------------------------------------------

class TestCheckFailureStreak(unittest.TestCase):
    """Tests for check_failure_streak."""

    def test_all_failed(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": False, "reason": "Failed"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "Failed"},
            {"name": "r3", "created": "2026-07-03", "status": False, "reason": "Failed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 3)

    def test_first_completed(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": True, "reason": "Completed"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "Failed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 0)

    def test_first_succeeded(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": True, "reason": "Succeeded"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "Failed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 0)

    def test_mixed_streak(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": False, "reason": "Failed"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "Failed"},
            {"name": "r3", "created": "2026-07-03", "status": False, "reason": "Failed"},
            {"name": "r4", "created": "2026-07-02", "status": True, "reason": "Completed"},
            {"name": "r5", "created": "2026-07-01", "status": False, "reason": "Failed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 3)
        self.assertEqual(streak[0]["name"], "r1")
        self.assertEqual(streak[2]["name"], "r3")

    def test_empty_list(self):
        self.assertEqual(check_failure_streak([]), [])

    def test_single_succeeded(self):
        runs = [{"name": "r1", "created": "2026-07-05", "status": True, "reason": "Succeeded"}]
        self.assertEqual(check_failure_streak(runs), [])

    def test_single_failed(self):
        runs = [{"name": "r1", "created": "2026-07-05", "status": False, "reason": "Failed"}]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 1)

    def test_timeout_reason(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": False, "reason": "PipelineRunTimeout"},
            {"name": "r2", "created": "2026-07-04", "status": True, "reason": "Completed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 1)

    def test_couldnt_get_pipeline_reason(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": False, "reason": "CouldntGetPipeline"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "Failed"},
            {"name": "r3", "created": "2026-07-03", "status": True, "reason": "Completed"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 2)

    def test_mixed_failure_reasons_in_streak(self):
        runs = [
            {"name": "r1", "created": "2026-07-05", "status": False, "reason": "Failed"},
            {"name": "r2", "created": "2026-07-04", "status": False, "reason": "PipelineRunTimeout"},
            {"name": "r3", "created": "2026-07-03", "status": False, "reason": "CouldntGetPipeline"},
        ]
        streak = check_failure_streak(runs)
        self.assertEqual(len(streak), 3)


# ---------------------------------------------------------------------------
# build_stale_notification
# ---------------------------------------------------------------------------

class TestBuildStaleNotification(unittest.TestCase):
    """Tests for build_stale_notification."""

    def _call(self, streak_days=5, streak_len=3):
        streak = [
            {"name": f"run-{i}", "created": f"2026-07-0{i}T10:00:00Z",
             "status": False, "reason": "Failed"}
            for i in range(1, streak_len + 1)
        ]
        return build_stale_notification(
            streak, "ARO HCP", 3,
            "current-run", "https://konflux-ui.example.com/base",
            streak_days)

    def test_color_red(self):
        payload = self._call()
        self.assertEqual(payload["attachments"][0]["color"], "#D32F2F")

    def test_header_contains_gate_and_days(self):
        payload = self._call(streak_days=5)
        blocks = payload["attachments"][0]["blocks"]
        header_text = blocks[0]["text"]["text"]
        self.assertIn("ARO HCP", header_text)
        self.assertIn("5 day(s)", header_text)

    def test_four_fields(self):
        payload = self._call()
        blocks = payload["attachments"][0]["blocks"]
        fields_block = blocks[1]
        self.assertEqual(len(fields_block["fields"]), 4)

    def test_history_current_first(self):
        payload = self._call()
        blocks = payload["attachments"][0]["blocks"]
        history_text = blocks[3]["text"]["text"]
        lines = history_text.split("\n")
        self.assertIn("(current)", lines[0])

    def test_history_shows_reason_and_run_name(self):
        streak = [
            {"name": "run-1", "created": "2026-07-05T10:00:00Z",
             "status": False, "reason": "PipelineRunTimeout"},
        ]
        payload = build_stale_notification(
            streak, "ARO HCP", 3,
            "current-run", "https://konflux-ui.example.com/base", 5)
        blocks = payload["attachments"][0]["blocks"]
        history_text = blocks[3]["text"]["text"]
        self.assertIn("PipelineRunTimeout", history_text)
        self.assertIn("run-1", history_text)

    def test_footer_present(self):
        payload = self._call()
        blocks = payload["attachments"][0]["blocks"]
        footer_text = blocks[-1]["text"]["text"]
        self.assertIn("investigating", footer_text.lower())

    def test_streak_days_parameter_used(self):
        payload = self._call(streak_days=99)
        blocks = payload["attachments"][0]["blocks"]
        header_text = blocks[0]["text"]["text"]
        self.assertIn("99 day(s)", header_text)

    def test_truncation_at_max_displayed(self):
        streak = [
            {"name": f"run-{i}", "created": f"2026-07-{i:02d}T10:00:00Z",
             "status": False, "reason": "Failed"}
            for i in range(1, 15)
        ]
        payload = build_stale_notification(
            streak, "ARO HCP", 3,
            "current-run", "https://konflux-ui.example.com/base", 16)
        blocks = payload["attachments"][0]["blocks"]
        history_text = blocks[3]["text"]["text"]
        lines = history_text.split("\n")
        self.assertIn("(current)", lines[0])
        self.assertIn("other failed", lines[-1])
        self.assertIn("4", lines[-1])

    def test_no_truncation_when_within_limit(self):
        payload = self._call(streak_len=4)
        blocks = payload["attachments"][0]["blocks"]
        history_text = blocks[3]["text"]["text"]
        self.assertNotIn("other failed", history_text)


# ---------------------------------------------------------------------------
# check_and_build_stale_payload
# ---------------------------------------------------------------------------

class TestCheckAndBuildStalePayload(unittest.TestCase):
    """Tests for check_and_build_stale_payload."""

    def _common_args(self):
        return {
            "token": "tok",
            "its_scenario": "my-its",
            "current_run_name": "current-run",
            "konflux_base_url": "https://konflux-ui.example.com/base",
            "namespace": "ns",
            "gate_label": "ARO HCP",
            "kubearchive_api_base": "https://kubearchive.example.com",
        }

    def _make_run(self, name, created, status=False, reason="Failed"):
        return {"name": name, "created": created,
                "status": status, "reason": reason}

    @patch("ho_release_gate.datetime")
    @patch("ho_release_gate.fetch_pipelineruns")
    def test_streak_below_threshold(self, mock_fetch, mock_dt):
        now = datetime(2026, 7, 8, 12, 0, 0, tzinfo=timezone.utc)
        mock_dt.now.return_value = now
        mock_dt.fromisoformat = datetime.fromisoformat
        mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)

        mock_fetch.return_value = [
            self._make_run("run-1", "2026-07-07T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)

    @patch("ho_release_gate.datetime")
    @patch("ho_release_gate.fetch_pipelineruns")
    def test_streak_below_threshold_verifies_days(self, mock_fetch, mock_dt):
        """1 run from yesterday: streak_days = 1+1 = 2, below threshold 3."""
        now = datetime(2026, 7, 8, 12, 0, 0, tzinfo=timezone.utc)
        mock_dt.now.return_value = now
        mock_dt.fromisoformat = datetime.fromisoformat
        mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)

        mock_fetch.return_value = [
            self._make_run("run-1", "2026-07-07T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)

    @patch("ho_release_gate.datetime")
    @patch("ho_release_gate.fetch_pipelineruns")
    def test_streak_equals_threshold(self, mock_fetch, mock_dt):
        """2 runs spanning Jul 6-7, now=Jul 8: streak_days = 2+1 = 3 = threshold."""
        now = datetime(2026, 7, 8, 12, 0, 0, tzinfo=timezone.utc)
        mock_dt.now.return_value = now
        mock_dt.fromisoformat = datetime.fromisoformat
        mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)

        mock_fetch.return_value = [
            self._make_run("run-2", "2026-07-07T10:00:00Z"),
            self._make_run("run-1", "2026-07-06T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNotNone(result)
        self.assertIn("attachments", result)
        blocks = result["attachments"][0]["blocks"]
        header_text = blocks[0]["text"]["text"]
        self.assertIn("3 day(s)", header_text)

    @patch("ho_release_gate.datetime")
    @patch("ho_release_gate.fetch_pipelineruns")
    def test_streak_above_threshold(self, mock_fetch, mock_dt):
        """5 runs spanning Jul 3-7, now=Jul 8: streak_days = 5+1 = 6."""
        now = datetime(2026, 7, 8, 12, 0, 0, tzinfo=timezone.utc)
        mock_dt.now.return_value = now
        mock_dt.fromisoformat = datetime.fromisoformat
        mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)

        mock_fetch.return_value = [
            self._make_run("run-5", "2026-07-07T10:00:00Z"),
            self._make_run("run-4", "2026-07-06T10:00:00Z"),
            self._make_run("run-3", "2026-07-05T10:00:00Z"),
            self._make_run("run-2", "2026-07-04T10:00:00Z"),
            self._make_run("run-1", "2026-07-03T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNotNone(result)
        blocks = result["attachments"][0]["blocks"]
        header_text = blocks[0]["text"]["text"]
        self.assertIn("6 day(s)", header_text)

    @patch("ho_release_gate.datetime")
    @patch("ho_release_gate.fetch_pipelineruns")
    def test_streak_days_includes_today(self, mock_fetch, mock_dt):
        """Oldest failure 4 days ago + today = 5 days total."""
        now = datetime(2026, 7, 8, 12, 0, 0, tzinfo=timezone.utc)
        mock_dt.now.return_value = now
        mock_dt.fromisoformat = datetime.fromisoformat
        mock_dt.side_effect = lambda *a, **kw: datetime(*a, **kw)

        mock_fetch.return_value = [
            self._make_run("run-4", "2026-07-07T10:00:00Z"),
            self._make_run("run-3", "2026-07-06T10:00:00Z"),
            self._make_run("run-2", "2026-07-05T10:00:00Z"),
            self._make_run("run-1", "2026-07-04T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNotNone(result)
        blocks = result["attachments"][0]["blocks"]
        header_text = blocks[0]["text"]["text"]
        self.assertIn("5 day(s)", header_text)

    @patch("ho_release_gate.fetch_pipelineruns")
    def test_no_runs(self, mock_fetch):
        mock_fetch.return_value = []

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)

    @patch("ho_release_gate.fetch_pipelineruns")
    def test_most_recent_succeeded(self, mock_fetch):
        mock_fetch.return_value = [
            self._make_run("run-2", "2026-07-07T10:00:00Z",
                           status=True, reason="Completed"),
            self._make_run("run-1", "2026-07-06T10:00:00Z"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)

    @patch("ho_release_gate.fetch_pipelineruns")
    def test_current_run_filtered_out(self, mock_fetch):
        mock_fetch.return_value = [
            self._make_run("current-run", "2026-07-08T10:00:00Z"),
            self._make_run("run-1", "2026-07-07T10:00:00Z",
                           status=True, reason="Completed"),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)

    @patch("ho_release_gate.fetch_pipelineruns")
    def test_empty_created_timestamp(self, mock_fetch):
        mock_fetch.return_value = [
            self._make_run("run-1", ""),
        ]

        result = check_and_build_stale_payload(
            threshold_days=3, **self._common_args())

        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()
