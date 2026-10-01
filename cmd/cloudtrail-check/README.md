# CloudTrail permission-denied checker

`cloudtrail-check` scans saved HyperShift dump artifacts for AWS role ARNs, then queries CloudTrail event history for permission-denied **management events** during the e2e run. It does not inspect data events, including S3 object operations. CloudTrail event history covers only the last 90 days. This command is intended to run as a post-e2e CI step; it is not part of local e2e hooks.

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
- `--region`: extra region; defaults to `AWS_REGION` when set. HostedCluster AWS platform regions and independent AWS KMS regions found in artifacts are always queried.
- `--output`: report destination; defaults to `cloudtrail-permission-denied.json` in the artifact directory.

The checker scans HostedCluster AWS role references and KMS role configuration, Pod `AWS_ROLE_ARN` environment variables, and ServiceAccount `eks.amazonaws.com/role-arn` annotations when present in artifacts.

## Regions and Credentials

Regions are validated against the `supportedCloudTrailRegions` allowlist in `cloudtrail.go`, copied from the vendored CloudTrail SDK v1.52.0 endpoint metadata. Unknown or unsupported regions and mixed-partition region or role ARN inputs are rejected before AWS configuration is loaded or requests are made. Supporting newly launched regions requires updating this allowlist from supported CloudTrail endpoints, not guessing an endpoint.

Each invocation supports one partition: commercial AWS (`aws`), GovCloud (`aws-us-gov`), or China (`aws-cn`). AWS credentials use the standard AWS SDK credential chain and must match the selected partition. Keep the start time and credentials scoped to the e2e job.

In addition to artifact and flag/environment regions, the checker includes the partition's global-service regions:

- Commercial AWS: `us-east-1`.
- GovCloud: `us-gov-west-1`.
- China: both `cn-north-1` and `cn-northwest-1`.

## Results

The JSON report includes discovered roles, queried regions, matching management-event denial details, and warnings. Events from completed pages and regions are appended to the report even if a later page or region fails. Query failures set `partial: true` and retain any earlier denials.

A successful query with no observed management-event denials exits 0, but the result is **provisional**, not proof that no denial occurred. The report sets `provisional: true` and includes a warning, and stdout explicitly identifies the result as provisional. CloudTrail is eventually consistent: this command does not wait for events to settle, recheck, or retry to wait for event arrival. The AWS SDK may still retry transient request failures. The entire query has a five-minute timeout and honors earlier parent-context cancellation.

Failure behavior is unchanged: observed management-event denials or an incomplete query exit 1. Artifact scanning failures, no discovered AWS roles, and report-writing failures also exit 1; invalid command arguments exit 2. Query failures are partial, not provisional clean results.

The report destination file is atomically replaced after a complete temporary file is written in the same directory. An existing destination symlink is replaced rather than followed, leaving its target untouched. Parent-directory symlinks are not confined by this mechanism.

## Artifact limitation

Current `hypershift dump` output does not include ServiceAccounts. ServiceAccount annotation discovery therefore becomes effective after CNTRLPLANE-4444 adds those resources to dump artifacts. HostedCluster roles and Pod environment variables remain available with current dump output.

Prow job wiring lives outside this repository (likely `openshift/release`) and must invoke this command after collecting the dump artifacts.
