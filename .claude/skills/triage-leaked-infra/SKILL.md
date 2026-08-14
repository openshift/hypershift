---
name: triage-leaked-infra
description: "Assess whether an AWS VPC or infra set from HyperShift CI is safe to delete. Use when the user pastes cleanleaked output and asks 'can I delete this?', 'is this safe to remove?', 'triage this infra', asks about a LEAKED or UNCERTAIN verdict, provides a VPC ID or infraID and wants to know if it's orphaned, or says 'check this VPC'. Also use when the user asks 'should I delete this?' about any AWS resource in the HyperShift CI account."
---

# Triage Leaked Infrastructure

Assess whether an AWS infrastructure set (VPC + associated resources) from HyperShift CI is safe to delete. Every claim must be backed by an empirical AWS query — never assume, always verify.

## Input

The user provides one of:
- cleanleaked tool output (an infra set block)
- A VPC ID (e.g., `vpc-0abc123...`)
- An infraID (e.g., `00ab3695c5f73d4354b9` or `node-pool-78vcg`)

Extract the **infraID** and **VPC ID** from the input. If only one is given, derive the other:
- VPC ID → `aws ec2 describe-vpcs --vpc-ids <VPC> --query 'Vpcs[0].Tags'` → extract infraID from `kubernetes.io/cluster/<infraID>` tag or `Name` tag (strip `-vpc` suffix)
- infraID → `aws ec2 describe-vpcs --filters "Name=tag:Name,Values=<infraID>-vpc"` → get VPC ID

## Checks

Run these in order. For each, report **PASS** (safe signal), **FAIL** (do NOT delete), or **UNKNOWN** (could not determine). Use `--region us-east-1` for all commands.

### 1. Protection tags
```bash
aws ec2 describe-vpcs --vpc-ids <VPC> --query 'Vpcs[0].Tags' --output json
```
- Check for `hypershift.openshift.io/do-not-delete=true` → if present: **FAIL**
- Check for `hypershift.openshift.io/ci-cluster` → if present: **FAIL** (this is a management cluster)

### 2. Protected VPC name
From the `Name` tag: if it is `hypershift-ci-2-vpc`, `hypershift-ci-3-vpc`, or `hypershift-ci-metrics-vpc` → **FAIL**

### 3. Protected user
Check if the infraID contains any of these usernames: `aabdelre`, `agarcial`, `ahmed`, `alamela`, `alesross`, `bclement`, `brcox`, `celebdor`, `cewong`, `dario`, `dari2o`, `dmace`, `glipceanu`, `jiezhao`, `jparrill`, `mbhalodi`, `mbrown`, `meha`, `mgencur`, `mulham`, `mraee`, `rkshirsa`, `sdminonne`, `sjenning`, `tsegura`, `vismishr`

If match → **FAIL** (developer cluster, contact the owner)

### 4. Expiration date / Age
From the VPC tags, check `expirationDate`:
- If present and **not yet expired** → **FAIL**
- If present and **expired** → **PASS**
- If absent → check resource age instead (the `expirationDate` tag only exists on resources created in the last ~2 weeks)

When `expirationDate` is absent, determine age from the earliest timestamped sub-resource:
```bash
aws ec2 describe-vpc-endpoints --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'VpcEndpoints[*].CreationTimestamp' --output text
aws ec2 describe-network-interfaces --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'NetworkInterfaces[*].Attachment.AttachTime' --output text
```
Take the earliest timestamp found. If the resource is **older than 24 hours** AND the CI pattern check (step 5) is **PASS** → **PASS**. If younger than 24 hours → **FAIL**. If no timestamp found at all → **UNKNOWN**.

### 5. CI pattern match
Does the infraID match a known CI test pattern?
- **Hex ID** (20+ lowercase hex chars, e.g., `00ab3695c5f73d4354b9`) → CI generic e2e → **PASS**
- **Named test**: `create-cluster-*`, `node-pool-*`, `control-plane-upgrade-*`, `autoscaling-*`, `karpenter-*`, `karpenter-upgrade-control-plane-*`, `scale-from-zero-*`, `kms-verify-*`, `request-serving-*`, `private-*`, `proxy-*`, `spot-demo-*`, `ha-break-glass-creds-*`, `custom-config-*`, `ho-upgrade-*`, `multi-hop-upgrade-*` → **PASS**
- **None of the above** (e.g., `hc1-*`, `clust-*`, `dev-*`, `test-dev-*`) → **UNKNOWN** (might be a developer cluster)

### 6. OIDC S3 liveness
```bash
aws s3api head-object --bucket hypershift-ci-oidc --key "<infraID>/.well-known/openid-configuration" --region us-east-1
aws s3api head-object --bucket hypershift-ci-2-oidc --key "<infraID>/.well-known/openid-configuration" --region us-east-1
aws s3api head-object --bucket hypershift-ci-3-oidc --key "<infraID>/.well-known/openid-configuration" --region us-east-1
```
If any returns 200 → **FAIL** (cluster still active). If all return 404/error → **PASS**.

### 7. EC2 instances
```bash
aws ec2 describe-instances --region us-east-1 \
  --filters "Name=tag:kubernetes.io/cluster/<infraID>,Values=owned" \
           "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[*].Instances[*].[InstanceId,State.Name,Tags[?Key==`Name`].Value|[0]]' --output text
```
If any instances returned → **FAIL**. If none → **PASS**.

