---
title: Managed DNS
---

# Managed DNS

By default, the Route53 hosted zones that an AWS HostedCluster uses must exist
before the cluster is created. `hypershift create infra aws` (or the platform
consuming HyperShift) creates them:

* the public and private ingress zones, which hold the guest cluster's
  `*.apps` records;
* the `{cluster-name}.hypershift.local` private zone, which PrivateLink clusters
  use for the records that let workers reach the control plane through the
  PrivateLink endpoint.

With managed DNS, the Control Plane Operator (CPO) creates and reconciles these
zones itself, in the AWS account the cluster runs in. It can also delegate the
public ingress zone from its parent zone, and create the ACME DNS-01 challenge
record that certificate tooling needs.

!!! important "Tech Preview"

    Managed DNS is gated by the `AWSManagedDNS` feature gate, which is
    part of the `TechPreviewNoUpgrade` feature set. The HyperShift Operator (HO)
    must be installed with `--tech-preview-no-upgrade` for the
    `spec.platform.aws.managedDNS` field to be available. See
    [Feature Gates](../feature-gates.md).

Managed DNS is opt-in per HostedCluster and can only be enabled at creation
time, so installing an HO with this feature does not change existing clusters.

## Zones and domains

Which zones the CPO manages depends on the VPC setup:

| Zone | Standard VPC | [Shared VPC](shared-vpc.md) |
|------|--------------|------------|
| Public ingress zone (`{ingressDomainPrefix}.{clusterBaseDomain}`) | Created and owned by the CPO | Created and owned by the CPO |
| Private ingress zone (same name, associated with the cluster VPC) | Created and owned by the CPO | Not managed. The VPC owner's private zone is used. |
| `{cluster-name}.hypershift.local` private zone (PrivateLink clusters only, i.e. `endpointAccess` is `PublicAndPrivate` or `Private`) | Created and owned by the CPO | Not managed. The VPC owner's local zone is used. |

Managed zones are tagged with `spec.platform.aws.resourceTags`.

The ingress zones are named `{ingressDomainPrefix}.{clusterBaseDomain}`, where
`{clusterBaseDomain}` is `{baseDomainPrefix}.{baseDomain}` from `spec.dns`. When
`baseDomainPrefix` is not set, the HostedCluster name is used as the prefix; when
it is set to an empty string, `{clusterBaseDomain}` is just `{baseDomain}`.

The guest cluster's apps domain moves under the ingress zones, to
`apps.{ingressDomainPrefix}.{clusterBaseDomain}`. Routes in the guest cluster,
including the console and OAuth routes, use this domain.

For example, a HostedCluster named `example` with `baseDomain: example.com` and
`ingressDomainPrefix: in` gets:

* ingress zones `in.example.example.com`
* apps domain `apps.in.example.example.com`
* local zone `example.hypershift.local`, if it uses PrivateLink

## How it works

Two CPO controllers share the work: the HostedControlPlane (HCP) controller
manages the ingress zones, and the AWS PrivateLink controller manages the
`.hypershift.local` zone. Errors in either are retried, and do not block the
rest of the HCP reconcile.

### Ingress zones

The CPO checks the ingress zones periodically during HCP reconciliation, and
less often once `AWSManagedDNSAvailable` is `True`. On each check, it:

1. Verifies the public ingress zone recorded in status still exists, and creates
   it if not.
2. For standard VPC clusters, does the same for the private ingress zone,
   associating it with the cluster VPC in the cluster region.
3. Records the zones it manages in `status.platform.aws.dnsZones` on the HCP:
   both ingress zones for standard VPC clusters, and only the public ingress
   zone for shared VPC clusters. The HO copies this to the HostedCluster status.
