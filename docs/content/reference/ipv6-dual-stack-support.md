# IPv6 and Dual-Stack Support

HyperShift's IPv6 and dual-stack support is layered. The API and the
control plane are network-stack aware on every platform, while platform
infrastructure support varies from purpose-built (KubeVirt) to fully
delegated to CAPI providers and cloud controller managers (AWS, Azure,
GCP).

This page maps what is supported where, with pointers to the code that
implements it. It records what is verifiable in this repository —
platforms without in-repo IPv6 handling are listed as such rather than
assumed working or broken.

## API level (all platforms)

The `ClusterNetworking` spec in
`api/hypershift/v1beta1/hostedcluster_types.go` accepts IPv4 or IPv6
CIDRs:

- `machineNetwork` supports one entry, or two for dual stack, and is
  immutable once set. `clusterNetwork` and `serviceNetwork` accept up to
  two entries; API comments note single entries as the currently
  documented level, and the dual-stack Agent lab exercises two-entry
  lists. All three networks are immutable.
- Defaults are IPv4: `10.132.0.0/14` for the cluster network and
  `172.31.0.0/16` for the service network.
- `APIServerNetworking.advertiseAddress` defaults to `172.20.0.1` (IPv4)
  or `fd00::1` (IPv6, `DefaultAdvertiseIPv6Address` in
  `support/config/constants.go`) based on the cluster network family.

Validation is split between admission and the HostedCluster controller
(the latter surfaced as the `ValidConfiguration` condition):

- CIDRs must be unique across `machineNetwork`, `clusterNetwork` and
  `serviceNetwork`; admission rejects duplicates, and the controller
  rejects overlaps.
- Multiple network stacks declared as the *primary* network (index 0 of
  each list) are rejected.
- For OVN clusters, join and transit subnets must not overlap the hosted
  cluster's own networks.

## Control plane behavior (all platforms)

Regardless of platform, the control plane HyperShift deploys is
IPv6-aware:

- The kube-apiserver and OAuth control plane Services in the management
  cluster are created with `ipFamilyPolicy: PreferDualStack`, so they
  pick up IPv6 addresses when the management cluster is dual stack.
- IPv6 literals are bracketed where URLs and endpoints are built
  (kubeconfigs, OAuth issuer and callback URLs, PKI SANs). Unbracketed
  IPv6 in OAuth URLs previously broke authentication on dual-stack
  clusters. A linter
  (`hack/tools/hypershiftlinter/analyzers/ipv6url`) keeps v2 e2e tests
  from reintroducing the pattern.
- The kube-apiserver EndpointSlice is created per address family based on
  the advertise address.
- Single-stack IPv4 hosted clusters get IPv4-preferred DNS when connecting
  to the Konnectivity server on dual-stack management clusters.
- The node-local apiserver HAProxy supports HTTP proxying in IPv6
  scenarios.

## Platform matrix

| Platform | IPv6 / dual-stack status |
|----------|--------------------------|
| KubeVirt | Purpose-built dual-stack support (see notes) |
| Agent | Network-agnostic platform (no IPv6 code needed); IPv6-only and dual-stack guest networking demonstrated in labs and weekly CI periodics (see notes) |
| OpenStack | IPv6 subnet plumbing to CAPO (see notes) |
| AWS | No IPv6 platform code; load balancers and subnets delegated to CAPI/AWS CCM |
| Azure | No IPv6 platform code; Private Link Service API fields accept IPv6 addresses |
| GCP | No IPv6 platform code; PSC API fields accept IPv6 addresses |
| PowerVS / IBM Cloud | No in-repo IPv6 evidence; networking delegated to the CAPI provider |
| None | No platform networking; clusters inherit the control plane behavior above |

## Platform notes

### KubeVirt

KubeVirt is the only platform with purpose-built IPv6/dual-stack
handling:

- **Multus primary networks and SLAAC**: the MCO renders nmstate
  configuration (disable IPv6 autoconf, IPv6 via KubeVirt's ARP proxy
  gateway) that is only correct for the default pod network. For
  NodePools using a multus network as primary
  (`--attach-default-network=false`), HyperShift overrides those files
  with no-ops so SLAAC works
  (`hypershift-operator/controllers/nodepool/kubevirt/network.go`).
  IPv4-only clusters are deliberately excluded, keeping the MachineConfig
  out of their NodePool config hash.
- **OVN IPv6 join subnet**: KubeVirt clusters with IPv6 networking are
  auto-defaulted to the `fd99::/64` OVN join subnet
  (`KubevirtDefaultV6InternalJoinSubnet` in
  `api/hypershift/v1beta1/operator.go`) to avoid routing conflicts with
  the management cluster's `fd98::/64`. The HyperShift operator's `ipv6`
  OVN config exposes `internalJoinSubnet` and
  `internalTransitSwitchSubnet`, both immutable once set, for collision
  avoidance in other setups.
- **Per-family endpoints and policies**: ingress EndpointSlices are built
  per IP family from VMI interface addresses, and virt-launcher
  NetworkPolicies block egress per family.
- **Tests**: `test/e2e/nodepool_kv_advanced_multinet_test.go` asserts the
  nmstate override behavior, conditional on the HostedCluster having IPv6
  networking.