### 8. red-hat-managed check
```bash
aws ec2 describe-instances --region us-east-1 \
  --filters "Name=vpc-id,Values=<VPC>" "Name=tag:red-hat-managed,Values=true" \
  --query 'Reservations[*].Instances[*].InstanceId' --output text
```
If any instances returned → **FAIL** (ROSA managed infrastructure). If none → **PASS**.

### 9. Sub-resources inventory (VPC-scoped)
Query each and report **ID + Name** for every resource, not just counts:
```bash
aws elbv2 describe-load-balancers --region us-east-1 --query "LoadBalancers[?VpcId=='<VPC>'].[LoadBalancerName,Type,Scheme,DNSName]" --output text
aws ec2 describe-vpc-endpoints --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'VpcEndpoints[*].[VpcEndpointId,VpcEndpointType,ServiceName,State]' --output text
aws ec2 describe-vpc-endpoint-service-configurations --region us-east-1 --output json | # filter by kubernetes.io/cluster/<infraID> tag
aws ec2 describe-nat-gateways --region us-east-1 --filter "Name=vpc-id,Values=<VPC>" --query 'NatGateways[*].[NatGatewayId,State,Tags[?Key==`Name`].Value|[0],NatGatewayAddresses[0].PublicIp]' --output text
aws ec2 describe-internet-gateways --region us-east-1 --filters "Name=attachment.vpc-id,Values=<VPC>" --query 'InternetGateways[*].[InternetGatewayId,Tags[?Key==`Name`].Value|[0]]' --output text
aws ec2 describe-subnets --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'Subnets[*].[SubnetId,CidrBlock,AvailabilityZone,Tags[?Key==`Name`].Value|[0]]' --output text
aws ec2 describe-security-groups --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'SecurityGroups[*].[GroupId,GroupName]' --output text
aws ec2 describe-network-interfaces --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'NetworkInterfaces[*].[NetworkInterfaceId,InterfaceType,Description]' --output text
aws ec2 describe-route-tables --region us-east-1 --filters "Name=vpc-id,Values=<VPC>" --query 'RouteTables[*].[RouteTableId,Associations[0].Main,Tags[?Key==`Name`].Value|[0]]' --output text
aws ec2 describe-addresses --region us-east-1 --filters "Name=domain,Values=vpc" --output text | # cross-check with NAT gateway EIPs
```
This check is informational — always **PASS** but list every resource with its ID and name.

### 10. Route53 zones
```bash
aws route53 list-hosted-zones --output text --query 'HostedZones[*].[Id,Name,Config.PrivateZone,ResourceRecordSetCount]'
```
Search for zones matching `<infraID>.ci.hypershift.devcluster.openshift.com` and `<infraID>.hypershift.local`. For each found zone, list its records:
```bash
aws route53 list-resource-record-sets --hosted-zone-id <ZONE_ID> --query 'ResourceRecordSets[*].[Name,Type]' --output text
```
Report zone IDs, names, private/public, and record count. Informational — always **PASS**.

### 11. OIDC IAM provider
```bash
aws iam list-open-id-connect-providers --output json
```
Search for providers whose ARN contains the infraID (pattern: `oidc-provider/hypershift-ci-*-oidc.s3.*.amazonaws.com/<infraID>`). Report if found — this is an orphaned IAM resource that should be cleaned up with the infra set.

### 12. IAM roles (by infraID prefix)
```bash
aws iam list-roles --query "Roles[?starts_with(RoleName, '<infraID>')].[RoleName,CreateDate]" --output text
```
Report any IAM roles whose name starts with the infraID. These are orphaned OIDC-type roles (cloud-controller, ebs-csi, ingress, etc.) that belong to this infra set.

## Output

Present the report in this format:

```
## Triage: <infraID>

| # | Check | Result | Detail |
|---|-------|--------|--------|
| 1 | Protection tags | PASS | No do-not-delete or ci-cluster tag |
| 2 | Protected VPC name | PASS | Name is "abcdef1234-vpc" |
| 3 | Protected user | PASS | No username match |
| 4 | Expiration date | PASS | Expired 2026-07-03 (5 days ago) |
| 5 | CI pattern match | PASS | Hex infraID (e2e-generic) |
| 6 | OIDC S3 liveness | PASS | Not found in any bucket |
| 7 | EC2 instances | PASS | 0 instances |
| 8 | red-hat-managed | PASS | No managed instances |
| 9 | Sub-resources | INFO | 1 IGW, 1 subnet, 1 RTB |
| 10 | Route53 zones | INFO | 2 zones found |
| 11 | OIDC IAM provider | INFO | 1 orphaned provider found |
| 12 | IAM roles | INFO | 0 orphaned roles |

### Verdict: SAFE TO DELETE
This infra set has no protection tags, no running instances, no OIDC document,
and the infraID matches a CI hex pattern. All deletion checks pass.
```

## Verdict rules

These are non-negotiable:

- Any **FAIL** → verdict is **DO NOT DELETE** (explain which check failed and why)
- All checks **PASS** and CI pattern matches → **SAFE TO DELETE**
- All checks **PASS** but CI pattern is **UNKNOWN** → **UNCERTAIN** (could be a developer cluster)
- If CI pattern is **PASS** and age/expiration is **PASS** (older than 24h or expired), remaining UNKNOWN checks do not block → **SAFE TO DELETE** (a confirmed CI resource older than 24h with no OIDC, no instances, and no protection tags is leaked)
- If CI pattern is **UNKNOWN** and ANY check is **UNKNOWN** → **UNCERTAIN — REQUIRES HUMAN DECISION**
