# HostedCluster Controller Reconciliation Loop — Segregation Analysis

## Problem Statement

The `reconcile()` function in `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go`
(lines 383–2113) executes ~50 distinct operations in a strictly sequential chain where **every error
causes an early return**, short-circuiting the entire remaining loop. This means an unrelated failure
(e.g., missing SSH key secret) prevents critical operations like deploying the control plane operator
or reconciling the HostedControlPlane object.

## Current Operation Map

### Pre-requisite Phase (lines 383–636)

| # | Operation | Lines | Short-circuits? | Depends on | Produces |
|---|-----------|-------|-----------------|------------|----------|
| P1 | Get HCP object | 384-393 | Yes (non-404) | — | `hcp` |
| P2 | Bubble up ValidIdentityProvider (AWS) | 398-424 | Yes (status update) | hcp | status condition |
| P3 | Bubble up AWSDefaultSecurityGroupDeleted | 428-490 | Yes (multiple) | hcp | status condition |
| P4 | Bubble up CloudResourcesDestroyed | 494-518 | Yes (status update) | hcp | status condition |
| P5 | Parse HCDestroyGracePeriod | 520-527 | Yes | — | grace period |
| P6 | Handle Deletion (exits early if deleting) | 530-609 | Yes (expected) | — | — |
| P7 | Dereference AWS roles | 615-619 | Yes | — | mutated spec |
| P8 | AWS KMS role from secret | 620-629 | Yes | — | mutated spec |
| P9 | Reconcile platform defaults | 634-635 | Yes | — | mutated spec |
| P10 | Update HC if spec changed | 639-642 | Yes (expected) | P7-P9 | — |

### Part One: Status Updates (lines 644–1243)

| # | Operation | Lines | Short-circuits? | Depends on | Produces |
|---|-----------|-------|-----------------|------------|----------|
| S1 | Set kubeconfig status | 647-657 | Yes (non-404 Get) | — | status field |
| S2 | Reconcile ICSP/IDMS registry | 660-663 | **Yes** | — | `releaseProvider`, `registryClientImageMetadataProvider` |
| S3 | Get pull secret bytes | 667-670 | **Yes** | — | `pullSecretBytes` |
| S4 | Get CPO image | 672-675 | **Yes** | S2, S3 | `controlPlaneOperatorImage` |
| S5 | Get CPO image labels | 676-679 | **Yes** | S4, S3 | `controlPlaneOperatorImageLabels` |
| S6 | Set custom kubeconfig status | 683-695 | Yes (non-404 Get) | S5 | status field |
| S7 | Set kubeadminPassword status | 698-713 | Yes (non-404 Get) | — | status field |
| S8 | Set version status | 716 | No | hcp | status field |
| S9 | Copy CVO conditions | 719-781 | No | hcp | status conditions |
| S10 | Copy Degraded condition | 783-804 | No | hcp | status condition |
| S11 | Copy KubeVirt MTU / LiveMigratable | 807-818 | **Yes** | hcp | status condition |
| S12 | Copy HCP conditions (Etcd, KAS, etc.) | 820-853 | No | hcp | status conditions |
| S13 | Copy platform status | 855-858 | No | hcp | status field |
| S14 | Copy AWS/Azure conditions | 860-885 | No | hcp | status conditions |
| S15 | Unmanaged etcd validation | 889-906 | Yes (non-404 Get) | — | status condition |
| S16 | Set Available condition | 908-933 | **Yes** (Patch) | hcp | status condition |
| S17 | Copy AWS/GCP endpoint conditions | 935-969 | No (handles errors) | — | status conditions |
| S18 | Set ValidConfiguration | 971-987 | No | — | status condition |
| S19 | Set SupportedHostedCluster | 989-1005 | No | — | status condition |
| S20 | Set ValidProxyConfiguration | 1007-1031 | No | — | status condition |
| S21 | Set Ignition Server endpoint | 1033-1087 | Yes (multiple) | — | status field |
| S22 | Set control plane endpoints | 1089-1101 | No | hcp | status field |
| S23 | Set Ignition server availability | 1103-1146 | Yes (non-404 Get) | — | status condition |
| S24 | Set ReconciliationActive | 1147 | No | — | status condition |
| S25 | Set ValidReleaseImage | 1149-1178 | No | S2 | status condition |
| S26 | Determine payload arch | 1180-1195 | **Yes** | S3 | `payloadArch` |
| S27 | Lookup release image | 1197-1200 | **Yes** | S2 | `releaseImage` |
| S28 | Set Progressing condition | 1201-1230 | No | S27, S3 | status condition |
| S29 | Copy configuration status | 1232-1235 | No | hcp | status field |
| S30 | Persist all status | 1237-1243 | **Yes** | all S* | — |

### Part Two: Reconcile State of the World (lines 1245–2112)

