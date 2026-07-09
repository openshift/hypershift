"""HTTP utilities using only Python stdlib (urllib.request)."""

import json
import time
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError

__all__ = ["http_request", "http_request_with_retry"]


def http_request(url, method="GET", headers=None, body=None, timeout=120):
    """Perform a single HTTP request using urllib.request.

    Args:
        url: Full URL to request.
        method: HTTP method (GET, POST, etc.).
        headers: Dict of HTTP headers. If body is a dict/list and
            Content-Type is not set, it defaults to application/json.
        body: Request body. Accepts dict/list (auto-serialized to JSON),
            str (encoded to UTF-8), or bytes (sent as-is).
        timeout: Socket timeout in seconds for the HTTP connection.

    Returns:
        Tuple (status_code, response_body_str).
        On connection failure, status_code is 0 and response_body_str
        contains the error reason.
    """
    if headers is None:
        headers = {}

    data = None
    if body is not None:
        if isinstance(body, (dict, list)):
            data = json.dumps(body).encode("utf-8")
            headers.setdefault("Content-Type", "application/json")
        elif isinstance(body, str):
            data = body.encode("utf-8")
        else:
            data = body

    req = Request(url, data=data, headers=headers, method=method)

    try:
        with urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8")
    except HTTPError as e:
        body_text = ""
        try:
            body_text = e.read().decode("utf-8")
        except Exception:
            pass
        return e.code, body_text
    except URLError as e:
        print(f"  ERROR: connection failed: {e.reason}", flush=True)
        return 0, str(e.reason)


def http_request_with_retry(url, method="GET", headers=None, body=None,
                            retries=3, backoff=10, should_retry=None,
                            timeout=120):
    """Perform an HTTP request with configurable retry logic.

    Retries the request when the response status matches the retry
    condition, sleeping backoff seconds between attempts.

    Args:
        url: Full URL to request.
        method: HTTP method (GET, POST, etc.).
        headers: Dict of HTTP headers (forwarded to http_request).
        body: Request body (forwarded to http_request).
        retries: Maximum number of attempts before giving up.
        backoff: Seconds to wait between retry attempts.
        timeout: Socket timeout in seconds (forwarded to http_request).
        should_retry: Callable(status_code) -> bool that decides whether
            a given status code should trigger a retry. Defaults to
            retrying on 429 (rate limit), 5xx (server error), and
            0 (connection failure).

    Returns:
        Tuple (status_code, response_body_str) from the last attempt.
    """
    if should_retry is None:
        def should_retry(s):
            return s == 0 or s == 429 or 500 <= s < 600

    status, response = 0, ""
    for attempt in range(1, retries + 1):
        status, response = http_request(url, method, headers, body,
                                        timeout=timeout)

        if not should_retry(status):
            return status, response

        if attempt < retries:
            print(f"  WARN: HTTP {status} (attempt {attempt}/{retries})"
                  f" - retrying in {backoff}s", flush=True)
            time.sleep(backoff)
        else:
            print(f"  WARN: HTTP {status} (attempt {attempt}/{retries})"
                  f" - no retries left", flush=True)

    return status, response
