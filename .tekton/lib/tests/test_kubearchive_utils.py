"""Tests for kubearchive_utils module."""

import json
import sys
import os
import unittest
from unittest.mock import patch

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from kubearchive_utils import (fetch_pipelineruns, build_pipelinerun_url,
                               STALE_QUERY_LIMIT)


class TestFetchPipelineruns(unittest.TestCase):
    """Tests for fetch_pipelineruns."""

    def _make_item(self, name, created, reason="Succeeded",
                   cond_status="True"):
        return {
            "metadata": {
                "name": name,
                "creationTimestamp": created,
            },
            "status": {
                "conditions": [{"reason": reason, "status": cond_status}],
            },
        }

    @patch("kubearchive_utils.http_request_with_retry")
    def test_valid_response(self, mock_req):
        items = [
            self._make_item("run-a", "2026-07-01T10:00:00Z",
                            "Completed", "True"),
            self._make_item("run-b", "2026-07-03T10:00:00Z",
                            "Failed", "False"),
        ]
        mock_req.return_value = (200, json.dumps({"items": items}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(len(result), 2)
        self.assertEqual(result[0]["name"], "run-b")
        self.assertEqual(result[1]["name"], "run-a")
        self.assertFalse(result[0]["status"])
        self.assertTrue(result[1]["status"])
        self.assertEqual(result[0]["reason"], "Failed")
        self.assertEqual(result[1]["reason"], "Completed")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_succeeded_reason(self, mock_req):
        items = [
            self._make_item("run-old", "2026-06-18T10:00:00Z",
                            "Succeeded", "True"),
        ]
        mock_req.return_value = (200, json.dumps({"items": items}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertTrue(result[0]["status"])
        self.assertEqual(result[0]["reason"], "Succeeded")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_pipeline_run_timeout(self, mock_req):
        items = [
            self._make_item("run-timeout", "2026-07-04T03:15:14Z",
                            "PipelineRunTimeout", "False"),
        ]
        mock_req.return_value = (200, json.dumps({"items": items}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertFalse(result[0]["status"])
        self.assertEqual(result[0]["reason"], "PipelineRunTimeout")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_couldnt_get_pipeline(self, mock_req):
        items = [
            self._make_item("run-no-pipe", "2026-06-18T15:59:32Z",
                            "CouldntGetPipeline", "False"),
        ]
        mock_req.return_value = (200, json.dumps({"items": items}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertFalse(result[0]["status"])
        self.assertEqual(result[0]["reason"], "CouldntGetPipeline")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_empty_items(self, mock_req):
        mock_req.return_value = (200, json.dumps({"items": []}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(result, [])

    @patch("kubearchive_utils.http_request_with_retry")
    def test_non_200_response(self, mock_req):
        mock_req.return_value = (503, "unavailable")

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(result, [])

    @patch("kubearchive_utils.http_request_with_retry")
    def test_malformed_json(self, mock_req):
        mock_req.return_value = (200, "not json at all")

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(result, [])

    @patch("kubearchive_utils.http_request_with_retry")
    def test_missing_conditions(self, mock_req):
        item = {
            "metadata": {"name": "run-x", "creationTimestamp": "2026-07-01T00:00:00Z"},
            "status": {},
        }
        mock_req.return_value = (200, json.dumps({"items": [item]}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(len(result), 1)
        self.assertFalse(result[0]["status"])
        self.assertEqual(result[0]["reason"], "Unknown")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_missing_metadata_fields(self, mock_req):
        item = {"metadata": {}, "status": {"conditions": [
            {"reason": "OK", "status": "True"}]}}
        mock_req.return_value = (200, json.dumps({"items": [item]}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(len(result), 1)
        self.assertEqual(result[0]["name"], "")
        self.assertEqual(result[0]["created"], "")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_non_dict_condition(self, mock_req):
        item = {
            "metadata": {"name": "run-x", "creationTimestamp": "2026-07-01T00:00:00Z"},
            "status": {"conditions": ["not-a-dict"]},
        }
        mock_req.return_value = (200, json.dumps({"items": [item]}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertFalse(result[0]["status"])
        self.assertEqual(result[0]["reason"], "Unknown")

    @patch("kubearchive_utils.http_request_with_retry")
    def test_query_includes_limit_and_time_filter(self, mock_req):
        mock_req.return_value = (200, json.dumps({"items": []}))

        fetch_pipelineruns("tok", "ns", "label=val")

        url = mock_req.call_args[0][0]
        self.assertIn(f"limit={STALE_QUERY_LIMIT}", url)
        self.assertIn("creationTimestampAfter=", url)

    @patch("kubearchive_utils.http_request_with_retry")
    def test_sort_order(self, mock_req):
        items = [
            self._make_item("oldest", "2026-07-01T00:00:00Z"),
            self._make_item("newest", "2026-07-05T00:00:00Z"),
            self._make_item("middle", "2026-07-03T00:00:00Z"),
        ]
        mock_req.return_value = (200, json.dumps({"items": items}))

        result = fetch_pipelineruns("tok", "ns", "label=val")

        self.assertEqual(result[0]["name"], "newest")
        self.assertEqual(result[1]["name"], "middle")
        self.assertEqual(result[2]["name"], "oldest")


class TestBuildPipelinerunUrl(unittest.TestCase):
    """Tests for build_pipelinerun_url."""

    def test_basic_url(self):
        url = build_pipelinerun_url(
            "run-abc",
            "https://konflux-ui.example.com/ns/test/applications/app")

        self.assertEqual(
            url,
            "https://konflux-ui.example.com/ns/test/applications/app"
            "/pipelineruns/run-abc/")

    def test_trailing_slash(self):
        url = build_pipelinerun_url("run-x", "https://example.com/base")
        self.assertTrue(url.endswith("/"))


if __name__ == "__main__":
    unittest.main()
