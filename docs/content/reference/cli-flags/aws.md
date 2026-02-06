---
title: AWS CLI Flags
---

# AWS CLI Flags

This page documents CLI flags for AWS platform commands, comparing the supported `hcp` CLI with the internal `hypershift` developer CLI.

---

## Create Cluster

Flags for `hcp create cluster aws` and `hypershift create cluster aws`.

### Required

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--name` | string |  | ✓ | ✓ | A name for the cluster |
| `--pull-secret` | string |  | ✓ | ✓ | File path to a pull secret. |

---

### Cluster Identity

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--annotations` | stringArray |  | ✓ | ✓ | Annotations to apply to the hostedcluster (key=value). Can be specified multiple times. |
| `--base-domain` | string |  | ✓ | ✓ | The ingress base domain for the cluster |
| `--base-domain-prefix` | string |  | ✓ | ✓ | The ingress base domain prefix for the cluster, defaults to cluster name. Use 'none' for an empty prefix |
| `--infra-id` | string |  | ✓ | ✓ | Infrastructure ID to use for hosted cluster resources. |
| `--labels` | stringArray |  | ✓ | ✓ | Labels to apply to the hostedcluster (key=value). Can be specified multiple times. |
| `--namespace` | string | `clusters` | ✓ | ✓ | A namespace to contain the generated resources |

---

### Release Configuration

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--arch` | string | `amd64` | ✓ | ✓ | The default processor architecture for the NodePool (e.g. arm64, amd64) |
| `--disable-cluster-capabilities` | stringSlice |  | ✓ | ✓ | Optional cluster capabilities to disable. The only currently supported values are ImageRegistry,openshift-samples,Insights,baremetal,Console,NodeTuning,Ingress. |
| `--enable-cluster-capabilities` | stringSlice |  | ✓ | ✓ | Optional cluster capabilities to enable. The only currently supported values are ImageRegistry,openshift-samples,Insights,baremetal,Console,NodeTuning,Ingress. |
| `--feature-set` | string |  | ✓ | ✓ | The predefined feature set to use for the cluster (TechPreviewNoUpgrade or DevPreviewNoUpgrade) |
| `--release-image` | string |  | ✓ | ✓ | The OCP release image for the cluster |
| `--release-stream` | string |  | ✓ | ✓ | The OCP release stream for the cluster (e.g. 4-stable-multi), this flag is ignored if release-image is set |

---

### AWS Infrastructure

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--additional-tags` | stringSlice |  | ✓ | ✓ | Additional tags to set on AWS resources |
| `--private-zones-in-cluster-account` | bool | `false` | ✓ | ✓ | In shared VPC infrastructure, create private hosted zones in cluster account |
| `--public-only` | bool | `false` | ✓ | ✓ | If true, creates a cluster that does not have private subnets or NAT gateway and assigns public IPs to all instances. |
| `--region` | string | `us-east-1` | ✓ | ✓ | Region to use for AWS infrastructure. |
| `--vpc-cidr` | string |  | ✓ | ✓ | The CIDR to use for the cluster VPC (mask must be 16) |
| `--zones` | stringSlice |  | ✓ | ✓ | The availability zones in which NodePools will be created |

---

