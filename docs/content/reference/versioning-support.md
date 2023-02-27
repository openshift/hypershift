# Versioning support

There are different components that might require independent versioning and support level:

- API `hypershift.openshift.io`.
- HyperShift Operator (HO).
- Control Plane Operator (CPO).
- HyperShift CLI.

## Support level

### HO

- A HO is intended to be released for each OCP release.
- A HO released for OCP N minor release must support N, N-1 and N-2 OCP minor releases.
- A HO release if anything, will only do best effort to support future OCP versions.
- The HO, once installed, will create a ConfigMap called `supported-versions` into the Hypershift namespace which describes the HostedClusters supported versions that could be deployed. This is how it looks like:
    ```
    apiVersion: v1
    data:
      supported-versions: '{"versions":["4.13","4.12","4.11"]}'
    kind: ConfigMap
    metadata:
      labels:
        hypershift.openshift.io/supported-versions: "true"
      name: supported-versions
      namespace: hypershift
    ```

### CPO

- A CPO is released as part of each [OCP payload release](https://amd64.ocp.releases.ci.openshift.org/).

### CLI

- A CLI is intended to be released as part of any HO release.
- The CLI is a helper utility for dev purposes. No compatibility policies are guaranteed.

### API

- There are two user facing resources exposed by HyperShift: [HostedClusters and NodePools](https://hypershift-docs.netlify.app/reference/api/).
- The HyperShift API version policy generally aligns with the [Kubernetes API versioning](https://kubernetes.io/docs/reference/using-api/#api-versioning).
