# Architectural Invariants and Key File Reference

## Architectural Invariants

> **See also**: [Goals and Design Invariants](../../reference/goals-and-design-invariants.md) for the authoritative list of project goals and invariants.

These are the design rules that should inform **every decision**:

1. **Unidirectional communication**: Management cluster -> hosted cluster, never the reverse. All communication originates from within the CP namespace.

2. **Pristine workers**: Compute nodes run only user workloads + minimal agents (kubelet, konnectivity-agent, CNI). No control plane logic.

3. **No mutable CRDs/CRs exposed**: The hosted cluster should not expose mutable resources that could interfere with HyperShift-managed features.

4. **Data plane changes do not trigger management-side lifecycle actions**: Prevents cascading failures.

5. **No user credential management**: HyperShift components do not own credentials; they copy and use them, but ownership remains with the user.

6. **Namespace isolation**: Each CP namespace is isolated via NetworkPolicies and Linux container primitives. See `hypershift-operator/controllers/hostedcluster/network_policies.go`.

7. **Decoupled upgrade signals**: Management-side and data-plane components upgrade independently via `controlPlaneRelease`.

8. **CPO backward compatibility**: The HO may deploy older CPO versions. Changes to the HO must consider impact on older CPOs. The HO checks CPO image labels before enabling features (e.g., `controlPlanePKIOperatorSignsCSRs`, `useRestrictedPSA`, `defaultToControlPlaneV2`).

---

## Key File Reference

### APIs

| File | Contents | Priority |
|------|----------|----------|
| `api/hypershift/v1beta1/hostedcluster_types.go` | HostedCluster spec/status, platform configs, constants, annotations | Must read |
| `api/hypershift/v1beta1/hostedcluster_conditions.go` | HC condition type constants | Must read |
| `api/hypershift/v1beta1/hosted_controlplane.go` | HostedControlPlane spec/status | Must read |
| `api/hypershift/v1beta1/nodepool_types.go` | NodePool spec/status | Must read |
| `api/hypershift/v1beta1/nodepool_conditions.go` | NP condition type constants | Must read |
| `api/hypershift/v1beta1/aws.go` | AWS types (`AWSPlatformSpec`, `AWSRolesRef`) | Read for AWS work |
| `api/hypershift/v1beta1/azure.go` | Azure types | Read for Azure work |
| `api/hypershift/v1beta1/kubevirt.go` | KubeVirt types | Read for KubeVirt work |
| `api/hypershift/v1beta1/controlplanecomponent_types.go` | CPOv2 ControlPlaneComponent CR | Good to know |
| `api/hypershift/v1beta1/etcdbackup_types.go` | Feature-gated type example | Good to know |
| `api/hypershift/v1beta1/groupversion_info.go` | API group registration | Reference |
| `api/CLAUDE.md` | API compatibility rules | Must read |

### HO Controllers

| File | Contents | Priority |
|------|----------|----------|
| `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` | HC Reconciler (~5200 lines) | Must read (selectively) |
| `hypershift-operator/controllers/hostedcluster/network_policies.go` | Namespace isolation NetworkPolicies | Good to know |
| `hypershift-operator/controllers/nodepool/nodepool_controller.go` | NP Reconciler main entry | Must read |
| `hypershift-operator/controllers/nodepool/config.go` | ConfigGenerator, rollout hash | Must read |
| `hypershift-operator/controllers/nodepool/token.go` | Token and UserData Secrets | Must read |
| `hypershift-operator/controllers/nodepool/capi.go` | MachineDeployment, MHC, templates | Must read |
| `hypershift-operator/controllers/nodepool/aws.go` | AWS MachineTemplate builder | Read for AWS work |
| `hypershift-operator/controllers/nodepool/azure.go` | Azure MachineTemplate builder | Read for Azure work |
| `hypershift-operator/controllers/nodepool/kubevirt/kubevirt.go` | KubeVirt MachineTemplate builder | Read for KubeVirt work |
| `hypershift-operator/controllers/nodepool/conditions.go` | SetStatusCondition helpers | Reference |
| `hypershift-operator/controllers/nodepool/version.go` | NodesInfo aggregation from CAPI Machines | Reference |
| `hypershift-operator/controllers/nodepool/scale_from_zero.go` | Scale-from-zero annotation management | Reference |
| `hypershift-operator/controllers/manifests/manifests.go` | Namespace naming, resource naming helpers | Reference |

