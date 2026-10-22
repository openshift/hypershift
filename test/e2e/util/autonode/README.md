# AutoNode Local Zone bootstrap coverage

Both `TestKarpenter/Parallel_provisioning_tests/Local_Zone_bootstrap_and_workload`
(v1) and the v2 `autonode-local-zone` lifecycle label invoke the same scenario.
The v2 scenario is serial and is deliberately **not** labeled `Informing`: a
requested bootstrap or workload failure must fail the test process.
Existing standard-AZ arbitrary-subnet tests are unchanged.

## Explicit account and CI prerequisites

Set the zone, instance type and volume type in the test process (and forward
them through the CI step invoking the test binary). v2 also requires a subnet:

| Variable | Required value |
| --- | --- |
| `E2E_AUTONODE_LOCAL_ZONE` | An available Local Zone belonging to the hosted cluster's AWS region, already opted in by the account administrator. |
| `E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID` | v2: an existing, available private subnet in that zone and the hosted cluster VPC. v1: leave unset to create one in the newly provisioned test VPC. |
| `E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE` | An on-demand amd64 instance type offered in that zone, with sufficient CPU/memory for OpenShift, plus account quota and capacity. |
| `E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE` | `gp2` or `gp3`, explicitly confirmed as supported by that Local Zone. There is no storage default. |

With none set, this opt-in coverage skips. Partial configuration, a standard AZ,
wrong region/VPC/subnet, a zone that is not opted in, or an instance type not
offered in the zone fails rather than silently falling back or skipping. The
test never calls the AWS opt-in API. Supported Local Zones and instance/storage
capabilities vary by region and account; there is no fixed region or type list.

Use an AWS hosted cluster with AutoNode enabled and the Karpenter v1 APIs
available (4.23+, or the existing `RUN_KARPENTER_TESTS=true` gate for 4.22 with
an operator built from main). v1 provisions its usual AutoNode hosted cluster;
v2 uses the existing hosted cluster and `AWS_GUEST_INFRA_CREDENTIALS_FILE`.
v1 creates its VPC during the test, so a subnet prepared before invocation
cannot belong to it: leave the subnet input unset. After read-only zone and
instance capability checks, the v1 fixture creates a Local Zone subnet in its
new VPC using `CreateTestSubnet` and registers cleanup before provisioning nodes.
The actual VPC primary CIDR must be `10.0.0.0/16`; auto-creation rejects other
CIDRs before changing resources. For custom VPCs use v2 with a prepared subnet.
This v1 subnet-creation path requires the standard IPv4 VPC and NAT routing;
it does not create an IPv6-only or dual-stack Local Zone subnet.
Do not filter out `TestKarpenter` in v1; in v2 select the
`autonode-local-zone` label and include lifecycle tests. Existing CI jobs do not
automatically gain Local Zone coverage without these inputs and account setup.

The selected subnet must have free addresses, DNS, NAT/egress, and routing and
security rules that permit ignition/control-plane access through the parent
region and image pulls. The existing `CreateTestSubnet` helper chooses a /28
inside a fixed VPC CIDR range and associates a regional NAT route table; this
v1 fixture uses that helper with its standard generated VPC and NAT setup.
For an existing/custom VPC, v2 selects an explicitly prepared subnet, avoiding
those CIDR assumptions. The test neither changes nor deletes a supplied
subnet or its routes. Do not require the Local Zone to appear in a VPC
endpoint service's supported AZ list: that would exclude the case under test.

The finite workload uses the existing e2e image
`quay.io/openshift/origin-pod:4.22.0` with `/bin/sh`, as do the other v1
Karpenter workloads. It requires registry access to pull that public amd64
image; disconnected execution needs an appropriately configured registry mirror.
The test does not replace an image-pull failure with a bootstrap success.

Test credentials need EC2 read access to availability zones (including opt-in
status), subnets, instance-type offerings, instance types and instances, plus
the normal management/hosted Kubernetes permissions. v1 additionally needs the
VPC/subnet/route-table describe, subnet create/delete, and route-table association
permissions used by `CreateTestSubnet`. AutoNode's existing cloud
credentials must be able to provision and terminate instances in the selected
subnet. The root disk uses the existing 120 GiB OpenShift convention with an
explicit supported volume type and delete-on-termination; it does not inherit
the controller's unconditional `gp3` default. No live cloud operation is needed
to compile or unit-test this code.

## Success and cleanup

The scenario snapshots existing nodes, creates an isolated NodeClass selecting
only the selected subnet, and pins its NodePool to the requested zone, instance
type, amd64 and on-demand capacity. A uniquely named, tainted pool and matching
pod selector prevent an existing node from satisfying the workload.

Success requires the workload pod to finish successfully, the assigned node to
be new by UID/name/provider ID and currently Ready, matching zone/pool labels,
and an EC2 lookup confirming the exact subnet, zone and instance type. NodeClass
readiness or instance creation alone is never success. Bootstrap failures are
not repaired or converted into skips; provisioning/workload polling is bounded
to 15 minutes and reports the pod status. A 20-minute context bounds all setup,
provisioning and verification API I/O; a terminal failed pod fails immediately.

Cleanup is registered immediately after each creation, uses a fresh bounded
context even on failure, deletes the pod and NodePool, waits for NodeClaims to
disappear (instance termination), then deletes the NodeClass. v1 then removes
its temporary subnet/route-table association using the existing helper with an
independently bounded, cancellation-detached context and confirms deletion with
a fresh EC2 read; unrecovered deletion errors fail the test. Setup failures in
the existing helper fail immediately, and v1's enclosing fixture owns teardown
of the generated infrastructure even if partial subnet setup fails. A supplied subnet and
account opt-in state remain untouched.

## Public capability references

- [How AWS Local Zones work](https://docs.aws.amazon.com/local-zones/latest/ug/how-local-zones-work.html): regional relationship, enablement, subnet routing and zone-dependent storage capabilities.
- [AWS Local Zones features](https://aws.amazon.com/about-aws/global-infrastructure/localzones/features/): confirm instance types and EBS support for the selected location before configuring CI.
- [DescribeInstanceTypeOfferings](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstanceTypeOfferings.html): the test checks the chosen instance type at availability-zone scope, not merely regional scope.

## Local verification

`DEVELOPMENT.md` documents package-scoped Go checks. Run
`GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race ./test/e2e/util/autonode`.
The CI consumer `.github/workflows/e2e-compile-reusable.yaml` runs `make e2e`;
its v1 compile recipe uses `-tags e2e -c`, while `make e2ev2` compiles with
`-tags e2ev2 -c`. No API or generated files are changed by this test enhancement.
