"""Slack webhook utilities (stdlib only)."""

import time
from http_utils import http_request

__all__ = ["send_slack_message", "build_slack_payload", "mrkdwn_section",
           "fields_section", "divider"]


def send_slack_message(webhook_url, payload, retries=3, delay=10):
    """POST a JSON payload to a Slack incoming webhook with retry.

    Retries on 429 (rate limit), 5xx (server error), and connection
    failures. Fails fast on 4xx client errors (e.g. bad payload).

    Args:
        webhook_url: Full Slack incoming webhook URL.
        payload: Dict to serialize as JSON and send as the request body.
        retries: Maximum number of send attempts.
        delay: Seconds to wait between retry attempts.

    Returns:
        True if the message was sent successfully, False otherwise.
    """
    for attempt in range(1, retries + 1):
        status, response = http_request(
            webhook_url, method="POST", body=payload
        )

        if status == 200:
            print(f"Slack notification sent (attempt {attempt})", flush=True)
            return True

        if status == 0:
            print(f"  ERROR: connection failed (attempt {attempt}/{retries})",
                  flush=True)
        elif 400 <= status < 500 and status != 429:
            print(f"  ERROR: Slack returned HTTP {status} - not retrying",
                  flush=True)
            if response:
                print(f"  response: {response}", flush=True)
            return False
        else:
            print(f"  WARN: Slack returned HTTP {status}"
                  f" (attempt {attempt}/{retries})", flush=True)
            if response:
                print(f"  response: {response}", flush=True)

        if attempt < retries:
            print(f"  retrying in {delay}s...", flush=True)
            time.sleep(delay)

    return False


def build_slack_payload(color, blocks):
    """Build a Slack webhook payload with a single colored attachment.

    Args:
        color: Hex color string for the attachment sidebar (e.g. "#2E7D32").
        blocks: List of Slack Block Kit block dicts.

    Returns:
        Dict ready to be passed to send_slack_message.
    """
    return {"attachments": [{"color": color, "blocks": blocks}]}


def mrkdwn_section(text):
    """Build a Slack Block Kit section with mrkdwn text.

    Args:
        text: Slack mrkdwn-formatted string.

    Returns:
        Block Kit section dict.
    """
    return {"type": "section", "text": {"type": "mrkdwn", "text": text}}


def fields_section(field_texts):
    """Build a Slack Block Kit section with multiple mrkdwn fields.

    Fields are rendered side-by-side in the Slack UI (up to 2 per row).

    Args:
        field_texts: List of Slack mrkdwn-formatted strings, one per field.

    Returns:
        Block Kit section dict with fields array.
    """
    return {
        "type": "section",
        "fields": [{"type": "mrkdwn", "text": t} for t in field_texts]
    }


def divider():
    """Build a Slack Block Kit divider block.

    Returns:
        Block Kit divider dict.
    """
    return {"type": "divider"}
