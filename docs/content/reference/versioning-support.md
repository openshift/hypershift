# Versioning Support
There are different components that might require independent versioning and support level:

- Management Cluster
- API `hypershift.openshift.io`
- HyperShift Operator (HO)
- Control Plane Operator (CPO)
- HyperShift CLI
- Hosted Control Plane (HCP) CLI

## Support Level

### Managed Services
Managed services, such as Red Hat OpenShift on IBM Cloud, control versioning
of all components. Refer to the managed service documentation for the latest,
authoritative support matrix.

#### [Red Hat OpenShift on IBM Cloud](https://cloud.ibm.com/docs/openshift?topic=openshift-openshift_versions)

Red Hat OpenShift on IBM Cloud may support OCP versions beyond standard
OCP timelines. As of September 28, 2025:
- Management cluster (where the HyperShift Operator runs): OpenShift 4.15 or later or Kubernetes 1.30 or later
- HostedClusters created by HyperShift on IBM Cloud: OpenShift 4.14 or later

Information above is subject to change; check IBM Cloud documentation or
contact IBM development.

### Management Cluster
In general, the upstream HyperShift project does not place strict requirements on the OpenShift version of your 
management cluster. This may vary depending on the particular platform; for example, Kubevirt requires management 
clusters with OCP 4.14 and higher.

