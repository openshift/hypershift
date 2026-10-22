# AutoNode Local Zone bootstrap coverage

The v2 `autonode-local-zone` lifecycle label runs this scenario. It is serial
and deliberately **not** labeled `Informing`: a
requested bootstrap or workload failure must fail the test process.
Existing standard-AZ arbitrary-subnet tests are unchanged.

## Explicit account and CI prerequisites

Set the zone, instance type and volume type in the test process (and forward
them through the CI step invoking the test binary). No prepared subnet is required:

| Variable | Required value |
| --- | --- |
| `E2E_AUTONODE_LOCAL_ZONE` | An available Local Zone belonging to the hosted cluster's AWS region, already opted in by the account administrator. |
| `E2E_AUTONODE_LOCAL_ZONE_SUBNET_ID` | Optional override selecting an existing, available private subnet in that zone and the hosted cluster VPC. Leave unset for automatic creation and cleanup. |
| `E2E_AUTONODE_LOCAL_ZONE_INSTANCE_TYPE` | An on-demand amd64 instance type offered in that zone, with sufficient CPU/memory for OpenShift, plus account quota and capacity. |
| `E2E_AUTONODE_LOCAL_ZONE_VOLUME_TYPE` | `gp2` or `gp3`, explicitly confirmed as supported by that Local Zone. There is no storage default. |

With none set, this opt-in coverage skips. Partial configuration, a standard AZ,
wrong region/VPC/subnet, a zone that is not opted in, or an instance type not
offered in the zone fails rather than silently falling back or skipping. The
test never calls the AWS opt-in API. Supported Local Zones and instance/storage
capabilities vary by region and account; there is no fixed region or type list.

Use an AWS hosted cluster with AutoNode enabled and the Karpenter v1 APIs
available (4.23+, or the existing `RUN_KARPENTER_TESTS=true` gate for 4.22 with
an operator built from main). The test uses the existing hosted cluster and
`AWS_GUEST_INFRA_CREDENTIALS_FILE`. Select the `autonode-local-zone` label and
include lifecycle tests. Existing CI jobs do not
automatically gain Local Zone coverage without these inputs and account setup.

The selected subnet must have free addresses, DNS, NAT/egress, and routing and
security rules that permit ignition/control-plane access through the parent
region and image pulls. Automatic setup lists all subnet pages, reuses the
existing AWS test CIDR allocator to find a free /28 in the VPC's associated
IPv4 CIDRs, and associates an existing active IPv4 default NAT route table.
Custom IPv4 CIDRs are supported; there is no fixed 10.0.0.0/16 assumption.
It does not create a VPC, NAT gateway, endpoint, security group or IPv6-only
subnet, or change existing routing rules. An active default NAT route table
and sufficient unallocated VPC addresses are external prerequisites. Concurrent
external allocation conflicts fail visibly rather than adopting another subnet.
The test neither changes nor deletes an override subnet or its routes.
Do not require the Local Zone to appear in a VPC
endpoint service's supported AZ list: that would exclude the case under test.

The finite workload uses the existing e2e image
`quay.io/openshift/origin-pod:4.22.0` with `/bin/sh`. It requires registry access to pull that public amd64
image; disconnected execution needs an appropriately configured registry mirror.
The test does not replace an image-pull failure with a bootstrap success.

Test credentials need EC2 read access to availability zones (including opt-in
status), subnets, instance-type offerings, instance types and instances, plus
the normal management/hosted Kubernetes permissions. Automatic setup additionally
needs VPC/route-table describe, subnet create/tag/delete and route-table
associate/disassociate permissions. AutoNode's existing cloud
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
disappear (instance termination), then deletes the NodeClass. Only then does it
disassociate the owned route-table association and delete the owned subnet,
retrying dependencies and confirming deletion through EC2. Unrecovered cleanup
errors fail the test; if NodeClaim termination is not confirmed, network cleanup
is refused with an error rather than disrupting a remaining node. Partial subnet
setup registers cleanup before its error is asserted. Override infrastructure and
account opt-in state remain untouched.

## v2 setup ownership and call path

`cmd/create-guests` owns the hosted cluster and VPC; AWS `ClusterSpecs` supplies
the existing `karpenter` variant with AutoNode and PublicAndPrivate endpoints.
`cmd/run-tests` invokes the v2 binary with the existing cluster and forwards its
environment. Suite setup establishes `TestContext`; the serial lifecycle `It`
then calls `RunLocalZoneTest` with that cluster's actual VPC and EC2 credentials.
After read-only zone/instance preflight, `createLocalZoneSubnet` performs the
test-owned network setup before NodeClass creation. Its returned subnet ID is
the sole NodeClass subnet selector. The spec owns immediate LIFO cleanup,
matching existing v2 lifecycle resource conventions; no unrelated cluster-wide
setup hooks or persisted shared-dir state are needed. `cmd/destroy-guests`
continues to own final hosted-cluster/VPC teardown, not the spec's temporary subnet.

## Public capability references

- [How AWS Local Zones work](https://docs.aws.amazon.com/local-zones/latest/ug/how-local-zones-work.html): regional relationship, enablement, subnet routing and zone-dependent storage capabilities.
- [AWS Local Zones features](https://aws.amazon.com/about-aws/global-infrastructure/localzones/features/): confirm instance types and EBS support for the selected location before configuring CI.
- [DescribeInstanceTypeOfferings](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstanceTypeOfferings.html): the test checks the chosen instance type at availability-zone scope, not merely regional scope.

## Local verification

`DEVELOPMENT.md` documents package-scoped Go checks. Run
`GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race ./test/e2e/util/autonode`.
Also test the reused allocator with `go test ./test/e2e/util -run TestFindFreeSubnetCIDR`
using the same module environment. `make e2ev2` compiles with `-tags e2ev2 -c`;
the CI e2e compile workflow consumes that target through `make e2e`.
No API or generated files are changed by this test enhancement.