### CPO Controllers

| File | Contents | Priority |
|------|----------|----------|
| `control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go` | HCP Reconciler (~3200 lines) | Must read (selectively) |
| `control-plane-operator/controllers/hostedcontrolplane/v2/kas/` | kube-apiserver component (complex example) | Good to know |
| `control-plane-operator/controllers/hostedcontrolplane/v2/kube_scheduler/` | kube-scheduler component (simple example) | Must read |
| `control-plane-operator/controllers/hostedcontrolplane/v2/etcd/` | etcd component | Good to know |
| `control-plane-operator/controllers/hostedcontrolplane/v2/capi_manager/` | CAPI manager component | Reference |
| `control-plane-operator/controllers/hostedcontrolplane/v2/capi_provider/` | CAPI provider component | Reference |
| `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/` | Per-platform CCMs | Read per platform |
| `control-plane-operator/controllers/hostedcontrolplane/v2/assets/` | YAML manifests for all components | Reference |

### Framework and Support

| File | Contents | Priority |
|------|----------|----------|
| `support/controlplane-component/controlplane-component.go` | CPOv2 framework core | Must read |
| `support/controlplane-component/builder.go` | Builder pattern for components | Must read |
| `support/controlplane-component/status.go` | Status logic, dependency checking | Must read |
| `support/controlplane-component/workload.go` | Workload reconciliation | Good to know |
| `support/upsert/upsert.go` | CreateOrUpdate wrapper | Must read |

### Platform Implementations

| File | Contents | Priority |
|------|----------|----------|
| `hypershift-operator/controllers/hostedcluster/internal/platform/platform.go` | Platform interface definition | Must read |
| `hypershift-operator/controllers/hostedcluster/internal/platform/aws/aws.go` | AWS platform impl | Read for AWS |
| `hypershift-operator/controllers/hostedcluster/internal/platform/azure/azure.go` | Azure platform impl | Read for Azure |
| `hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt/kubevirt.go` | KubeVirt platform impl | Read for KubeVirt |
| `hypershift-operator/controllers/hostedcluster/internal/platform/agent/agent.go` | Agent platform impl | Read for Agent |

### PKI and Ignition

| File | Contents | Priority |
|------|----------|----------|
| `control-plane-pki-operator/operator.go` | PKI operator wiring | Good to know |
| `control-plane-pki-operator/certrotationcontroller/` | Certificate rotation | Reference |
| `control-plane-pki-operator/certificatesigningcontroller/` | CSR signing | Reference |
| `ignition-server/cmd/start.go` | Ignition HTTPS server | Good to know |
| `ignition-server/controllers/tokensecret_controller.go` | Token reconciler | Good to know |
| `ignition-server/controllers/local_ignitionprovider.go` | MCO binary execution | Reference |

### CLI and Infrastructure

| File | Contents | Priority |
|------|----------|----------|
| `main.go` | CLI entry point | Reference |
| `cmd/cluster/` | create/destroy cluster commands | Reference |
| `cmd/nodepool/` | create/destroy nodepool commands | Reference |
| `cmd/install/` | install command, CRD assets | Reference |
| `cmd/infra/aws/create.go` | AWS infra CLI (`CreateInfra()`) | Read for AWS |
| `cmd/infra/aws/iam.go` | AWS IAM roles and OIDC | Read for AWS |
| `cmd/infra/azure/` | Azure infra CLI | Read for Azure |

### Tests

| File | Contents | Priority |
|------|----------|----------|
| `test/e2e/` | E2E tests (cluster lifecycle, nodepool, upgrades) | Browse for context |
| `test/integration/` | Integration tests (controller behavior) | Browse for context |
| `api/hypershift/v1beta1/nodepool_types_test.go` | Serialization compatibility test example | Must read for API changes |
