# Versioning Support
There are different components that might require independent versioning and support level:

- Management Cluster
- API `hypershift.openshift.io`
- HyperShift Operator (HO)
- Control Plane Operator (CPO)
- HyperShift CLI
- Hosted Control Plane (HCP) CLI

## Support Level
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
There are two user facing resources exposed by HyperShift: [HostedClusters and NodePools](https://hypershift-docs.netlify.app/reference/api/).

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
