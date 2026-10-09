# GCP Platform Implementation for HyperShift

This document describes the implementation of Google Cloud Platform (GCP) support in HyperShift with Cluster API Provider GCP (CAPG) integration.

## Overview

The GCP platform implementation enables HyperShift to create and manage OpenShift hosted clusters on Google Cloud Platform using CAPG (Cluster API Provider GCP) for NodePool support. This implementation follows established patterns from AWS and Azure platforms.

## Architecture

### Key Components

1. **Platform Interface Implementation** (`/hypershift-operator/controllers/hostedcluster/internal/platform/gcp/gcp.go`)
   - Implements the HyperShift Platform interface for GCP
   - Provides CAPG controller deployment specification
   - Manages credential validation and condition management
   - Handles CAPI infrastructure resource reconciliation

2. **API Types** (`/api/hypershift/v1beta1/gcp.go`)
   - `GCPPlatformSpec`: Main configuration for GCP clusters
   - `GCPNetworkConfig`: VPC and Private Service Connect configuration
   - `GCPWorkloadIdentityConfig`: Workload Identity Federation preparation
   - `GCPResourceReference`: GCP resource naming standards

3. **Condition Management** (`/api/hypershift/v1beta1/hostedcluster_conditions.go`)
   - `ValidGCPCredentials`: GCP credential validation status
   - `ValidGCPWorkloadIdentity`: WIF configuration validation status

## Features

### Credential Management

The GCP platform uses **Workload Identity Federation (WIF)** for secure, short-lived token-based authentication. This eliminates the need for long-term service account keys and provides better security.

#### Workload Identity Federation (Required)
```yaml
spec:
  platform:
    gcp:
      workloadIdentity:
        projectNumber: "123456789012"
        poolID: "hypershift-pool"
        providerID: "hypershift-provider"
        serviceAccountsRef:
          nodePoolEmail: "capg-controller@my-gcp-project.iam.gserviceaccount.com"
```

**Note**: Workload Identity Federation is mandatory for GCP clusters. The token minter sidecar is automatically deployed to handle token exchange and service account impersonation for CAPG controllers. The specified Google Service Account (GSA) will be impersonated to access GCP APIs for node pool management.

**Getting the Project Number**: The `projectNumber` is the numeric identifier for your GCP project (different from the project ID). You can get it with:
```bash
gcloud projects describe PROJECT_ID --format="value(projectNumber)"
```

### Network Configuration

Supports VPC and Private Service Connect configuration:

```yaml
spec:
  platform:
    gcp:
      networkConfig:
        network:
          name: "my-vpc-network"
        privateServiceConnectSubnet:
          name: "my-psc-subnet"
```

### Resource Labeling

Automatic resource labeling for organization and billing. Labels can be updated after cluster creation and will be propagated to GCP resources where supported:

```yaml
spec:
  platform:
    gcp:
      resourceLabels:
        - key: environment
          value: production
        - key: team
          value: platform
        - key: cost-center
          value: engineering
```

### Resource Manager Tags

Resource Manager tags are distinct from resource labels. Define the TagKeys and
TagValues in the HostedCluster's GCP project or an organization before
installation, then configure their short names. If `parentID` is omitted, the
HostedCluster project is used; for an organization-defined tag, set `parentID`
to its numeric organization ID:

```yaml
spec:
  platform:
    gcp:
      resourceTags:
        - key: environment
          value: production
        - parentID: "123456789012"
          key: cost-center
          value: shared
```

An explicit `parentID` must be the HostedCluster project ID or a numeric
organization ID. A different project ID is not accepted. HyperShift does not
check whether the referenced tag exists or whether the guest identities have
permission to use it at admission time.

Keys and values must each be 1–63 characters long and begin and end with an
ASCII letter or digit. Keys may also contain `.`, `_`, and `-`; values accept
additional punctuation and spaces. When omitted, no tags are applied through
this setting. If present, the list must contain 1–50 entries. **Each short key
must be unique across the entire list, even when the tags have different
parents.** Thus an organization and project tag with the same short key cannot
both be configured with the current guest Infrastructure API. The HostedCluster
API enforces the accepted characters.

The configured tags currently apply to these resources:

| Tagged resource | When tagging is attempted |
| --- | --- |
| GCP PD CSI persistent disks for guest PersistentVolumes | When a new disk is provisioned |
| Image registry bucket | During bucket setup, when the image registry is enabled |

They are **not** applied by this setting to worker VMs or their boot disks,
Private Service Connect forwarding rules or IP addresses, firewall rules, DNS
resources, or other GCP resources created by HyperShift or CAPG. Broader
management-side resource tagging is separate work. HyperShift does not create
TagKeys or TagValues.