### Networking

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--allocate-node-cidrs` | bool | `false` | ✓ | ✓ | When networkType=Other, it's recommended to set this field to 'true' when using Flannel as the CNI. |
| `--cluster-cidr` | stringArray | `[10.132.0.0/14]` | ✓ | ✓ | The CIDR of the cluster network. Can be specified multiple times. |
| `--default-dual` | bool | `false` | ✓ | ✓ | Defines the Service and Cluster CIDRs as dual-stack default values. Cannot be defined with service-cidr or cluster-cidr flag. |
| `--disable-multi-network` | bool | `false` | ✓ | ✓ | Disables the Multus CNI plugin and related components in the hosted cluster |
| `--endpoint-access` | string | `Public` | ✓ | ✓ | Access for control plane endpoints (Public, PublicAndPrivate, Private) |
| `--external-dns-domain` | string |  | ✓ | ✓ | Sets hostname to opinionated values in the specified domain for services with publishing type LoadBalancer or Route. |
| `--kas-dns-name` | string |  | ✓ | ✓ | The custom DNS name for the kube-apiserver service. Make sure the DNS name is valid and addressable. |
| `--machine-cidr` | stringArray |  | ✓ | ✓ | The CIDR of the machine network. Can be specified multiple times. |
| `--network-type` | string | `OVNKubernetes` | ✓ | ✓ | Enum specifying the cluster SDN provider. Supports either Calico, OVNKubernetes, OpenShiftSDN or Other. |
| `--service-cidr` | stringArray | `[172.31.0.0/16]` | ✓ | ✓ | The CIDR of the service network. Can be specified multiple times. |

---

### Proxy Configuration

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--enable-proxy` | bool | `false` | ✓ | ✓ | If true, a proxy should be set up, rather than allowing direct internet access from the nodes |
| `--enable-secure-proxy` | bool | `false` | ✓ | ✓ | If true, a secure proxy should be set up, rather than allowing direct internet access from the nodes |
| `--proxy-vpc-endpoint-service-name` | string |  | ✓ | ✓ | The name of a VPC Endpoint Service offering a proxy service to use for the cluster |

---

### Node Pool Configuration

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--auto-node` | bool | `false` | ✓ | ✓ | If true, this flag indicates the Hosted Cluster will support AutoNode feature. |
| `--auto-repair` | bool | `false` | ✓ | ✓ | Enables machine autorepair with machine health checks |
| `--instance-type` | string |  | ✓ | ✓ | Instance type for AWS instances. |
| `--node-drain-timeout` | duration |  | ✓ | ✓ | The NodeDrainTimeout on any created NodePools |
| `--node-pool-replicas` | int32 | `0` | ✓ | ✓ | If 0 or greater, creates a nodepool with that many replicas; else if less than 0, does not create a nodepool. |
| `--node-upgrade-type` | UpgradeType |  | ✓ | ✓ | The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace |
| `--node-volume-detach-timeout` | duration |  | ✓ | ✓ | The NodeVolumeDetachTimeout on any created NodePools |
| `--root-volume-iops` | int64 | `0` | ✓ | ✓ | The iops of the root volume when specifying type:io1 for machines in the NodePool |
| `--root-volume-kms-key` | string |  | ✓ | ✓ | The KMS key ID or ARN to use for root volume encryption for machines in the NodePool |
| `--root-volume-size` | int64 | `120` | ✓ | ✓ | The size of the root volume (min: 8) for machines in the NodePool |
| `--root-volume-type` | string | `gp3` | ✓ | ✓ | The type of the root volume (e.g. gp3, io2) for machines in the NodePool |

---

### Security & Encryption

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--additional-trust-bundle` | string |  | ✓ | ✓ | Path to a file with user CA bundle |
| `--fips` | bool | `false` | ✓ | ✓ | Enables FIPS mode for nodes in the cluster |
| `--generate-ssh` | bool | `false` | ✓ | ✓ | If true, generate SSH keys |
| `--image-content-sources` | string |  | ✓ | ✓ | Path to a file with image content sources |
| `--kms-key-arn` | string |  | ✓ | ✓ | The ARN of the KMS key to use for Etcd encryption. If not supplied, etcd encryption will default to using a generated AESCBC key. |
| `--oidc-issuer-url` | string |  | ✓ | ✓ | The OIDC provider issuer URL |
| `--sa-token-issuer-private-key-path` | string |  | ✓ | ✓ | The file to the private key for the service account token issuer |
| `--ssh-key` | string |  | ✓ | ✓ | Path to an SSH key file |

---

