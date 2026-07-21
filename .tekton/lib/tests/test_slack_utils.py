"""Tests for slack_utils module."""

import sys
import os
import unittest
from unittest.mock import patch, MagicMock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from slack_utils import (send_slack_message, build_slack_payload,
                          mrkdwn_section, fields_section, divider)


class TestSendSlackMessage(unittest.TestCase):
    """Tests for send_slack_message."""

    @patch("slack_utils.http_request")
    def test_success(self, mock_req):
        mock_req.return_value = (200, "ok")

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"})

        self.assertTrue(result)
        self.assertEqual(mock_req.call_count, 1)

    @patch("slack_utils.http_request")
    def test_client_error_no_retry(self, mock_req):
        mock_req.return_value = (400, "invalid_payload")

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"}, retries=3)

        self.assertFalse(result)
        self.assertEqual(mock_req.call_count, 1)

    @patch("slack_utils.http_request")
    def test_403_no_retry(self, mock_req):
        mock_req.return_value = (403, "forbidden")

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"}, retries=3)

        self.assertFalse(result)
        self.assertEqual(mock_req.call_count, 1)

    @patch("slack_utils.time.sleep")
    @patch("slack_utils.http_request")
    def test_429_retries(self, mock_req, mock_sleep):
        mock_req.side_effect = [
            (429, "rate limited"),
            (429, "rate limited"),
            (429, "rate limited"),
        ]

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"},
            retries=3, delay=1)

        self.assertFalse(result)
        self.assertEqual(mock_req.call_count, 3)

    @patch("slack_utils.time.sleep")
    @patch("slack_utils.http_request")
    def test_500_retries_then_succeeds(self, mock_req, mock_sleep):
        mock_req.side_effect = [(500, "error"), (200, "ok")]

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"},
            retries=3, delay=1)

        self.assertTrue(result)
        self.assertEqual(mock_req.call_count, 2)

    @patch("slack_utils.time.sleep")
    @patch("slack_utils.http_request")
    def test_connection_failure_retries(self, mock_req, mock_sleep):
        mock_req.side_effect = [
            (0, "Connection refused"),
            (200, "ok"),
        ]

        result = send_slack_message(
            "https://hooks.slack.com/test", {"text": "hello"},
            retries=3, delay=1)

        self.assertTrue(result)
        self.assertEqual(mock_req.call_count, 2)


class TestBuildSlackPayload(unittest.TestCase):
    """Tests for build_slack_payload."""

    def test_structure(self):
        blocks = [{"type": "section", "text": {"type": "mrkdwn", "text": "hi"}}]
        payload = build_slack_payload("#2E7D32", blocks)

        self.assertIn("attachments", payload)
        self.assertEqual(len(payload["attachments"]), 1)
        self.assertEqual(payload["attachments"][0]["color"], "#2E7D32")
        self.assertEqual(payload["attachments"][0]["blocks"], blocks)


class TestMrkdwnSection(unittest.TestCase):
    """Tests for mrkdwn_section."""

    def test_structure(self):
        result = mrkdwn_section("hello *bold*")

        self.assertEqual(result["type"], "section")
        self.assertEqual(result["text"]["type"], "mrkdwn")
        self.assertEqual(result["text"]["text"], "hello *bold*")


class TestFieldsSection(unittest.TestCase):
    """Tests for fields_section."""

    def test_structure(self):
        result = fields_section(["*A:*\nval1", "*B:*\nval2"])

        self.assertEqual(result["type"], "section")
        self.assertEqual(len(result["fields"]), 2)
        self.assertEqual(result["fields"][0]["type"], "mrkdwn")
        self.assertEqual(result["fields"][0]["text"], "*A:*\nval1")

    def test_four_fields(self):
        result = fields_section(["a", "b", "c", "d"])
        self.assertEqual(len(result["fields"]), 4)


class TestDivider(unittest.TestCase):
    """Tests for divider."""

    def test_structure(self):
        self.assertEqual(divider(), {"type": "divider"})


if __name__ == "__main__":
    unittest.main()