The HO determines what versions of OCP can be installed through the HostedCluster (HC); see the [HO section](#ho) for 
more details. However, different versions of the HO are thoroughly tested only on a limited set of OpenShift versions, 
and this should inform your deployment decisions.

#### Production Use Cases
For production use & support, it is required to use a downstream product which bundles a supported build of the 
HyperShift Operator. This downstream product is called [Multi-Cluster Engine](https://docs.openshift.com/container-platform/4.16/architecture/mce-overview-ocp.html) (MCE) and it is available through 
OpenShift's OperatorHub. 

MCE versions _do_ require specific OCP versions for the Management Cluster to remain in a supported state. 
Each version documents its own support matrix. For example, 

- [MCE 2.5](https://access.redhat.com/articles/7056007)
- [MCE 2.4](https://access.redhat.com/articles/7027079)

As a heuristic, a new release of MCE will run on:

- The latest, yet to be released version of OpenShift
- The latest GA version of OpenShift
- Two versions prior to the latest GA version

Versions of MCE can also be obtained with the Advanced Cluster Management (ACM) offering. If you are running ACM, refer 
to product documentation to determine the bundled MCE version.

The full list of HostedCluster OCP versions that can be installed via the HO on a Management Cluster will depend on the 
version of the installed HO. However, if you are running a tested configuration or MCE, this list will always include at 
least (a) the same OCP version as the Management Cluster and (b) Two previous minor versions relative to the Management 
Cluster. For example, if the Management Cluster is running 4.16 and a supported version of MCE, then the HO will at 
least be able to install 4.16, 4.15, and 4.14 Hosted Clusters. See the Multi-Cluster Engine section, under the expanded 
section titled "OpenShift Advanced Cluster Management" on [this page](https://access.redhat.com/support/policy/updates/openshift_operators) for more details. 

### API
There are two user facing resources exposed by HyperShift: [HostedClusters and NodePools](https://hypershift.pages.dev/reference/api/).

The HyperShift API version policy generally aligns with the [Kubernetes API versioning](https://kubernetes.io/docs/reference/using-api/#api-versioning).

### HO
The upstream HyperShift project does not release new versions aligned with the OpenShift release cadence. New versions 
of the HO are periodically tagged from the `main` branch. These versions are tested and consumed by internal Red Hat 
managed services, and you can use these versions directly. However, for supported production use, you should use a 
supported version of MCE.

The HO is tagged at particular commits as part of merging new HO versions for Red Hat managed services; there is no 
particular tagging scheme for this effort.

A list of the tags can be found [here](https://github.com/openshift/hypershift/tags).

Once installed, the HO creates a ConfigMap called `supported-versions` into the Hypershift namespace, which describes 
the HostedClusters supported versions that could be deployed. 

Here is an example `supported-versions` ConfigMap:
```
apiVersion: v1
data:
    server-version: 2f6cfe21a0861dea3130f3bed0d3ae5553b8c28b
    supported-versions: '{"versions":["4.17","4.16","4.15","4.14"]}'
kind: ConfigMap
metadata:
    creationTimestamp: "2024-06-20T07:12:31Z"
    labels:
        hypershift.openshift.io/supported-versions: "true"
    name: supported-versions
    namespace: hypershift
    resourceVersion: "927029"
    uid: f6336f91-33d3-472d-b747-94abae725f70
```

!!! important

        You cannot install HCs higher than what the HO supports. In the example above, HCs using images greater than 
        4.17 cannot be created.

### CPO
The CPO is released as part of each OCP payload release image. You can find those release images here:

- [amd64](https://amd64.ocp.releases.ci.openshift.org/)
- [arm64](https://arm64.ocp.releases.ci.openshift.org/)
- [multi-arch](https://multi.ocp.releases.ci.openshift.org/)

### HyperShift CLI
The HyperShift CLI is a helper utility used only for development and testing purposes. No compatibility policies are 
guaranteed.

It helps create required infrastructure needed for a HostedCluster CR and NodePool CR to successfully install. 

#### Showing General Version Information
Running the following command will show what the latest OCP version the CLI supports against your KUBECONFIG:
```
% hypershift version
Client Version: openshift/hypershift: 1ed535a8d27c5a1546f1ff4cc71abf32dd1a26aa. Latest supported OCP: 4.17.0
Server Version: 2f6cfe21a0861dea3130f3bed0d3ae5553b8c28b
Server Supports OCP Versions: 4.17, 4.16, 4.15, 4.14
```

#### Showing HyperShift CLI Version Information
Running the following command will show the commit sha the HyperShift CLI was built from:
```
 % hypershift version --commit-only
211a8536809aca94d6047c057866be54d96777c5
```

### HCP CLI
The HCP CLI is the productized version of the HyperShift CLI. This CLI is available through download from MCE.

Similar to the HyperShift CLI, running the `hcp version` command will show the latest OCP version the CLI supports
against your KUBECONFIG. Running the `hcp version --commit-only` will show the commit sha the HCP CLI was built from.

## HostedCluster and NodePool Version Compatibility

HyperShift validates version compatibility between HostedClusters and their NodePools. While the system reports version incompatibilities, existing NodePools with unsupported version skews will continue to operate without guarantees of stability or support.

### Version Skew Policy

The following rules apply to NodePool version compatibility:

1. **NodePool version cannot be higher than the HostedCluster version**
   - A NodePool must run the same version or an older version than its HostedCluster
   - This applies to both minor and patch versions (e.g., NodePool 4.18.6 is not allowed with HostedCluster 4.18.5)

2. **N-3 version skew support**
   - For all 4.y OpenShift versions, NodePools can run up to 3 minor versions behind the HostedCluster
   - For example, a 4.18 HostedCluster supports NodePools running 4.18, 4.17, 4.16, and 4.15

### Examples

**HostedCluster version 4.18.5:**

Supported NodePool versions:
- Same minor, same or lower patch: `4.18.5`, `4.18.4`, `4.18.3`, etc.
- N-1 minor (any patch): `4.17.z` (e.g., `4.17.0`, `4.17.10`, `4.17.25`)
- N-2 minor (any patch): `4.16.z` (e.g., `4.16.0`, `4.16.15`)
- N-3 minor (any patch): `4.15.z` (e.g., `4.15.0`, `4.15.20`)

Unsupported NodePool versions:
- Higher patch in same minor: `4.18.6`, `4.18.10` (NodePool patch cannot exceed HostedCluster patch)
- Higher minor version: `4.19.0`, `4.19.z`, `4.20.0` and above
- Beyond N-3 minor version: `4.14.z`, `4.13.z` and below 

### Version Compatibility Validation

The NodePool controller automatically validates version compatibility and reports the status through the `SupportedVersionSkew` condition:

- **Status: True** - NodePool version is compatible with the HostedCluster version
- **Status: False** - Version incompatibility detected; the condition message provides details about the specific incompatibility

**Behavior when incompatibility is detected:**

- **Existing NodePools**: Will continue to operate despite the incompatibility, but without stability or support guarantees. The condition serves as a warning to administrators.
- **CLI (`hypershift` command)**: Will prevent creation of new NodePools with incompatible versions by returning an error.

**Recommendations:**

- Monitor the `SupportedVersionSkew` condition regularly to identify version compatibility issues
- When upgrading HostedClusters, verify that all NodePools remain within the supported version skew window
- If a NodePool becomes incompatible, upgrade it to a compatible version as soon as operationally feasible
- When creating new NodePools, the CLI will enforce compatibility checks to prevent misconfiguration
