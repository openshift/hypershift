# hcp-infra

Unified AWS infrastructure management tool for HyperShift. Scans, audits, and cleans orphaned resources left behind by HyperShift hosted cluster tests.

Replaces the previous `cleanroles` and `cleanzones` tools with a single binary that handles all AWS resource types.

## Quick Start

```bash
# Build
cd contrib/hcp-infra
go build -o hcp-infra .

# Quick audit (tag-based, fast)
./hcp-infra scan tags

# Full audit with leak detection
./hcp-infra scan detect --kubeconfig /path/to/mgmt/kubeconfig

# See only your resources
./hcp-infra scan tags --owner aabdelre

# Preview cleanup
./hcp-infra clean --infra-id kms-smoke5-lsbct --dry-run

# Execute cleanup
./hcp-infra clean --infra-id kms-smoke5-lsbct

# Clean all your orphans
./hcp-infra clean --all --owner aabdelre

# Pipeline: scan, review, clean
./hcp-infra scan tags -o json > orphans.json
cat orphans.json | jq '.groups | to_entries[] | select(.value.category == "CONFIRMED ORPHAN") | .key'
./hcp-infra clean --from-file orphans.json
```

## Commands

### `hcp-infra scan`

Discover and report on HyperShift AWS infrastructure.

| Subcommand | Description |
|------------|-------------|
| `scan tags` | Fast scan via Resource Groups Tagging API. Finds all resources tagged with `hypershift.openshift.io/infra-id`. |
| `scan deep` | Comprehensive scan using per-service APIs. Finds untagged resources too. |
| `scan detect` | Leak detection: runs both scans, cross-references with the management cluster, and categorizes resources. |

### `hcp-infra clean`

Delete orphaned resources. Requires one of `--infra-id`, `--all`, or `--from-file`.

Safety features:
- Interactive confirmation prompt (auto-skipped for non-TTY / CronJob)
- `--dry-run` global flag to preview without deleting
- Prints AWS account ID before any deletion
- Deletes in dependency order (NATs before EIPs before VPCs)

## Scan Modes

### Tag Scan (default)

Uses the AWS Resource Groups Tagging API. Every HyperShift resource is tagged with:
- `hypershift.openshift.io/infra-id` - the cluster's infrastructure ID
- `hypershift.openshift.io/cluster-name` - the cluster name
- `kubernetes.io/cluster/<infraID>: owned` - standard ownership tag

One API call discovers all infra IDs; another retrieves all resources for each ID.

Fast (seconds), but only finds properly tagged resources.

### Deep Scan

Scans every resource type directly via per-service APIs (DescribeVpcs, ListRoles, ListOpenIDConnectProviders, etc.). Matches resources to infra IDs using naming conventions when tags are absent.

Slow (30-60s across 5 regions), but finds untagged and partially tagged resources.

### Detect (Leak Detection)

Runs both scans, cross-references with the management cluster's HostedCluster resources, and categorizes every resource:

| Category | Meaning |
|----------|---------|
| `CONFIRMED ORPHAN` | Tagged with an infra ID, no VPC exists. Safe to delete. |
| `PROBABLE ORPHAN` | Matches naming convention but lacks tags. Likely a partial creation leftover. |
| `DEVELOPER-OWNED` | Tagged with a developer name. May be intentional. |
| `ACTIVE` | Has a VPC and/or a live HostedCluster. Not touched. |
| `UNKNOWN ORIGIN` | Doesn't match any convention. Needs human review. |

## Resource Types

The tool manages all AWS resources created by HyperShift:

- VPCs, Subnets, NAT Gateways, Elastic IPs, Internet Gateways
- Route Tables, Security Groups, VPC Endpoints, DHCP Options
- EC2 Instances, EBS Volumes
- IAM Roles (8 per cluster), Instance Profiles, OIDC Providers
- Route53 Hosted Zones
- S3 OIDC document prefixes

## CronJob Deployment

```bash
kubectl apply -f manifests/
```

The CronJob runs daily, cleaning all orphaned resources older than 48 hours. See `manifests/02-cronjob.yaml` for configuration.

## IAM Policy

The minimum IAM policy is in `role-policy.json`. It has two statements:
- `ScanRead`: read-only permissions for scanning all resource types
- `CleanWrite`: write permissions for deleting resources

For scan-only use, only the `ScanRead` statement is needed.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success (scan: no orphans; clean: all deleted) |
| 1 | Orphans found (scan) or partial failure (clean) |
| 2 | Error (AWS API failure, invalid flags) |

## Global Flags

| Flag | Description |
|------|-------------|
| `--region` | AWS regions to scan (default: us-east-1,us-east-2,us-west-1,us-west-2,eu-west-1; override with `$HCP_INFRA_REGIONS`) |
| `--kubeconfig` | Management cluster kubeconfig for HC cross-reference |
| `--output` / `-o` | Output format: text, json, markdown |
| `--dry-run` | Preview actions without making changes |
| `--verbose` / `-v` | Verbose output |
| `--quiet` / `-q` | Quiet output (summary only) |
