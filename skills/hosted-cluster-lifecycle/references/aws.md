# AWS overlay

Load for AWS setup or a provider/identity/network gate. Keep the account,
region, role/credential source and infrastructure ID tied to the selected HC.
Read cloud identity metadata without displaying credential material:

```bash
set -euo pipefail
umask 077
aws sts get-caller-identity --output json \
  > "$RESTRICTED_DIR/aws-caller-identity.raw.json" \
  2> "$RESTRICTED_DIR/aws-caller-identity.raw.stderr"
mgmt_oc -n "$HCP_NAMESPACE" \
  get awsclusters.infrastructure.cluster.x-k8s.io,awsmachines.infrastructure.cluster.x-k8s.io \
  -o json > "$RESTRICTED_DIR/aws-provider-resources.raw.json" \
  2> "$RESTRICTED_DIR/aws-provider-resources.raw.stderr"
jq '[.items[] | {kind, phase: .status.phase,
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]}]' \
  "$RESTRICTED_DIR/aws-provider-resources.raw.json" \
  2> "$RESTRICTED_DIR/aws-provider-projection.raw.stderr"
```

## Setup and identity

Use the restricted local output directory established by intake/evidence for
provider query results and logs. Do not print unredacted provider output.

Confirm the workflow's VPC/subnets, region/zones, quotas, OIDC bucket/prefix,
issuer and IAM trust/roles. Existing infrastructure/IAM inputs change what
the create command allocates. Before HC exists, inspect create logs captured in
restricted local files for NAT, network or IAM failures; preserve the allocated
infrastructure ID in access-controlled metadata before a retry.

Distinguish credential consumers. CLI `create cluster aws --sts-creds` accepts
STS JSON; HO `install --oidc-storage-provider-s3-credentials` supplies a shared
AWS INI profile to its SDK. Temporary INI credentials require a session token
in addition to the access/secret keys. Verify the format and consumer without
printing the file. SDK fallback to EC2 IMDS may indicate unreadable/misformatted
credentials; do not diagnose expiry from that message alone.

A refreshed Secret is not proof that the running operator uses the new
credentials. HO loads the OIDC S3 credentials during startup and builds its S3
client from that configuration; the client can retain its credential provider
after the mounted Secret changes. Repeated `PutObject` `InvalidToken` errors
with verified updated Secret bytes therefore point first to the existing SDK
client. Compare the error time with the HO process start time and inspect the
Secret's resource version and mounted-file metadata without printing its data.
With authorization, restart the HO deployment to rebuild that client, then
verify a fresh OIDC upload, a current HC `ValidOIDCConfiguration` condition,
and HTTP 200 from the OIDC discovery and JWKS endpoints. Do not rotate the
credentials again or call the Secret update a successful renewal before the
upload succeeds.

Read current conditions and logs to distinguish OIDC document publication,
AWS identity-provider validation and default worker SG creation. These are
related but separate gates. A late OIDC publication error can block startup
after KAS has started.

## No Machines despite a generated payload

Check HCP `ValidAWSIdentityProvider` and `AWSDefaultSecurityGroupCreated`, and
HC `status.platform.aws.defaultWorkerSecurityGroupID`. NodePool can report
`ValidGeneratedPayload=True` alongside `AWSSecurityGroupAvailable=False`
(`DefaultSGNotReady`). This is upstream of instance boot or ignition delivery.
Some release versions defer identity validation until KAS and then use a
five-minute retry interval; check the deployed source and the observations
reference before applying that explanation. `Unknown` alone is not bad STS.
The published [AWS startup debugging examples](https://hypershift.pages.dev/reference/cluster-startup-aws-observation/)
trace this gate for the 5.0.0-rc.7 development run; treat that timing as a
version-scoped example, not a universal timer.

## Endpoints and workers

Inspect Service/LB/DNS and the configured endpoint strategy. Conditions such
as `AWSEndpointServiceAvailable`, `AWSEndpointAvailable`, `ExternalDNSReachable`
and `PublicEndpointExposed` apply only where that workflow uses them. For a
private cluster, distinguish worker-VPC resources from control-plane load
balancers/endpoints in the management VPC.

For a Machine/provider ID, use exact instance IDs and the selected region:

```bash
umask 077
aws ec2 describe-instances --region "$AWS_REGION" --instance-ids "$INSTANCE_ID" \
  --query 'Reservations[].Instances[].{ID:InstanceId,State:State.Name,Subnet:SubnetId}' \
  --output json > "$RESTRICTED_DIR/ec2-instance.raw.json" \
  2> "$RESTRICTED_DIR/ec2-instance.raw.stderr"
```

When boot evidence is needed, capture console output for the exact instance
and region to restricted local files rather than stdout:

```bash
umask 077
aws ec2 get-console-output --region "$AWS_REGION" \
  --instance-id "$INSTANCE_ID" --latest --query 'Output' --output text \
  > "$RESTRICTED_DIR/ec2-console.raw.txt" \
  2> "$RESTRICTED_DIR/ec2-console.raw.stderr"
```

Redact it before printing or retaining it in ordinary evidence or sharing. First-boot
MachineConfig work/reboot can follow a successful ignition fetch. Scope
network and quota checks to the implicated VPC/zone and error, rather than
changing account-wide roles or firewall rules as a diagnostic experiment.
