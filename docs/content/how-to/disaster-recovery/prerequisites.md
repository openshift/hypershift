# Disaster Recovery Prerequisites

This page consolidates the prerequisites that must be met before performing any backup/restore operation on a HostedCluster. All disaster recovery guides in this section reference these prerequisites.

## General Prerequisites

Ensure the following requirements are met on the Management cluster (connected or disconnected):

- A valid StorageClass configured in the Management cluster.
- Cluster-admin access to the Management cluster.
- Access to online storage compatible with OpenShift ADP cloud storage providers (e.g., S3, Azure, GCP, MinIO).
- HostedControlPlane pods are accessible and functioning correctly.
- Access to the `openshift-adp` subscription through a CatalogSource (version depends on the DR procedure you follow).

!!! important

    Before proceeding with any backup/restore procedure, keep in mind:

    1. Restoration will occur in a green field environment. After the HostedCluster has been backed up, it must be destroyed to initiate the restoration process.
    2. Node reprovisioning will take place. Back up workloads in the Data Plane before deleting the HostedCluster.

## HostedCluster Service Publishing Strategy Requirements

!!! warning "Critical Requirement for Backup/Restore to a Different Management Cluster"

    When restoring a HostedCluster to a **different** Management cluster, all services in the HostedCluster **must** be configured with a fixed hostname in their `servicePublishingStrategy`. This applies to **all platforms** (AWS, Agent, KubeVirt, OpenStack, etc.).

    The most critical service is the **APIServer**, which **must** have a fixed hostname. Without it, the restore will fail and nodes will be unable to rejoin the cluster.

### Why Is This Required?

When a HostedCluster is restored on a different Management cluster:

- The infrastructure endpoints (e.g., Load Balancer addresses, Route URLs) change because they are ephemeral and tied to the original Management cluster.
- Nodes store the KAS (Kube API Server) address in their kubelet configuration. If that address was an ephemeral Load Balancer or Route URL, nodes will point to the old address after restore.
- TLS certificates (SAN - Subject Alternative Name) will not match the new ephemeral endpoints, causing certificate validation failures.
- A fixed hostname configured via DNS allows you to update the DNS record to point to the new Management cluster's endpoint, making the migration transparent for existing nodes.

### Minimum Required Configuration

At a minimum, the **APIServer** service must have a fixed hostname:

```yaml
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
      loadBalancer:
        hostname: api-int.example.com
```

### Recommended Production Configuration

For production environments, it is strongly recommended to configure **all** services with fixed hostnames:

```yaml
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
      loadBalancer:
        hostname: api-int.example.com
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
      route:
        hostname: oauth.example.com
  - service: OIDC
    servicePublishingStrategy:
      type: Route
      route:
        hostname: oidc.example.com
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
      route:
        hostname: konnectivity.example.com
  - service: Ignition
    servicePublishingStrategy:
      type: Route
      route:
        hostname: ignition.example.com
```

This ensures full service continuity and DNS consistency during the restore process on a different Management cluster.

### AWS Self-Managed Platform Specifics

When using AWS platform with self-managed infrastructure, the APIServer can also use a **Route** service publishing strategy with a fixed hostname:

```yaml
spec:
  platform:
    aws:
      endpointAccess: Public
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: Route
      route:
        hostname: api.example.com
```

## Platform-Specific Prerequisites

### Bare Metal / Agent Provider

!!! note "InfraEnv Lifecycle"

    Since the InfraEnv has a different lifecycle than the HostedCluster, it should reside in a namespace separate from that of the HostedControlPlane and must not be deleted during backup or restore procedures.

### AWS Provider

- Ensure OIDC provider configuration is accessible for post-restore fixup (see [Fixing OIDC After Restore](#fixing-oidc-after-restore)).
- If using S3 for backup storage, ensure IAM roles and policies are configured following the [official documentation](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-aws.html#migration-configuring-aws-s3_installing-oadp-aws).

## Fixing OIDC After Restore

After completing an OADP restore on AWS, if the control-plane-operator reports `WebIdentityErr` errors or NodePool nodes remain not-ready due to a missing default security group, run the OIDC disaster recovery command:

```bash
hypershift fix dr-oidc-iam \
  --hc-name <cluster-name> \
  --hc-namespace <namespace> \
  --aws-creds ~/.aws/credentials
```

This re-uploads the OIDC discovery documents using the existing cluster signing key and recreates the IAM OIDC provider if needed. See the [AWS Disaster Recovery](../aws/disaster-recovery.md#fixing-oidc-identity-provider-after-oadp-restore) documentation for full details.
