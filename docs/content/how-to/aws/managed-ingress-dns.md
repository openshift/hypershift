# Managed Ingress DNS for AWS

By default, HyperShift creates only an internal `.hypershift.local` DNS zone for hosted control planes on AWS. When managed ingress DNS is enabled, the control plane operator (CPO) creates and manages Route53 hosted zones for ingress traffic in the customer's AWS account.

## What Gets Created

When `IngressDNSManagement` is set to `Managed`, the CPO creates:

1. **Public Route53 hosted zone** for the cluster's ingress subdomain (e.g. `my-cluster.example.com`)
2. **Private Route53 hosted zone** associated with the cluster's VPC for internal resolution
3. **DNSEndpoint CR** with NS records for delegation from the service provider's DNS zone to the customer's public zone
4. **ACME challenge CNAME** (`_acme-challenge.apps.<ingress>` pointing to `_acme-challenge.<baseDomain>`) for cert-manager DNS01 validation

## Prerequisites

The IAM role used for Route53 access (via `SharedVPC.RolesRef.IngressARN` or the default credentials) must have the following permissions:

- `route53:CreateHostedZone`
- `route53:DeleteHostedZone`
- `route53:GetHostedZone`
- `route53:ListHostedZones`
- `route53:ListResourceRecordSets`
- `route53:ChangeResourceRecordSets`

## Configuration

Set `ingressDNSManagement` to `Managed` on the HostedCluster's AWS platform spec:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  platform:
    type: AWS
    aws:
      region: us-east-1
      ingressDNSManagement: Managed
      # ... other AWS fields
  dns:
    baseDomain: example.com
```

This field is **immutable** after creation. The default value is `Unmanaged`, which preserves the existing behavior.

## Monitoring

The `AWSIngressDNSAvailable` condition on the `AWSEndpointService` and `HostedCluster` resources reflects the status of ingress DNS setup:

```bash
# Check AWSEndpointService condition
oc get awsendpointservice -n clusters-my-cluster -o jsonpath='{.items[*].status.conditions}'

# Check HostedCluster condition
oc get hostedcluster my-cluster -n clusters -o jsonpath='{.status.conditions[?(@.type=="AWSIngressDNSAvailable")]}'
```

## Cleanup

When the hosted cluster is deleted, the CPO automatically:

1. Deletes all custom DNS records from the managed zones
2. Deletes both the public and private hosted zones
3. Removes the DNSEndpoint CR

Zone IDs are persisted in the `AWSEndpointService` status (`ingressPublicZoneID` and `ingressPrivateZoneID`) so cleanup is reliable even across controller restarts.