| # | Operation | Lines | Short-circuits? | Depends on | Produces |
|---|-----------|-------|-----------------|------------|----------|
| R1 | Add finalizer | 1247-1256 | Yes | — | — |
| R2 | Add CAPI finalizers | 1258-1261 | Yes | — | — |
| R3 | Handle pause (exits early if paused) | 1263-1273 | Yes (expected) | hcp | — |
| R4 | Default cluster IDs | 1275-1277 | Yes | — | — |
| R5 | Reconcile CLI secrets | 1279-1281 | Yes | — | — |
| R6 | Reconcile AWS resource tags | 1283-1286 | Yes | — | — |
| R7 | Block on invalid config (exits early) | 1288-1318 | Yes (expected) | S18, S19, S25 | — |
| R8 | Set CPO utilities/labels | 1320-1333 | No | S5 | utility vars |
| R9 | Reconcile CP namespace | 1335-1385 | **Yes** | — | `controlPlaneNamespace` |
| R10 | Get platform | 1387-1390 | **Yes** | S2, S3 | `p` |
| R11 | Reconcile platform credentials | 1392-1419 | **Yes** | R10 | credentials in CP NS |
| R12 | Set restored-from-backup condition | 1421-1466 | Yes | hcp | status condition |
| R13 | Sync pull secret to CP NS | 1468-1494 | **Yes** | R9 | secret in CP NS |
| R14 | Reconcile secret encryption | 1496-1582 | Yes | R9, R10 | secrets in CP NS |
| R15 | Sync audit webhook | 1584-1618 | Yes | R9 | secret in CP NS |
| R16 | Sync SSH key | 1620-1647 | Yes | R9 | secret in CP NS |
| R17 | Sync AdditionalTrustBundle | 1649-1653 | Yes | R9 | CM in CP NS |
| R18 | Reconcile SA signing key | 1655-1660 | Yes | R9 | secret in CP NS |
| R19 | Sync unmanaged etcd MTLS | 1662-1694 | Yes | R9 | secret in CP NS |
| R20 | Reconcile ETCD member recovery | 1696-1705 | Yes | R9 | requeueAfter |
| R21 | Sync global config CMs/secrets | 1707-1760 | Yes | R9 | CMs/secrets in CP NS |
| R22 | Parse release image version | 1762-1767 | **Yes** | S27 | `releaseImageVersion` |
| R23 | Reconcile HCP object | 1769-1789 | **Yes** | many | HCP spec updated |
| R24 | Reconcile CAPI Infra CR | 1791-1798 | **Yes** | R10, R23 | `infraCR` |
| R25 | Reconcile AWS subnets | 1800-1802 | Yes | R24 | — |
| R26 | Reconcile cluster prometheus RBAC | 1804-1809 | Yes | R9 | — |
| R27 | Reconcile CAPI Cluster | 1811-1821 | Yes | R23, R24 | — |
| R28 | Reconcile monitoring dashboard | 1823-1828 | Yes | — | — |
| R29 | Sync kubeconfig | 1830-1866 | Yes | R23 (hcp status) | — |
| R30 | Sync custom kubeconfig | 1868-1922 | Yes | R23 (hcp status) | — |
| R31 | Sync kubeadmin password | 1924-1965 | Yes | R23 (hcp status) | — |
| R32 | Get default ingress domain | 1967-1970 | **Yes** | — | `defaultIngressDomain` |
| R33 | Reconcile SRE metrics config | 1972-1975 | Yes | R23 | — |
| R34 | Reconcile OpenShift trusted CAs | 1977-1980 | Yes | R23 | — |
| R35 | Build cpContext | 1982-2004 | Yes | R22 | `cpContext` |
| R36 | Reconcile Control Plane Operator | 2006-2010 | **Yes** | R35, R32 | CPO deployment |
| R37 | Reconcile CAPI Manager | 2012-2016 | **Yes** | R35 | CAPI manager deployment |
| R38 | Reconcile CAPI Provider | 2018-2021 | **Yes** | R35, R10 | CAPI provider deployment |
| R39 | Reconcile PKI operator RBAC | 2023-2029 | Yes | — | ClusterRoles/Bindings |
| R40 | Reconcile network policies | 2031-2034 | Yes | R22, S5 | NetworkPolicies |
| R41 | Platform-specific reconciliation | 2036-2101 | Yes | R10, R23 | platform resources |
| R42 | Reconcile karpenter operator | 2103-2105 | Yes | R35 | karpenter deployment |

## Dependency Graph (Critical Paths)

