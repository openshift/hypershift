# Feature Gates

Feature gates in OpenShift allows to ensure everything new works together and optimizes for rapid evaluation in CI to promote without ever releasing a public preview.
There are no guarantees that your fleet continues to be operational long term after you enable a feature gate.

In the HCP context, there are multiple non exclusive scenarios where features might need to be gated:

1 - A feature that impacts HO install
E.g. New CRDs are required for CPOv2 (control plane operator version 2)

2 - A feature that impacts the whole cluster fleet / HO
E.g. Introduce fleet wide shared ingress to be validated in targeted environments

3 - A feature that impacts individual clusters
E.g. Introduce using CPOv2 for some HC to develop feedback

4 - A feature that impacts API
E.g. Introduce a new provider like Openstack
E.g. Introduce a new field/feature like AWS tenancy

5 - A feature specific for an OCP component
Components honour existing standalone in-cluster OCP feature gate mechanisim

## Users
All the feature gates are grouped in a single TechPreviewNoUpgrade feature set. Current implementation exposes this --tech-preview-no-upgrade flag in the CLI at install time

```
hypershift install --help
```
Will show among other flags:
```
--tech-preview-no-upgrade                        If true, the HyperShift operator runs with TechPreviewNoUpgrade features enabled
```

In a follow up we'll consider to introduce support to also signal --tech-preview-no-upgrade at the HC level.
Eventually support for at least 1, 2, 3 and 4 afromentioned scenarios will most likely converge into a single API.

## Devs

We rely on [openshift/api](https://github.com/openshift/api) tooling for generating CRDs with [openshift markers](https://github.com/openshift/kubernetes-sigs-controller-tools/blob/96a305393cb22f0c69c4ee59be27ad09057cc704/pkg/crd/markers/patch_validation.go#L30-L36). See [this PR](https://github.com/openshift/hypershift/pull/5047) as an example of a adding a field behind a feature gate.

The currently ongoing implementation of feature gates for the controllers business logic relies on "k8s.io/component-base/featuregate". This enables devs to declare [granular gates for their features](https://github.com/openshift/hypershift/blob/9f5ccaef47cdcf9d2df91134571f1783e99e30fe/hypershift-operator/featuregate/feature.go).
See [this PR](https://github.com/openshift/hypershift/pull/4980) as an example.

### Promoting a feature gated API field and feature to sable

Generally speaking any new field should start by being feature gated.
The minimum criteria for promotion is:

- Provide clear context and analysis on the PR about how the field might impact the different GA products. This includes but it is not limited to ROSA, ARO, IBM Cloud and MCE (self hosted).

- Document the field with the expected behaviour for day 1 and day 2 changes.

- There is e2e test coverage for the feature that include day 2 changes of the field.

- There is e2e test coverage for on creation UX failure expectation via [this e2e test](https://github.com/openshift/hypershift/blob/84fecafa57504139ae6f0623a789369eda05c56f/test/e2e/create_cluster_test.go#L33-L48)

- There is e2e test coverage for day 2 on update UX failure expectations via [this e2e test](https://github.com/openshift/hypershift/blob/d6f79f6cd0a638e07f82b6c57bff6c23a6c8d2c0/test/e2e/util/util.go#L977)

In general we aim to adhere and converge with stand alone principles in [openshift/api](https://github.com/openshift/api)