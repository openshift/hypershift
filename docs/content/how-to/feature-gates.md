# Feature Gates

## :material-information-outline: Overview

HyperShift has two separate feature gate systems that serve different purposes. Understanding which system applies is essential for correctly enabling and testing features.

| System | Scope | Set by |
|--------|-------|--------|
| **Management cluster feature gates** | CRD schemas, HyperShift Operator, Control Plane Operator | `hypershift install --tech-preview-no-upgrade` |
| **Hosted Cluster feature gates** | OCP release payload for a single tenant | `spec.configuration.featureGate.featureSet` on the HostedCluster |

!!! warning

    These two systems are independent. Enabling a management cluster feature gate does **not** enable OCP feature gates inside hosted clusters, and vice versa.

## :material-server-network: Management Cluster Feature Gates

Management cluster feature gates govern the HyperShift infrastructure running on the management cluster. There are no guarantees that your fleet continues to be operational long-term after you enable these feature gates.

These gates apply to the following scenarios:

- A feature that impacts HyperShift Operator install (e.g. new CRDs are required for CPOv2)
- A feature that impacts the whole cluster fleet (e.g. fleet-wide shared ingress to be validated in targeted environments)
- A feature that impacts individual clusters (e.g. introduce using CPOv2 for some HostedClusters)
- A feature that impacts the HyperShift API (e.g. introduce a new provider like OpenStack, or a new field like AWS tenancy)

All management cluster feature gates are grouped under a single `TechPreviewNoUpgrade` feature set. To enable them, pass `--tech-preview-no-upgrade` at install time:

```bash
hypershift install --tech-preview-no-upgrade
```

This flag determines which CRD variants are installed (Default vs TechPreviewNoUpgrade) and configures the `HYPERSHIFT_FEATURESET` environment variable that both the HyperShift Operator and Control Plane Operator read.

### Adding a Management Cluster Feature Gate

We rely on [openshift/api](https://github.com/openshift/api) tooling for generating CRDs with [openshift markers](https://github.com/openshift/kubernetes-sigs-controller-tools/blob/96a305393cb22f0c69c4ee59be27ad09057cc704/pkg/crd/markers/patch_validation.go#L30-L36). See [this PR](https://github.com/openshift/hypershift/pull/8675) as an example of adding an API field behind a feature gate.

The controller business logic uses `k8s.io/component-base/featuregate`. This enables devs to declare [granular gates for their features](https://github.com/openshift/hypershift/blob/9f5ccaef47cdcf9d2df91134571f1783e99e30fe/hypershift-operator/featuregate/feature.go). See [this PR](https://github.com/openshift/hypershift/pull/8976) as an example.

## :material-cloud-outline: Hosted Cluster Feature Gates

Hosted Cluster feature gates control the OCP release payload for a specific hosted cluster. OCP components in the hosted control plane honor the standard in-cluster OCP feature gate mechanism. This applies to:

- A feature specific to an OCP component (e.g. TLSAdherence, DynamicResourceAllocation)

To enable a feature set for a hosted cluster, set it in `spec.configuration.featureGate.featureSet`:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
spec:
  configuration:
    featureGate:
      featureSet: TechPreviewNoUpgrade
```

!!! warning

    Enabling `TechPreviewNoUpgrade` is irreversible and prevents minor-version upgrades on the hosted cluster. Use this only on test clusters where future upgrades are not required.

The `featuregate-generator` job in the hosted control plane namespace renders the OCP payload's feature gates into a `feature-gate` ConfigMap that OCP components consume.

!!! note

    If you are an OCP component team looking to test a feature gate like `TLSAdherence` or `DynamicResourceAllocation`, this is the mechanism you need — set the feature set on the HostedCluster, not on the management cluster.

### Example: Enabling TLSAdherence

The `TLSAdherence` feature gate is an OCP feature gate that is part of the `TechPreviewNoUpgrade` feature set. To enable it on a hosted cluster:

1. Set `TechPreviewNoUpgrade` as the feature set on the HostedCluster:

    ```yaml
    spec:
      configuration:
        featureGate:
          featureSet: TechPreviewNoUpgrade
    ```

2. Verify the feature gate was rendered by checking the ConfigMap in the hosted control plane namespace:

    ```bash
    oc get configmap feature-gate -n <hcp-namespace> -o yaml
    ```

    !!! tip

        The hosted control plane namespace is typically `<clusters-namespace>-<hostedcluster-name>`.

## Promoting a Feature Gated API Field

Generally speaking any new field should start by being feature-gated.
The minimum criteria for promotion is:

- Provide clear context and analysis on the PR about how the field might impact the different GA products. This includes but is not limited to ROSA, ARO, IBM Cloud and MCE (self-hosted).

- Document the field with the expected behaviour for day 1 and day 2 changes.

- There is e2e test coverage for the feature that include day 2 changes of the field.

- There is e2e test coverage for on creation UX failure expectation via [this e2e test](https://github.com/openshift/hypershift/blob/84fecafa57504139ae6f0623a789369eda05c56f/test/e2e/create_cluster_test.go#L33-L48)

- There is e2e test coverage for day 2 on update UX failure expectations via [this e2e test](https://github.com/openshift/hypershift/blob/d6f79f6cd0a638e07f82b6c57bff6c23a6c8d2c0/test/e2e/util/util.go#L977)

In general we aim to adhere and converge with stand-alone principles in [openshift/api](https://github.com/openshift/api)
