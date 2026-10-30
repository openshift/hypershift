# IBM provider schema compatibility

HyperShift copies the VPC v1beta1/v1beta2 and PowerVS v1beta2 API types
from [CAPIBM v0.14.0](https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/tree/5d081d1caea01b531f48092f0a784ceaa4298b64/api).
This source update does not update the deployed provider controller.

`compatibility.yaml` is a maintained generation input, not generated output.
Its five complete PowerVS v1beta1 version entries and nine served/storage
policies were captured from `cmd/install/assets/crds/cluster-api-provider-ibmcloud`
at [c87e4905311af782d166e0f06ee102a5bebdf487](https://github.com/openshift/hypershift/tree/c87e4905311af782d166e0f06ee102a5bebdf487/cmd/install/assets/crds/cluster-api-provider-ibmcloud).
Each CRD name identifies the corresponding
`infrastructure.cluster.x-k8s.io_<plural>.yaml` source file. The same immutable
assets are recorded in the transformer's `testdata` for regression comparisons.
Generation does not fetch these inputs or read Git history.

The machine `spec.systemType` and machine-template
`spec.template.spec.systemType` enums retain all previously accepted values,
including `s1122`. This is a downstream schema compatibility exception, not
a statement about upstream hardware support. PowerVS v1beta1 stays served;
all nine CRDs retain v1beta2 storage and existing deprecation settings.
PowerVS v1beta3 is deliberately excluded. No conversion webhook or storage
migration is introduced, and historical `None` conversion is not certified
as lossless by this change.

The PowerVS v1beta2 source package is deprecated upstream. The scoped import
lint exception is deliberate while this compatibility stage retains the
existing runtime API. Immutable baseline fixture copies are excluded from
spelling checks to preserve the original schema descriptions byte-for-byte.

Run `make capi-sync-ibmcloud` after changing the isolated source pin or imports,
then `make cluster-api-provider-ibmcloud`. The latter builds the typed
`hack/tools/capi-crd-compat` helper and applies this manifest inside the staging
directory before replacing installed assets. A validation failure leaves the
destination untouched. Other providers do not invoke the helper.

Run the regression tests from `hack/tools` with
`GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go test -race -count=1 ./capi-crd-compat/...`.
Changes to this policy require explicit version/schema compatibility review;
do not copy a newer provider's storage or conversion policy implicitly.
