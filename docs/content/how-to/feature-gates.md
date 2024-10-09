# Feature Gates

Feature gates in OpenShift allows to ensure everything knew works together and optimizes for rapid evaluation in CI to promote without ever releasing a public preview.
There are no guarantees that your fleet continues to be operational long term after you enable a feature gate.

In the HCP context, there are multiple non exclusive scenarios where features might need to be gated:

1 - A feature that impacts HO install
E.g. New CRDs are required for CPOv2 (control plane operator version 2)

2 - A feature that impacts the whole cluster fleet / HO
E.g. Introduce fleet wise shared ingress to be validated in targeted environments

3 - A feature that impacts individual clusters
E.g. Introduce using CPOv2 for some HC to develop feedback

4 - A feature that impacts API
E.g. Introduce a new provider like Openstack
E.g. Introduce a new field/feature like AWS tenancy

5 - A feature specific for an OCP component
Components honour existing standalone in-cluster OCP feature gate mechanisim

The currently ongoing implementation of feature gates expose this in the CLI at install time
```
--feature-gates mapStringBool                    A set of key=value pairs that describe feature gates for alpha/experimental features. Options are:
                                                    AllAlpha=true|false (ALPHA - default=false)
                                                    AllBeta=true|false (BETA - default=false)
                                                    OpenStack=true
```

Current implementation uses "k8s.io/component-base/featuregate" to expose feature gates as flags.
In a follow up we plan to introduce support to also signal a feature gate granularly at the HC level.
Eventually support for at least 1, 2, 3 and 4 afromentioned scenarios will most likely converge into a single API.