```
                  +-----------------------------------------------------+
                  |  CRITICAL PREREQUISITES (must succeed first)         |
                  |  P1: Get HCP                                        |
                  |  S2: ICSP/IDMS registry                             |
                  |  S3: Pull secret bytes                              |
                  |  S4+S5: CPO image + labels                          |
                  |  S27: Lookup release image -> S26: payload arch     |
                  |  R22: Parse release image version                   |
                  |  R10: Get platform                                  |
                  +------------------------+----------------------------+
                                           |
            +------------------------------+------------------------------+
            |                              |                              |
            v                              v                              v
  +-----------------+   +--------------------------+   +--------------------------+
  | BLOCK A:        |   | BLOCK B:                 |   | BLOCK C:                 |
  | Status Updates  |   | Namespace & Foundation   |   | Validation Gates         |
  | (S1-S30)        |   | R1: Finalizer            |   | R7: Block on invalid     |
  | Mostly reads,   |   | R2: CAPI finalizers      |   |     config/release       |
  | independent     |   | R4: Cluster IDs          |   |                          |
  | from each other |   | R5: CLI secrets          |   | (depends on S18,S19,S25) |
  |                 |   | R6: AWS resource tags    |   |                          |
  |                 |   | R9: CP namespace --------+-->|                          |
  +-----------------+   +-----------+--------------+   +-----------+--------------+
                                    |                              |
                                    | (R9: CP namespace exists)    |
                                    v                              |
            +------------------------------------------+           |
            | BLOCK D: Secret & ConfigMap Sync         |           |
            | R11: Platform credentials                |           |
            | R13: Pull secret sync                    |           |
            | R14: Secret encryption                   |           |
            | R15: Audit webhook                       |           |
            | R16: SSH key                             |           |
            | R17: AdditionalTrustBundle               |           |
            | R18: SA signing key                      |           |
            | R19: Unmanaged etcd MTLS                 |           |
            | R21: Global config CMs/secrets           |           |
            |                                          |           |
            | *** ALL INDEPENDENT OF EACH OTHER ***    |           |
            +---------------------+--------------------+           |
                                  |                                |
                                  v                                |
            +------------------------------------------+           |
            | BLOCK E: Core Control Plane              |<----------+
            | R23: Reconcile HCP object                |
            | R24: Reconcile CAPI Infra CR (-> R23)    |
            | R25: Reconcile AWS subnets (-> R24)      |
            | R27: Reconcile CAPI Cluster (-> R23,R24) |
            |                                          |
            | *** STRICTLY SEQUENTIAL ***              |
            +---------------------+--------------------+
                                  |
            +--------------+------+--------+---------------+
            v              v               v               v
  +--------------+ +------------+ +-------------+ +--------------+
  | BLOCK F:     | | BLOCK G:   | | BLOCK H:    | | BLOCK I:     |
  | Operators    | | RBAC &     | | Kubeconfig  | | Platform-    |
  |              | | Policies   | | & Secrets   | | specific     |
  | R36: CPO     | | R26: Prom  | | R29: kube-  | | R41: OIDC/   |
  | R37: CAPI    | |     RBAC   | |   config    | |   SecretProv |
  |      manager | | R39: PKI   | | R30: custom | |   KubeVirt   |
  | R38: CAPI    | |      RBAC  | |   kubeconf  | |              |
  |      provider| | R40: Net   | | R31: admin  | |              |
  | R42: Karpent | |      Pol.  | |   password  | |              |
  +--------------+ +------------+ +-------------+ +--------------+

  +-------------------------------------------------+
  | BLOCK J: Auxiliary / Monitoring                  |
  | R12: Restored-from-backup condition              |
  | R20: ETCD member recovery                        |
  | R28: Monitoring dashboard                        |
  | R33: SRE metrics config                          |
  | R34: OpenShift trusted CAs                       |
  |                                                  |
  | *** LARGELY INDEPENDENT ***                      |
  +-------------------------------------------------+
```

## Key Problems Identified

1. **Secret sync operations block each other** (R13-R21): A missing SSH key secret prevents
   pull secret sync, secret encryption setup, ETCD MTLS, and global config sync. These are
   entirely independent operations.

2. **Secret sync failures block HCP reconciliation** (R23): If any secret sync fails, the
   HostedControlPlane object never gets updated. This is the most critical operation — it
   creates/updates the HCP that drives everything the CPO does.

3. **HCP reconciliation failure blocks operator deployments** (R36-R38): Even if the HCP is
   already created and needs a minor spec update, failure blocks CPO, CAPI manager, and CAPI
   provider deployments from being created or updated.

4. **Operator deployment failures block each other**: CPO failure blocks CAPI manager and
   provider. These are independent deployments.

5. **RBAC failures block unrelated operations**: Network policy failure blocks karpenter
   operator and platform-specific items.

6. **Status update failures block the world**: Some status updates (e.g., KubeVirt LiveMigratable
   at line 815) short-circuit and prevent all of Part Two from executing.

7. **Kubeconfig/kubeadmin sync blocks late operations**: These are relatively simple copy
   operations that, if they fail, prevent SRE metrics, trusted CAs, CPO deployment, and
   karpenter from being reconciled.

## Impact Summary

**Before**: An error in any of ~50 operations blocks all subsequent operations. Examples:
- SSH key secret missing -> CPO never deployed -> control plane never starts
- Monitoring dashboard error -> karpenter operator never reconciled
- AWS resource tags error -> HCP never updated

**After (proposed)**: Only genuinely dependent operations block each other. Examples:
- SSH key secret missing -> SSH key not synced (reported via condition), but CPO still
  deploys, HCP still reconciles, CAPI still works
- Monitoring dashboard error -> reported, but karpenter and network policies proceed normally
- Platform credential error -> reported, but kubeconfig sync, RBAC, and auxiliary operations
  continue
