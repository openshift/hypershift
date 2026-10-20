# Managed DNS fixture compatibility

The AWS `managed-dns` fixture is created by the main tests image even when the
selected control plane comes from an older release. The management operator
copies the AWS platform spec to the HostedControlPlane, but DNS zone creation
and hosted cluster DNS configuration are implemented by the CPO and HCCO in the
selected control plane image. A newer management operator alone cannot provide
that implementation.

Before provisioning this fixture, create-guests resolves the CPO using the same
image selection and image-label override helpers as the management operator.
Registry providers use management-cluster IDMS/ICSP mirrors, and authentication
uses the effective CLI `--pull-secret`, including fixture overrides.
It enables `--managed-dns` only when that image advertises
`io.openshift.hypershift.control-plane-operator-manages-aws-dns=true`.
This is a capability boundary, not a version comparison: release 4.22 and 5.0
images without the implementation remain eligible for the baseline fixture,
and an image with a backported implementation can opt in regardless of version.
New CPO images must be built and promoted with this label before CI will exercise
the feature. Registry lookup errors fail fixture creation, rather than being
treated as an unsupported capability.

On an unsupported image the fixture retains its normal externally provisioned
DNS settings and omits the immutable managed DNS opt-in at creation time. The
existing managed DNS specs then explicitly skip because the feature was not
requested. This does not change the public `hypershift create cluster aws
--managed-dns` command, HC/HCP API fields, JSON shapes, or defaulting/validation.
Earlier API consumers have no new required field or status to produce.

Capability selection happens before any HostedCluster status exists; missing
DNS zones or an absent/Unknown condition are never evidence that a capable CPO
may skip its assertions. When the opt-in survives management CRD admission, both
existing DNS tests remain strict about zone status, the availability condition,
and hosted cluster DNS wiring. The management cluster must still install the
TechPreviewNoUpgrade CRDs; without them the feature field can be pruned and the
existing tests skip. Guest `--feature-set` alone does not enable management CRDs.

Focused validation:

```sh
GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race -tags=e2ev2 -count=1 -timeout=10m ./test/e2e/v2/cmd/create-guests
make test-e2ev2-unit
```
