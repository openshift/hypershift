# cleanleaked

Detect and clean leaked AWS resources left behind by failed HyperShift CI test runs.

Replaces the previous bash scripts (`detect-leaked-vpcs.sh`, `detect-leaked-hosted-zones.sh`, etc.) with a single Go tool that is testable, safe by design, and consolidates all detection logic.

## Quick Start

```bash
# From the repo root
make -C contrib/aws-leaked-resources build

# Scan — report only, no deletions
./contrib/aws-leaked-resources/cleanleaked scan

# Preview what would be deleted
./contrib/aws-leaked-resources/cleanleaked delete --dry-run

# Delete interactively, 5 at a time
./contrib/aws-leaked-resources/cleanleaked delete --limit 5 --interactive
```

Or run directly with `go run`:

```bash
go run ./contrib/aws-leaked-resources/ scan
go run ./contrib/aws-leaked-resources/ delete --dry-run
```

## Classification Gates

Each VPC passes through a series of gates in order. The first gate that matches determines the verdict.

| Gate | Check | Verdict if matched |
|------|-------|--------------------|
| **0. Protection** | `do-not-delete=true` tag, protected VPC name, protected username in infraID | **PROTECTED** |
| **1. Age** | `expirationDate` tag not expired, or VPCE/ENI creation < `--min-age` | **TOO_YOUNG** |
| **2. OIDC/S3** | S3 discovery document exists in any allowed OIDC bucket | **ACTIVE** |
| **3. EC2 Instances** | Running/pending/stopped instances tagged with this infraID | **ACTIVE** |
| **4. CI Pattern** | infraID does not match any known CI test pattern | **UNCERTAIN** |
| **5. (all gates pass)** | No OIDC, no instances, matches CI pattern, old enough | **LEAKED** |

### Protection (Gate 0)

Three layers of protection, checked in order:

1. **Tag-based**: VPC has `hypershift.openshift.io/do-not-delete=true`
2. **Name-based**: VPC name is `hypershift-ci-2-vpc`, `hypershift-ci-3-vpc`, or `hypershift-ci-metrics-vpc`
3. **User-based**: infraID contains a protected team member username

Protected resources are **never** deleted regardless of other signals.

### Age (Gate 1)

Two sources of age information:

- **`expirationDate` tag** (preferred): Set by newer CI jobs. If present and not expired, the VPC is `TOO_YOUNG`.
- **VPCE/ENI timestamps** (fallback): For older resources without the tag, the earliest `CreationTimestamp` from VPC Endpoints or `AttachTime` from ENIs is used. Must be older than `--min-age` (default 24h).

### CI Pattern Match (Gate 4)

Known CI test patterns:
- Hex infraIDs (20+ lowercase hex chars): `00ab3695c5f73d4354b9`
- Named test prefixes: `create-cluster-*`, `node-pool-*`, `control-plane-upgrade-*`, `autoscaling-*`, `karpenter-*`, `scale-from-zero-*`, `kms-verify-*`, `request-serving-*`, `private-*`, `proxy-*`, `spot-demo-*`, `ha-break-glass-creds-*`, `custom-config-*`, `ho-upgrade-*`, `multi-hop-upgrade-*`

InfraIDs that don't match (e.g., `hc1-*`, `clust-*`, `dev-*`) are classified as `UNCERTAIN`.

## Verdicts

| Verdict | Meaning | Deletable? |
|---------|---------|------------|
| `PROTECTED` | Management cluster, developer resource, or `do-not-delete` tag | Never |
| `TOO_YOUNG` | Created less than `--min-age` ago or `expirationDate` not reached | No |
| `ACTIVE` | OIDC S3 doc exists or EC2 instances running | No |
| `UNCERTAIN` | No CI pattern match — may belong to a developer | Only manually with `--interactive` |
| `LEAKED` | No OIDC, no instances, matches CI pattern, old enough | Yes |

## Delete Modes

| Command | Behavior |
|---------|----------|
| `delete` | Prompt once at the start, then delete all LEAKED |
| `delete --confirm` | No prompts, delete all LEAKED immediately |
| `delete --interactive` | Show full resource inventory per infra set, prompt for each |
| `delete --dry-run` | Show what would be deleted, delete nothing |

### Cascade Order

When deleting a VPC, sub-resources are removed in this order:

1. ELBs/NLBs
2. VPC Endpoint Services (by infraID tag)
3. VPC Endpoints
4. NAT Gateways → wait 15s for ENI release
5. Elastic IPs (only those from the NAT gateways)
6. Internet Gateways
7. Route Tables (non-main only)
8. Network Interfaces
9. Subnets
10. Security Groups (non-default only, revoke rules first)
11. VPC

Additionally per infra set: Route53 zones, OIDC IAM providers, IAM roles.

Each delete operation retries up to 10 times with 2s delay. Ctrl+C interrupts immediately.

## Flags

### `scan` command

| Flag | Default | Description |
|------|---------|-------------|
| `--region` | `us-east-1` | AWS region |
| `--min-age` | `24h` | Minimum VPC age to consider for deletion |
| `--target` | *(none)* | Scan a single infraID or VPC ID instead of all VPCs |
| `--output` | `table` | Output format: `table` or `json` |
| `--output-dir` | `output` | Directory for timestamped report files (empty to disable) |

### `delete` command

| Flag | Default | Description |
|------|---------|-------------|
| `--region` | `us-east-1` | AWS region |
| `--min-age` | `24h` | Minimum VPC age to consider for deletion |
| `--target` | *(none)* | Delete a single infraID or VPC ID instead of all leaked |
| `--dry-run` | `false` | Show what would be deleted without deleting |
| `--confirm` | `false` | No prompts, delete all LEAKED immediately |
| `--interactive`, `-i` | `false` | Prompt before each infra set with full inventory |
| `--limit` | `0` | Max infra sets to delete (0 = no limit) |
| `--output` | `table` | Output format: `table` or `json` |
| `--output-dir` | `output` | Directory for timestamped report files (empty to disable) |

### `--target` usage

Accepts a VPC ID (`vpc-*`) or an infraID. If an infraID is given, it searches for a VPC with the Name tag `<infraID>-vpc`.

```bash
# By VPC ID
cleanleaked scan --target vpc-066231b8dc5a4400f

# By infraID
cleanleaked scan --target node-pool-78vcg

# Also accepts the -vpc suffix (stripped automatically)
cleanleaked scan --target node-pool-78vcg-vpc

# Delete a specific target interactively
cleanleaked delete --target 00ab3695c5f73d4354b9 --interactive
```

## Prow Correlation

For each LEAKED infra set, the tool infers:
- **Test type**: from the infraID pattern (e.g., `node-pool`, `create-cluster`, `e2e-generic`)
- **HC namespace**: from Route53 `service.ci` zone TXT records
- **Prow link**: direct job URL if `hypershift.openshift.io/prow-job-id` tag exists (PR #8909), otherwise a Prow deck search URL

## Safety

- **Default SG** (`GroupName=default`) is never deleted
- **Main route table** is never deleted (auto-deleted with VPC)
- **EIPs** are scoped: only EIPs from NAT gateways of the target VPC are released
- **ELBs** are filtered by VPC ID before deletion
- All delete operations check `ctx.Done()` between steps — Ctrl+C works immediately

## Development

```bash
make build    # Build binary
make test     # Run unit tests
make vet      # Run go vet
make scan     # Run scan against real AWS
make dry-run  # Run delete --dry-run against real AWS
make clean    # Remove binary
```
