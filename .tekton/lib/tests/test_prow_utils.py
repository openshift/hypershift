"""Tests for prow_utils module."""

import json
import sys
import os
import unittest
from unittest.mock import patch, MagicMock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from prow_utils import (short_name, trigger_prow_job,
                         resolve_prow_url, get_prow_job_status)


class TestShortName(unittest.TestCase):
    """Tests for short_name."""

    def test_short_name_correctly_processes_job_names(self):
        cases = [
            ("periodic-ci-openshift-hypershift-release-5.0-periodics-e2e-aks", "5.0/e2e-aks"),
            ("periodic-ci-openshift-hypershift-release-5.0-periodics-hcm-azure-e2e-aks-upgrade-minor", "5.0/hcm-azure-e2e-aks-upgrade-minor"),
            ("periodic-ci-openshift-hypershift-release-5.0-periodics-e2e-aks-multi-x-ax", "5.0/e2e-aks-multi-x-ax"),
            ("periodic-ci-openshift-hypershift-release-4.22-periodics-e2e-aks-multi-x-ax", "4.22/e2e-aks-multi-x-ax"),
            ("periodic-ci-openshift-hypershift-release-4.21-periodics-e2e-aks-multi-x-ax", "4.21/e2e-aks-multi-x-ax"),
            ("periodic-ci-openshift-hypershift-release-4.20-periodics-e2e-aks-multi-x-ax", "4.20/e2e-aks-multi-x-ax"),
            ("periodic-ci-openshift-hypershift-release-4.22-periodics-hcm-azure-e2e-aks-upgrade-from-zero", "4.22/hcm-azure-e2e-aks-upgrade-from-zero"),
            ("periodic-ci-openshift-hypershift-release-4.21-periodics-hcm-azure-e2e-aks-upgrade-from-zero", "4.21/hcm-azure-e2e-aks-upgrade-from-zero"),
            ("periodic-ci-openshift-hypershift-release-4.20-periodics-hcm-azure-e2e-aks-upgrade-from-zero", "4.20/hcm-azure-e2e-aks-upgrade-from-zero"),
            ("periodic-ci-openshift-hypershift-release-5.0-periodics-e2e-azure-aks-ovn-conformance", "5.0/e2e-azure-aks-ovn-conformance"),
        ]
        for job_name, expected in cases:
            with self.subTest(job=job_name):
                self.assertEqual(short_name(job_name), expected)

    def test_short_name_no_match_returns_original(self):
        self.assertEqual(short_name("other-job-name"), "other-job-name")

    def test_short_name_empty_string(self):
        self.assertEqual(short_name(""), "")


class TestTriggerProwJob(unittest.TestCase):
    """Tests for trigger_prow_job."""

    @patch("prow_utils.http_request_with_retry")
    def test_success(self, mock_req):
        mock_req.return_value = (200, json.dumps({"id": "12345"}))

        job_id = trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {"IMAGE": "quay.io/test"})

        self.assertEqual(job_id, "12345")

    @patch("prow_utils.http_request_with_retry")
    def test_numeric_id_returned_as_string(self, mock_req):
        mock_req.return_value = (200, json.dumps({"id": 99}))

        job_id = trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {})

        self.assertEqual(job_id, "99")

    @patch("prow_utils.http_request_with_retry")
    def test_http_failure(self, mock_req):
        mock_req.return_value = (503, "unavailable")

        job_id = trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {})

        self.assertIsNone(job_id)

    @patch("prow_utils.http_request_with_retry")
    def test_invalid_json_response(self, mock_req):
        mock_req.return_value = (200, "not json")

        job_id = trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {})

        self.assertIsNone(job_id)

    @patch("prow_utils.http_request_with_retry")
    def test_missing_id_field(self, mock_req):
        mock_req.return_value = (200, json.dumps({"status": "ok"}))

        job_id = trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {})

        self.assertIsNone(job_id)

    @patch("prow_utils.http_request_with_retry")
    def test_passes_correct_payload(self, mock_req):
        mock_req.return_value = (200, json.dumps({"id": "1"}))

        trigger_prow_job(
            "https://gangway.example.com/v1/executions",
            "tok", "periodic-job", {"K": "V"}, max_retries=5, backoff=60)

        _, kwargs = mock_req.call_args
        self.assertEqual(kwargs["retries"], 5)
        self.assertEqual(kwargs["backoff"], 60)
        body = kwargs["body"]
        self.assertEqual(body["job_name"], "periodic-job")
        self.assertEqual(body["pod_spec_options"]["envs"], {"K": "V"})