HyperShift does not add a separate system Resource Manager tag to the two
supported resource types, so it does not reserve a slot for one in the
`resourceTags` list. Google Cloud allows at most 50 tags attached to each
resource; tags attached by other actors also use that limit. Accepting 50
entries in the HostedCluster API therefore does not guarantee that all 50 can
be applied to a target resource. See [Google Cloud tag
limits](https://cloud.google.com/resource-manager/docs/limits#tag_limits).

HyperShift passes these tags to the guest cluster for the two components above.
This requires an OpenShift 5.3 or later control plane payload. Earlier payloads
do not propagate `resourceTags` to the guest cluster.

Configure tags when creating the HostedCluster: `resourceTags` cannot be added,
removed, or changed afterward. This installation-time restriction is temporary
until tag update reconciliation is implemented; unlike `resourceTags`,
`resourceLabels` can be changed after creation. The guest components that
attach tags (the storage and image registry service-account identities) require
Google Cloud Tag User on the tag value and target resource, plus the applicable
resource-specific TagBinding permissions. HyperShift does not currently report
a dedicated condition for missing or inaccessible TagKeys or TagValues, or for
missing tag permissions. The `ValidGCPCredentials` and `ValidGCPWorkloadIdentity` conditions
do not validate the guest storage and image-registry identities' tag access.
Such failures do not block HyperShift's HostedCluster reconciliation, but they
can prevent the guest components from creating a tagged registry bucket or
persistent disk. Check the guest image-registry operator's status and logs, or
the GCP PD CSI controller logs and affected PVC events, for runtime errors.
See [Create GCP IAM Resources](gcp/create-gcp-iam.md#resource-tag-permissions)
for the required grants and how to apply them to existing clusters.

## CAPG Integration

### Controller Deployment

The GCP platform deploys CAPG controllers with:

- **Manager Container**: Main CAPG controller with proper image handling
- **Token Minter Sidecar**: Prepared for Workload Identity Federation
- **Comprehensive RBAC**: Full permissions for CAPI resource management
- **Feature Gates**: Version-conditional settings for compatibility

### RBAC Policies

Comprehensive policy rules covering:

- **CAPG Resources**: `gcpclusters`, `gcpmachines`, `gcpmachinetemplates`
- **CAPI Resources**: `clusters`, `machines`, `machinesets`
- **Kubernetes Resources**: `nodes`, `pods`, `secrets`, `configmaps`
- **HyperShift Resources**: `nodepools`, `hostedclusters`
- **Bootstrap Resources**: `kubeadmconfigs`, `kubeadmconfigtemplates`
- **Certificate Management**: CSRs and approval handling
- **Coordination**: Leader election and distributed coordination

### Infrastructure Reconciliation

Creates and manages `GCPCluster` CAPI resources with:

- **External Management**: Marked with `cluster.x-k8s.io/managed-by: external`
- **Project/Region Configuration**: From HostedCluster specification
- **Network Settings**: VPC and subnet configuration for Private Service Connect
- **Resource Labels**: Applied to all created GCP resources
- **Failure Domains**: Multi-zone deployment support
- **Control Plane Endpoint**: API server endpoint configuration

## Configuration Examples

### Complete GCP Cluster Example
```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example-gcp
  namespace: clusters
spec:
  platform:
    type: GCP
    gcp:
      project: "my-gcp-project"
      region: "us-central1"
      networkConfig:
        network:
          name: "hypershift-vpc"
        privateServiceConnectSubnet:
          name: "hypershift-psc"
      workloadIdentity:
        projectNumber: "123456789012"
        poolID: "hypershift-pool"
        providerID: "hypershift-provider"
        serviceAccountsRef:
          nodePoolEmail: "capg-controller@my-gcp-project.iam.gserviceaccount.com"
      resourceLabels:
        - key: environment
          value: production
        - key: team
          value: platform
```

## Validation and Defaulting

### API Validation
- **Project ID**: RFC-compliant format with length and character restrictions
- **Region**: Valid GCP region format
- **Resource Labels**: GCP label requirements (63 char max, valid characters)
- **Cross-Field Validation**: Either credentials or WIF required
- **Network Naming**: GCP resource naming standards

### Default Conditions
- **ValidGCPCredentials**: Set to `True` by default
- **ValidGCPWorkloadIdentity**: Set when WIF is configured
- **Platform-Agnostic**: Standard HyperShift conditions apply

## Implementation Status

### ✅ Completed (Phase 1)
- [x] Platform interface implementation
- [x] CAPG controller deployment specification
- [x] API types with comprehensive validation
- [x] Credential validation and condition management
- [x] CAPI infrastructure resource reconciliation
- [x] Comprehensive RBAC policy rules
- [x] Default condition management
- [x] Integration testing framework
- [x] Code generation and documentation

### 🚧 Future Work (Not in Current Scope)
- [ ] NodePool implementation and CAPG machine management
- [ ] Workload Identity Federation token exchange implementation
- [ ] GCP KMS secret encryption integration
- [ ] External infrastructure creation (VPC, IAM) via CLI
- [ ] Production deployment and testing

## Testing

Integration tests are provided in `/test/integration/gcp_test.go` covering:

1. **Platform Creation**: Basic constructor and instance validation
2. **Deployment Specification**: CAPG controller deployment configuration
3. **Credential Validation**: Condition management and validation logic
4. **Infrastructure Reconciliation**: CAPI resource creation and configuration
5. **Policy Rules**: RBAC rule completeness and correctness
6. **Default Conditions**: Platform-specific condition initialization

Run tests with:
```bash
go test ./test/integration/gcp_test.go -v
```

## Technical Details

### Image Handling Priority
1. Annotation override: `hypershift.openshift.io/capi-provider-gcp-image`
2. Environment variable: `IMAGE_GCP_CAPI_PROVIDER`
3. Payload discovery: From OpenShift release payload

### Naming Conventions
- **ServiceAccount**: `capi-gcp-controller-manager`
- **ClusterRole**: `capi-gcp-controller-manager-<namespace>`
- **Secret**: `capg-manager-bootstrap-credentials`
- **GCPCluster**: Same as HostedCluster name

### Error Handling
- Comprehensive credential validation with detailed error messages
- Condition-based status reporting following HyperShift patterns
- Graceful handling of optional configuration (WIF, resource labels)
- Proper error aggregation and propagation

## References

- [CAPG Documentation](https://cluster-api-gcp.sigs.k8s.io/)
- [GCP Resource Naming Standards](https://google.aip.dev/122)
- [HyperShift Platform Development Guide](../contribute/onboard-a-platform.md)
- [Workload Identity Federation](https://cloud.google.com/iam/docs/workload-identity-federation)