### Agent

Agent has no IPv6 platform code because it needs none: the platform
provisions no network infrastructure. Machines are pre-provisioned
bare-metal hosts that register through the Agent flow, so HyperShift's
only IPv6-sensitive surfaces are the generic ones (CIDR validation,
advertise address, ignition endpoint rendering). The one Agent-specific
piece is rendering IPv6 ignition endpoints as bracketed URLs in the
InfraEnv, which is unit-tested in
`hypershift-operator/controllers/hostedcluster/internal/platform/agent/agent_test.go`.

The labs demonstrate two different things:

- The **IPv6 lab** (`docs/content/labs/IPv6/`) runs IPv6 at the
  infrastructure layer only: the bare-metal network, DNS and ignition
  endpoint are IPv6, while the hosted cluster's own pod and service
  networks in that lab are IPv4.
- The **Dual-Stack lab** (`docs/content/labs/Dual/`) runs a dual-stack
  guest cluster network: `fd01::/48` and `fd02::/112` alongside the IPv4
  defaults.

The `--default-dual` CLI flag (Agent-only, `cmd/cluster/agent/create.go`)
sets those dual-stack defaults: `10.132.0.0/14` + `fd01::/48` cluster
network and `172.31.0.0/16` + `fd02::/112` service network.

CI coverage: openshift/release defines MCE Agent metal3 conformance
periodics for connected dual-stack
(`e2e-agent-connected-ovn-dualstack-metal-conformance`), disconnected
dual-stack, and disconnected IPv6-only
(`e2e-agent-disconnected-ovn-ipv6-metal-conformance`) on every release
branch from 4.16 to 5.1. On the current branches (4.21 through 5.1) the
dual-stack jobs run weekly, and IPv6-only runs weekly on 5.0 and 5.1.
Older branches keep the same jobs on infrequent schedules (4.19–4.20 a
few times a month, 4.16–4.18 roughly annually). The hostedcluster
create step maps `IP_STACK=v6` to IPv6-only guest CIDRs
(`fd03::/48`, `fd04::/112`) and `IP_STACK=v4v6` to `--default-dual`, so
these jobs exercise guest cluster networking, not just the
infrastructure layer.

Remaining gaps: periodics only (no presubmit coverage), IPv6-only is only
tested disconnected, and the in-repo labs remain disconnected.

### OpenStack

- Subnet filters pass `IPVersion`, `IPv6AddressMode` and `IPv6RAMode` to
  CAPO; the API requires that when two subnets are specified, one must be
  IPv4 and the other IPv6.
- Machine networks feed the cloud-config `address-sort-order`.
- Additional ports (Multus) can attach workloads to IPv6 networks — see
  [OpenStack additional ports](../how-to/openstack/additional-ports.md).

### AWS, Azure, GCP

- No IPv6-aware platform code: load balancer IP families, subnet address
  assignment and node networking are delegated to each platform's CAPI
  provider and cloud controller manager.
- API fields for user-supplied IPs (Azure Private Link Service, GCP
  Private Service Connect) accept IPv4 or IPv6 addresses.
- The AWS IAM policy includes `ec2:AssignIpv6Addresses` permissions for
  the cloud provider's use.

### PowerVS / IBM Cloud and None

- No IPv6-specific code in the PowerVS or IBM Cloud platform
  implementations; networking is delegated to the CAPI provider.
- The None platform implements no networking of its own; clusters inherit
  the generic control plane behavior described above.

## Test coverage

- No IPv6 or dual-stack job is defined in this repository; the coverage
  lives in openshift/release (below). KubeVirt's IPv6 e2e assertions
  activate only when the HostedCluster is created with IPv6 networking,
  which the default (IPv4) clusters do not.
- openshift/release runs MCE Agent metal3 conformance periodics covering
  connected dual-stack, disconnected dual-stack and disconnected
  IPv6-only on every release branch; the dual-stack jobs are weekly on
  4.21+ and IPv6-only is weekly on 5.0+ (see the Agent notes above).
- The release step registry's KubeVirt create step supports `v4v6`,
  `v6v4` and `v6` IP stacks using explicit non-conflicting IPv6 CIDRs
  (`fd03::/48`, `fd04::/112`) — `--default-dual` does not work for
  KubeVirt — but no job currently invokes it with a dual-stack or IPv6
  stack.
- Unit-level IPv6 coverage exists for kubeconfig/PKI bracketing, KubeVirt
  ingress EndpointSlices, metrics proxying and Agent ignition endpoints.

## Known limitations

- Components that read only the first entry of
  `Service.Status.LoadBalancer.Ingress` see just the primary family's
  address; in dual-stack environments use an address from the Service's
  primary IP family (see
  [Deterministic load balancer IP](../how-to/common/deterministic-loadbalancer-ip.md)).
- The in-repo IPv6 labs are disconnected (air-gapped) environments,
  because remote registries cannot work with IPv6 in that setup. Of the
  CI periodics, only the dual-stack job runs connected; IPv6-only is
  tested disconnected.

For general platform support semantics (management cluster coupling,
mixed platforms), see [Multi-platform support](multi-platform-support.md).