class TestResolveProwUrl(unittest.TestCase):
    """Tests for resolve_prow_url."""

    @patch("prow_utils.time.sleep")
    @patch("prow_utils.http_request")
    def test_url_available_immediately(self, mock_req, mock_sleep):
        mock_req.return_value = (
            200, json.dumps({"job_url": "https://prow.ci/view/123"}))

        url = resolve_prow_url(
            "https://gangway.example.com/v1/executions",
            "tok", "123")

        self.assertEqual(url, "https://prow.ci/view/123")
        mock_sleep.assert_not_called()

    @patch("prow_utils.time.sleep")
    @patch("prow_utils.http_request")
    def test_url_available_after_polls(self, mock_req, mock_sleep):
        mock_req.side_effect = [
            (200, json.dumps({"job_url": ""})),
            (200, json.dumps({"job_url": "https://prow.ci/view/123"})),
        ]

        url = resolve_prow_url(
            "https://gangway.example.com/v1/executions",
            "tok", "123", max_attempts=5, delay=1)

        self.assertEqual(url, "https://prow.ci/view/123")
        self.assertEqual(mock_req.call_count, 2)

    @patch("prow_utils.time.sleep")
    @patch("prow_utils.http_request")
    def test_max_attempts_exceeded(self, mock_req, mock_sleep):
        mock_req.return_value = (200, json.dumps({"job_url": ""}))

        url = resolve_prow_url(
            "https://gangway.example.com/v1/executions",
            "tok", "123", max_attempts=3, delay=1)

        self.assertEqual(url, "")
        self.assertEqual(mock_req.call_count, 3)

    @patch("prow_utils.time.sleep")
    @patch("prow_utils.http_request")
    def test_auth_error_aborts(self, mock_req, mock_sleep):
        mock_req.return_value = (401, "unauthorized")

        url = resolve_prow_url(
            "https://gangway.example.com/v1/executions",
            "tok", "123", max_attempts=5)

        self.assertEqual(url, "")
        self.assertEqual(mock_req.call_count, 1)


class TestGetProwJobStatus(unittest.TestCase):
    """Tests for get_prow_job_status."""

    @patch("prow_utils.http_request")
    def test_success(self, mock_req):
        mock_req.return_value = (
            200, json.dumps({"job_status": "SUCCESS"}))

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "passed")
        self.assertEqual(raw, "SUCCESS")

    @patch("prow_utils.http_request")
    def test_failure(self, mock_req):
        mock_req.return_value = (
            200, json.dumps({"job_status": "FAILURE"}))

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "failed")
        self.assertEqual(raw, "FAILURE")

    @patch("prow_utils.http_request")
    def test_aborted(self, mock_req):
        mock_req.return_value = (
            200, json.dumps({"job_status": "ABORTED"}))

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "failed")

    @patch("prow_utils.http_request")
    def test_pending(self, mock_req):
        mock_req.return_value = (
            200, json.dumps({"job_status": "TRIGGERED"}))

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "pending")
        self.assertEqual(raw, "TRIGGERED")

    @patch("prow_utils.http_request")
    def test_rate_limited_429(self, mock_req):
        mock_req.return_value = (429, "rate limited")

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "rate_limited")
        self.assertIn("429", raw)

    @patch("prow_utils.http_request")
    def test_rate_limited_5xx(self, mock_req):
        mock_req.return_value = (503, "unavailable")

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "rate_limited")

    @patch("prow_utils.http_request")
    def test_connection_failure_is_retryable(self, mock_req):
        mock_req.return_value = (0, "Connection refused")

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "rate_limited")
        self.assertIn("0", raw)

    @patch("prow_utils.http_request")
    def test_auth_error(self, mock_req):
        mock_req.return_value = (401, "unauthorized")

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "error")

    @patch("prow_utils.http_request")
    def test_invalid_json(self, mock_req):
        mock_req.return_value = (200, "not json")

        mapped, raw = get_prow_job_status(
            "https://gangway.example.com/v1/executions", "tok", "123")

        self.assertEqual(mapped, "error")


if __name__ == "__main__":
    unittest.main()
