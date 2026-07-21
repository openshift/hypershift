"""Tests for http_utils module."""

import io
import json
import sys
import os
import time
import unittest
from unittest.mock import patch, MagicMock
from urllib.error import HTTPError, URLError

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from http_utils import http_request, http_request_with_retry


class TestHttpRequest(unittest.TestCase):
    """Tests for http_request."""

    @patch("http_utils.urlopen")
    def test_get_success(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b"OK"
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = resp

        status, body = http_request("https://example.com/api")

        self.assertEqual(status, 200)
        self.assertEqual(body, "OK")
        mock_urlopen.assert_called_once()

    @patch("http_utils.urlopen")
    def test_post_with_dict_body(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b'{"ok":true}'
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = resp

        payload = {"key": "value"}
        status, body = http_request("https://example.com/api",
                                    method="POST", body=payload)

        self.assertEqual(status, 200)
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.data, json.dumps(payload).encode("utf-8"))
        self.assertEqual(req.get_header("Content-type"), "application/json")

    @patch("http_utils.urlopen")
    def test_post_with_string_body(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b"ok"
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = resp

        status, body = http_request("https://example.com/api",
                                    method="POST", body="raw text")

        self.assertEqual(status, 200)
        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.data, b"raw text")

    @patch("http_utils.urlopen")
    def test_http_error(self, mock_urlopen):
        error = HTTPError("https://example.com", 404, "Not Found",
                          {}, io.BytesIO(b"not found"))
        mock_urlopen.side_effect = error

        status, body = http_request("https://example.com/api")

        self.assertEqual(status, 404)
        self.assertEqual(body, "not found")

    @patch("http_utils.urlopen")
    def test_url_error(self, mock_urlopen):
        mock_urlopen.side_effect = URLError("Connection refused")

        status, body = http_request("https://example.com/api")

        self.assertEqual(status, 0)
        self.assertIn("Connection refused", body)

    @patch("http_utils.urlopen")
    def test_custom_headers(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b""
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = resp

        http_request("https://example.com/api",
                     headers={"Authorization": "Bearer tok123"})

        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_header("Authorization"), "Bearer tok123")

    @patch("http_utils.urlopen")
    def test_post_dict_body_does_not_override_content_type(self, mock_urlopen):
        resp = MagicMock()
        resp.status = 200
        resp.read.return_value = b""
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = resp

        http_request("https://example.com/api", method="POST",
                     headers={"Content-Type": "text/plain"},
                     body={"key": "value"})

        req = mock_urlopen.call_args[0][0]
        self.assertEqual(req.get_header("Content-type"), "text/plain")


class TestHttpRequestWithRetry(unittest.TestCase):
    """Tests for http_request_with_retry."""

    @patch("http_utils.http_request")
    def test_success_first_try(self, mock_req):
        mock_req.return_value = (200, "OK")

        status, body = http_request_with_retry("https://example.com/api")

        self.assertEqual(status, 200)
        self.assertEqual(body, "OK")
        self.assertEqual(mock_req.call_count, 1)

    @patch("http_utils.time.sleep")
    @patch("http_utils.http_request")
    def test_retry_on_429(self, mock_req, mock_sleep):
        mock_req.side_effect = [(429, "rate limited"), (200, "OK")]

        status, body = http_request_with_retry(
            "https://example.com/api", retries=3, backoff=5)

        self.assertEqual(status, 200)
        self.assertEqual(mock_req.call_count, 2)
        mock_sleep.assert_called_once_with(5)

    @patch("http_utils.time.sleep")
    @patch("http_utils.http_request")
    def test_retry_on_500(self, mock_req, mock_sleep):
        mock_req.side_effect = [(500, "error"), (200, "OK")]

        status, body = http_request_with_retry(
            "https://example.com/api", retries=3, backoff=5)

        self.assertEqual(status, 200)
        self.assertEqual(mock_req.call_count, 2)

    @patch("http_utils.time.sleep")
    @patch("http_utils.http_request")
    def test_all_retries_fail(self, mock_req, mock_sleep):
        mock_req.return_value = (503, "unavailable")

        status, body = http_request_with_retry(
            "https://example.com/api", retries=3, backoff=1)

        self.assertEqual(status, 503)
        self.assertEqual(body, "unavailable")
        self.assertEqual(mock_req.call_count, 3)

    @patch("http_utils.http_request")
    def test_no_retry_on_client_error(self, mock_req):
        mock_req.return_value = (400, "bad request")

        status, body = http_request_with_retry(
            "https://example.com/api", retries=3)

        self.assertEqual(status, 400)
        self.assertEqual(mock_req.call_count, 1)

    @patch("http_utils.time.sleep")
    @patch("http_utils.http_request")
    def test_retry_on_connection_failure(self, mock_req, mock_sleep):
        mock_req.side_effect = [(0, "Connection refused"), (200, "OK")]

        status, body = http_request_with_retry(
            "https://example.com/api", retries=3, backoff=2)

        self.assertEqual(status, 200)
        self.assertEqual(mock_req.call_count, 2)

    @patch("http_utils.http_request")
    def test_custom_should_retry(self, mock_req):
        mock_req.return_value = (418, "teapot")

        status, body = http_request_with_retry(
            "https://example.com/api", retries=2,
            should_retry=lambda s: s == 418)

        self.assertEqual(status, 418)
        self.assertEqual(mock_req.call_count, 2)


if __name__ == "__main__":
    unittest.main()
