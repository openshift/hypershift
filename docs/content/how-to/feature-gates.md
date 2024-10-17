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
The currently ongoing implementation of feature gates relies on "k8s.io/component-base/featuregate" to enable devs to declare [granular gates for their features](https://github.com/openshift/hypershift/blob/9f5ccaef47cdcf9d2df91134571f1783e99e30fe/hypershift-operator/featuregate/feature.go).