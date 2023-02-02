# Versioning support

There are different components that might require independent versioning and support level:

- API `hypershift.openshift.io`.
- HyperShift Operator (HO).
- Control Plane Operator (CPO).
- HyperShift CLI.

## Support level

### HO

- A HO is intended to be released for each OCP minor release.
- A HO released for OCP N minor release must support HostedClusters with a release of N, N-1 and N-2 OCP minor releases.
- A HO must be updated before future OCP minor release can be deployed or updated. For example, a HO released for OCP N minor release does not support future guest cluster N+1 minor releases. A HO must be updated to match the N+1 release before guest clusters can be deployed or updated to N+1.

### CPO

- A CPO is released as part of each [OCP payload release](https://amd64.ocp.releases.ci.openshift.org/).

### CLI

- A CLI is intended to be released as part of every HO release.
- A CLI is only guaranteed to be compatible with the HO release it is tied to. For example, CLI compatiblity with N-1 and N+1 HO minor releases are not guaranteed.

### API

- There are two user facing resources exposed by HyperShift: [HostedClusters and NodePools](https://hypershift-docs.netlify.app/reference/api/).
- The HyperShift API version policy generally aligns with the [Kubernetes API versioning](https://kubernetes.io/docs/reference/using-api/#api-versioning).