4. If `delegation` is set, creates the ACME DNS-01 challenge CNAME and, in
   `ExternalDNS` mode, the `DNSEndpoint` for NS delegation. See
   [Delegation](#delegation).
5. Sets the `AWSManagedDNSAvailable` condition. See [Checking status](#checking-status).

### Local zone

For PrivateLink clusters on a standard VPC, each time the PrivateLink controller
reconciles an `AWSEndpointService`, it verifies the
`{cluster-name}.hypershift.local` zone exists, and creates it associated with the
cluster VPC if not. It records the zone ID in the `AWSEndpointService`
`status.dnsZoneID`, then writes the PrivateLink endpoint records into the zone.

Errors are reported in the `AWSEndpointAvailable` condition, which the HO
copies to the HostedCluster.

### Guest cluster DNS configuration

The Hosted Cluster Config Operator (HCCO) sets the guest cluster's
`dns.config` to the managed zones, so the ingress operator creates the `*.apps`
wildcard records in them:

```yaml
apiVersion: config.openshift.io/v1
kind: DNS
metadata:
  name: cluster
spec:
  baseDomain: example.example.com
  publicZone:
    id: Z0123456789ABCDEFGHIJ # PublicIngress zone ID
  privateZone:
    id: Z9876543210JIHGFEDCBA # PrivateIngress zone ID; for shared VPC, spec.dns.privateZoneID
```

### Existing zones

When creating a zone, the CPO first looks for an existing zone with the same
name:

* **Private zones** (ingress and `.hypershift.local`): an existing zone is
  adopted only if it is associated with the cluster VPC in the cluster region. A
  same-name private zone on another VPC is never adopted.
* **Public ingress zone:** any existing public zone with the same name in the
  account is adopted.

!!! warning

    An adopted public zone is treated as owned by the cluster, and is deleted
    with all of its records when the cluster is deleted. Make sure the ingress
    zone name is not already in use in the account.

### Out-of-band changes and deletion

If a managed zone is deleted outside of HyperShift, the CPO creates a new one on
its next check, records the new zone ID in status, and the HCCO points
`dns.config` at the new zone.

When the HostedCluster is deleted, the CPO deletes the `DNSEndpoint` and each
managed zone, including all records in it. If the CPO's AWS credentials are no
longer valid at that point, it logs the error and leaves the zones in place.

## Prerequisites

* The HO is installed with `--tech-preview-no-upgrade`.
* The IAM roles for the cluster grant the Route53 permissions that managed DNS
  needs. Because the managed zone IDs are not known when the roles are created,
  these permissions apply to all hosted zones in the account. Pass `--managed-dns`
  to `hypershift create cluster aws`, or to `hypershift create iam aws` if you
  [create IAM separately](create-infra-iam-separately.md), to add them:

    **CPO role**, in addition to its EC2 permissions. Attach this as an
    additional policy, or add its statements to the role's existing policy:

    ```json
    {
      "Version": "2012-10-17",
      "Statement": [
        {
          "Effect": "Allow",
          "Action": [
            "route53:ListHostedZones",
            "route53:GetHostedZone",
            "route53:CreateHostedZone",
            "route53:DeleteHostedZone",
            "route53:ChangeTagsForResource"
          ],
          "Resource": "*"
        },
        {
          "Effect": "Allow",
          "Action": [
            "route53:ChangeResourceRecordSets",
            "route53:ListResourceRecordSets"
          ],
          "Resource": "arn:aws:route53:::hostedzone/*"
        }
      ]
    }
    ```

    **Ingress operator role:** `route53:ChangeResourceRecordSets` on
    `arn:aws:route53:::hostedzone/*`, instead of only the public and private
    zones that exist at infrastructure creation time.

    If you manage IAM roles outside the HyperShift CLI, add these permissions
    yourself. Without them, zone creation fails with `AccessDenied`.

* For `ExternalDNS` delegation, external-dns is deployed with the AWS provider
  and can write to the parent zone. See [ExternalDNS mode](#externaldns-mode).

## Enabling managed DNS

`managedDNS` can only be set when the HostedCluster is created. It cannot be
added or removed later, and `ingressDomainPrefix` cannot be changed.
`delegation` can be changed after creation.

!!! note

    If the HO was installed without `--tech-preview-no-upgrade`, the HostedCluster
    CRD does not include `managedDNS`. `kubectl` rejects the field, but clients
    that do not request strict field validation, including the HyperShift CLI,
    have it pruned, and the cluster is created without managed DNS. Check that
    `spec.platform.aws.managedDNS` is set after creating the cluster.

### Using the CLI

Pass `--managed-dns` to `hypershift create cluster aws`. The CLI sets
`ingressDomainPrefix` to `in` and creates the IAM roles with the permissions
listed above:

```bash
hypershift create cluster aws \
    --name example \
    --namespace clusters \
    --base-domain example.com \
    --region us-east-1 \
    --aws-creds ${AWS_CREDS} \
    --pull-secret ${PULL_SECRET} \
    --release-image ${RELEASE_IMAGE} \
    --node-pool-replicas 2 \
    --managed-dns
```

The CLI does not configure delegation. To enable it or use a different prefix,
add `--render`, edit `spec.platform.aws.managedDNS` in the output, and apply it.

### Using the API

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
spec:
  dns:
    baseDomain: example.com
  platform:
    type: AWS
    aws:
      region: us-east-1
      managedDNS:
        # Required. 1-63 lowercase alphanumeric characters or '-', starting and
        # ending with an alphanumeric character. Immutable.
        ingressDomainPrefix: in
        # Optional. Omit to only create zones; the consuming platform then
        # handles NS delegation and certificates.
        delegation:
          nsDelegationMode: ExternalDNS # or Manual
  # ...
```

## Delegation

The public ingress zone only resolves publicly once its parent zone, the zone
for `{clusterBaseDomain}`, holds NS records pointing to it.

When `delegation` is omitted, the CPO only creates the zones. NS delegation and
certificate management are left to the consuming platform, and
`AWSManagedDNSAvailable` becomes `True` as soon as the zones exist.

When `delegation` is set, the CPO also creates an ACME DNS-01 challenge CNAME in
the public ingress zone. This lets certificate tooling answer challenges for the
apps domain from the parent zone:

```text
_acme-challenge.apps.in.example.example.com.  CNAME  _acme-challenge.example.example.com.
```

How the NS records get into the parent zone depends on `nsDelegationMode`.
In both modes, the CPO then waits until the ingress zone's NS records resolve
before reporting success.

### ExternalDNS mode

The CPO creates a `DNSEndpoint` in the HCP namespace (`{namespace}-{name}` of
the HostedCluster, for example `clusters-example`), owned by the
HostedControlPlane:

```yaml
apiVersion: externaldns.k8s.io/v1alpha1
kind: DNSEndpoint
metadata:
  name: example-ingress-delegation
  namespace: clusters-example
spec:
  endpoints:
  - dnsName: in.example.example.com
    recordType: NS
    recordTTL: 300
    targets: # name servers of the public ingress zone
    - ns-1.awsdns-01.org
    - ns-2.awsdns-02.co.uk
    - ns-3.awsdns-03.com
    - ns-4.awsdns-04.net
```

external-dns writes this record to the parent zone. This requires external-dns
deployed by `hypershift install` with `--external-dns-provider=aws`, with
credentials and a `--external-dns-domain-filter` that cover the parent zone. With
the AWS provider, `hypershift install` also installs the `DNSEndpoint` CRD and
starts external-dns with `--source=crd` and NS added to its managed record types.
See [External DNS](external-dns.md) for setting up external-dns.

### Manual mode

The CPO publishes the public zone's name servers in
`status.platform.aws.dnsZones[].nameServers`, and the consuming platform creates
the NS records in the parent zone:

```bash
kubectl get hostedcluster example -n clusters \
    -o jsonpath='{.status.platform.aws.dnsZones[?(@.zoneType=="PublicIngress")].nameServers}'
```

## Checking status

The CPO reports progress through the `AWSManagedDNSAvailable` condition, which
is set on the HCP and mirrored to the HostedCluster:

```bash
kubectl get hostedcluster example -n clusters \
    -o jsonpath='{.status.conditions[?(@.type=="AWSManagedDNSAvailable")]}'
```

| Status | Reason | Meaning |
|--------|--------|---------|
| `True` | `ManagedDNSSuccess` | The zones exist. When `delegation` is set, the ingress zone's NS records also resolve. |
| `False` | `NSDelegationPending` | The zones exist but the ingress zone's NS records do not resolve yet. If delegation stays unresolved, the message changes to ask you to check the NS records in the parent zone. In `ExternalDNS` mode, the message includes any error from reconciling the `DNSEndpoint`. |
| `False` | `ManagedDNSError` | Creating or verifying a zone, or creating the ACME CNAME, failed. The message contains the AWS error. |

The CPO checks NS resolution with a lookup from its own pod, so it uses the
management cluster's DNS resolvers.

The managed ingress zones are listed in the HostedCluster status:

```bash
kubectl get hostedcluster example -n clusters -o jsonpath='{.status.platform.aws.dnsZones}'
```

```json
[
  {
    "zoneType": "PublicIngress",
    "zoneID": "Z0123456789ABCDEFGHIJ",
    "name": "in.example.example.com",
    "nameServers": ["ns-1.awsdns-01.org", "ns-2.awsdns-02.co.uk", "ns-3.awsdns-03.com", "ns-4.awsdns-04.net"]
  },
  {
    "zoneType": "PrivateIngress",
    "zoneID": "Z9876543210JIHGFEDCBA",
    "name": "in.example.example.com"
  }
]
```

Shared VPC clusters only list the `PublicIngress` zone. The `.hypershift.local`
zone is recorded in the `AWSEndpointService` status (`status.dnsZoneID`), not in
`dnsZones`.

## Troubleshooting

* **`ManagedDNSError`, or `AWSEndpointAvailable` is `False`, with an
  `AccessDenied` message:** the CPO role lacks the managed DNS permissions.
  Recreate the IAM roles with `--managed-dns`, or add the permissions listed in
  [Prerequisites](#prerequisites).
* **Workers cannot reach the control plane on a PrivateLink cluster:** check the
  `AWSEndpointAvailable` condition for errors creating the
  `{cluster-name}.hypershift.local` zone, and check that the zone in the
  `AWSEndpointService` `status.dnsZoneID` exists and is associated with the
  cluster VPC.
* **Stuck in `NSDelegationPending`:**
    * Check that the parent zone has NS records for the ingress zone, for example
      with `dig NS in.example.example.com`.
    * In `ExternalDNS` mode, check that the `{name}-ingress-delegation`
      `DNSEndpoint` exists in the HCP namespace, and look for errors in the
      external-dns logs in the `hypershift` namespace.
    * If the records resolve from your workstation, check that they also
      resolve from the management cluster, which is where the CPO looks them up.
* **`*.apps` records missing:** check that the guest `dns.config` public and
  private zone IDs match `status.platform.aws.dnsZones`, and look at the
  ingress operator's `DNSRecord` status in the guest cluster:
  `kubectl get dnsrecords -n openshift-ingress-operator -o yaml`.
* **Zones left behind after deletion:** if the CPO's AWS credentials were no
  longer valid when the cluster was deleted, the zones are left in place.
  Delete them in Route53.