### AWS Credentials & IAM

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--role-arn` | string |  | ✓ | ✓ | The ARN of the role to assume. |
| `--secret-creds` | string |  | ✓ | ✓ | A Kubernetes secret with needed AWS platform credentials: sts-creds, pull-secret, and a base-domain value. The secret must exist in the supplied "--namespace". If a value is provided through the flag '--pull-secret', that value will override the pull-secret value in 'secret-creds'. |
| `--shared-role` | bool | `false` | ✓ | ✓ | Create a single shared role with all role policies instead of individual component roles |
| `--sts-creds` | string |  | ✓ | ✓ | Path to the STS credentials file to use when assuming the role. Can be generated with 'aws sts get-session-token --output json' |
| `--use-rosa-managed-policies` | bool | `false` | ✓ | ✓ | Use ROSA managed policies for the operator roles and worker instance profile |

---

### Control Plane Configuration

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--control-plane-availability-policy` | string | `HighlyAvailable` | ✓ | ✓ | Availability policy for hosted cluster components. Supported options: SingleReplica, HighlyAvailable |
| `--etcd-storage-class` | string |  | ✓ | ✓ | The persistent volume storage class for etcd data volumes |
| `--etcd-storage-size` | string |  | ✓ | ✓ | The storage size for etcd data volume. Example: 8Gi |
| `--infra-availability-policy` | string |  | ✓ | ✓ | Availability policy for infrastructure services in guest cluster. Supported options: SingleReplica, HighlyAvailable |
| `--node-selector` | stringToString |  | ✓ | ✓ | A comma separated list of key=value to use as node selector for the Hosted Control Plane pods to stick to. E.g. role=cp,disk=fast |
| `--pods-labels` | stringToString |  | ✓ | ✓ | A comma separated list of key=value to use as labels for the Hosted Control Plane pods |
| `--toleration` | stringArray |  | ✓ | ✓ | A comma separated list of options for a toleration that will be applied to the hcp pods. Valid options are, key, value, operator, effect, tolerationSeconds. E.g. key=node-role.kubernetes.io/master,operator=Exists,effect=NoSchedule. Can be specified multiple times to add multiple tolerations |

---

### OLM Configuration

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--olm-catalog-placement` | OLMCatalogPlacement | `management` | ✓ | ✓ | The OLM Catalog Placement for the HostedCluster. Supported options: Management, Guest |
| `--olm-disable-default-sources` | bool | `false` | ✓ | ✓ | Disables the OLM default catalog sources for the HostedCluster. |

---

### Output & Execution

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--pausedUntil` | string |  | ✓ | ✓ | If a date is provided in RFC3339 format, HostedCluster creation is paused until that date. If the boolean true is provided, HostedCluster creation is paused until the field is removed. |
| `--render` | bool | `false` | ✓ | ✓ | Render output as YAML to stdout instead of applying. Note: secrets are not rendered by default, additionally use the --render-sensitive flag to render secrets |
| `--render-into` | string |  | ✓ | ✓ | Render output as YAML into this file instead of applying. If unset, YAML will be output to stdout. |
| `--render-sensitive` | bool | `false` | ✓ | ✓ | When used along --render it enables rendering of secrets in the output |
| `--timeout` | duration |  | ✓ | ✓ | If the --wait flag is set, set the optional timeout to limit the waiting duration. The format is duration; e.g. 30s or 1h30m45s; 0 means no timeout; default = 0 |
| `--version-check` | bool | `false` | ✓ | ✓ | Checks version of CLI and Hypershift operator and blocks create if mismatched |
| `--wait` | bool | `false` | ✓ | ✓ | If the create command should block until the cluster is up. Requires at least one node. |

---

### Developer-Only

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--aws-creds` | string |  |  | ✓ | Path to an AWS credentials file |
| `--control-plane-operator-image` | string |  |  | ✓ | Override the default image used to deploy the control plane operator |
| `--iam-json` | string |  |  | ✓ | Path to file containing IAM information for the cluster. If not specified, IAM will be created |
| `--infra-json` | string |  |  | ✓ | Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created |
| `--single-nat-gateway` | bool | `false` |  | ✓ | If enabled, only a single NAT gateway is created, even if multiple zones are specified |
| `--vpc-owner-aws-creds` | string |  |  | ✓ | Path to VPC owner AWS credentials file |

---

### Deprecated

| Flag | Type | Default | hcp | hypershift | Description |
|------|------|---------|:---:|:----------:|-------------|
| `--multi-arch` | bool | `false` | ✓ | ✓ | If true, this flag indicates the Hosted Cluster will support multi-arch NodePools and will perform additional validation checks to ensure a multi-arch release image or stream was used. |

---

### Summary

| | hcp | hypershift |
|---|:---:|:----------:|
| **Shared Flags** | 74 | 74 |
| **Developer-Only Flags** | - | 6 |
| **Total Flags** | 74 | 80 |
