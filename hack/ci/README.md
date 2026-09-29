# HyperShift CI daily health companion

`hypershift-ci-daily-health.py` supplies the deterministic collection and rendering stages for `.chai-bot/hypershift_ci_daily_health_report.md`. It uses only the Python standard library in production and never writes to Jira, triggers CI, or mutates a cluster.

## Collect

Use one UTC timestamp and one canonical checked-out commit for the entire run:

```console
python3 hack/ci/hypershift-ci-daily-health.py collect \
  --as-of 2026-09-28T12:00:00Z \
  --source-revision "$(git rev-parse HEAD)" \
  --slack-out /tmp/hypershift-ci-stage-one.txt \
  --candidates-out /tmp/hypershift-ci-candidates.json
```

The text file is a deterministic Slack parent message shorter than 2000 characters. It owns a compact periodic payload/trend summary by release. The JSON file contains only bounded presubmit candidates grouped by branch plus a separate, non-judgment `periodic_status` section with exact stream/tag links and counts for deterministic rendering. Periodics are never sent for LLM judgment. Collection has an overall deadline and request budget in addition to per-request limits. Source failures and explicit candidate count/byte overflow are rendered as `Unknown`; expected never-run Prow history and ERROR/ABORTED-only windows remain visible coverage uncertainties rather than being reported green.

## Render judgments

The judgment document must use `schema_version: 1` and contain exactly one entry for each candidate ID:

```json
{
  "schema_version": 1,
  "source_revision": "0123456789abcdef0123456789abcdef01234567",
  "collection_id": "hci-doc-0123456789abcdef",
  "judgments": [
    {
      "candidate_id": "hci-0123456789abcdef",
      "classification": "permafail_candidate",
      "summary": "Bounded evidence summary",
      "signature": "Verified repeated signature",
      "recurring_evidence": ["Evidence from two independent runs"],
      "next_action": "Human follow-up",
      "tracking": {"status": "none"}
    }
  ]
}
```

Presubmits allow `not_permafailing`, `flaky`, `permafail_candidate`, `infrastructure_triage`, `one_off_failure`, and `no_data`. Every judgment must use `tracking.status: none`; this scheduled workflow never promotes a periodic incident or proposes a Jira action. The renderer requires the exact collection ID and source revision, and accepts `permafail_candidate` only when the bound candidate has three consecutive failures across at least two canonical PR head SHAs.

```console
python3 hack/ci/hypershift-ci-daily-health.py render \
  --source-revision "$(git rev-parse HEAD)" \
  --stage-one /tmp/hypershift-ci-stage-one.txt \
  --candidates /tmp/hypershift-ci-candidates.json \
  --judgments /tmp/hypershift-ci-judgments.json \
  --slack-out /tmp/hypershift-ci-report.txt
```

The result preserves the parent text and uses `---THREAD_DETAILS---` and `---THREAD_BREAK---` for bounded same-thread replies. Deterministic periodic replies include every validated stream, payload tag, release-status link, phase, count, and uncertainty without an LLM classification. Every presubmit candidate includes its deterministic role, Dashboard context, timestamped run evidence, uncertainty, judgment, and action. Large groups split across replies without slicing away candidates or actions; zero-candidate runs still emit a consistent thread summary.

## Offline verification

The committed fixtures model active-versus-accepted payloads, future-stream exclusion, the 5.0-to-4.23 major transition, exact health joins, exact trend boundaries, and Prow-only presubmit history.

```console
make verify-hypershift-ci-daily-health
```
