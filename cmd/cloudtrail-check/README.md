# CloudTrail permission-denied checker

`cloudtrail-check` scans saved HyperShift dump artifacts for AWS role ARNs, then queries CloudTrail for permission-denied events during the e2e run. It is intended to run as a post-e2e CI step; it is not part of local e2e hooks.

## Run

```sh
CLOUDTRAIL_START_TIME=2026-09-29T10:00:00Z \
  go run ./cmd/cloudtrail-check \
  --artifacts-dir /path/to/dump-artifacts
```

Options:

- `--artifacts-dir`: recursively scanned directory; defaults to `ARTIFACT_DIR`, then `.`.
- `--start-time`: required RFC3339 timestamp; defaults to `CLOUDTRAIL_START_TIME`.
- `--end-time`: RFC3339 timestamp; defaults to current time.
- `--region`: extra region; defaults to `AWS_REGION` when set. Regions found in HostedCluster artifacts are always queried.
- `--output`: report destination; defaults to `cloudtrail-permission-denied.json` in the artifact directory.

AWS credentials use the standard AWS SDK credential chain. The checker queries `us-east-1` for global-service events in addition to discovered regions. It scans HostedCluster AWS role references and KMS role configuration, Pod `AWS_ROLE_ARN` environment variables, and ServiceAccount `eks.amazonaws.com/role-arn` annotations when present in artifacts.

The JSON report includes discovered roles, queried regions, event details, and query warnings. Exit status is non-zero when permission-denied events are found, artifacts contain no AWS roles, or CloudTrail cannot be queried completely. Keep the start time and credentials scoped to the e2e job.

## Artifact limitation

Current `hypershift dump` output does not include ServiceAccounts. ServiceAccount annotation discovery therefore becomes effective after CNTRLPLANE-4444 adds those resources to dump artifacts. HostedCluster roles and Pod environment variables remain available with current dump output.

Prow job wiring lives outside this repository (likely `openshift/release`) and must invoke this command after collecting the dump artifacts.
