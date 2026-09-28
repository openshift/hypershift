# HyperShift CI daily health companion

`hypershift-ci-daily-health.py` supplies the deterministic collection and rendering stages for `.chai-bot/hypershift_ci_daily_health_report.md`. It uses only the Python standard library in production and never writes to Jira, triggers CI, or mutates a cluster.

## Collect

Use one UTC timestamp for the entire run:

```console
python3 hack/ci/hypershift-ci-daily-health.py collect \
  --as-of 2026-09-28T12:00:00Z \
  --slack-out /tmp/hypershift-ci-stage-one.txt \
  --candidates-out /tmp/hypershift-ci-candidates.json
```

The text file is a deterministic Slack parent message shorter than 2000 characters. The JSON file contains only bounded candidates and public evidence for later LLM judgment. Collection has an overall deadline and request budget in addition to per-request limits. Source failures and explicit candidate count/byte overflow are rendered as `Unknown`; expected never-run Prow history is retained separately as visible `no_data` coverage rather than misreported as a transport outage.

## Render judgments

The judgment document must use `schema_version: 1` and contain exactly one entry for each candidate ID:

```json
{
  "schema_version": 1,
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

Presubmits allow `not_permafailing`, `flaky`, `permafail_candidate`, `infrastructure_triage`, `one_off_failure`, and `no_data`. Periodics allow `flaky`, `infrastructure_triage`, `one_off_failure`, `incident_candidate`, `payload_impact_unknown`, and `no_data`. Only a periodic `incident_candidate` can use `tracking.status` `existing` or `gap`; all other judgments use `none`. Existing tracking must be a verified public OCPBUGS or CNTRLPLANE key.

```console
python3 hack/ci/hypershift-ci-daily-health.py render \
  --stage-one /tmp/hypershift-ci-stage-one.txt \
  --candidates /tmp/hypershift-ci-candidates.json \
  --judgments /tmp/hypershift-ci-judgments.json \
  --slack-out /tmp/hypershift-ci-report.txt
```

The result preserves the parent text and uses `---THREAD_DETAILS---` and `---THREAD_BREAK---` for bounded same-thread replies. Every candidate includes its deterministic role, Dashboard context, exact trend where applicable, timestamped run evidence, payload evidence, uncertainty, judgment, tracking state, and action. Large groups split across replies without slicing away candidates or actions; zero-candidate runs still emit a consistent thread summary.

## Offline verification

The committed fixtures model active-versus-accepted payloads, future-stream exclusion, the 5.0-to-4.23 major transition, exact health joins, exact trend boundaries, and Prow-only presubmit history.

```console
make verify-hypershift-ci-daily-health
```
