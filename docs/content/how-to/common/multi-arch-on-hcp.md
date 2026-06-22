# Multi-arch on Hosted Control Planes
## General
Several platforms now support multi-arch capable HostedClusters when a multi-arch release image or stream is used for 
the HostedCluster. This means a HostedCluster can manage NodePools with different CPU architectures. 

!!! note

    An individual NodePool only supports one CPU architecture and cannot support multiple CPU architectures within the 
    same NodePool.

The most up-to-date information on what CPU types are supported on a platform can be found 
by looking at the NodePool controller function to validate NodePool CPU and platform [here](https://github.com/openshift/hypershift/blob/2e7d5e357080c8e8d0f91fac71917723814440cc/hypershift-operator/controllers/nodepool/nodepool_controller.go#L1010).

As of September 2024:

* AWS supports amd64 & arm64 NodePools
* Azure supports amd64 & arm64 NodePools (through ARO HCP only)
* Agent supports amd64, arm64, & ppc64le NodePools

## Multi-arch Validation
### HyperShift Operator
The HyperShift Operator, through the HostedCluster controller, will update a field, HostedCluster.Status.PayloadArch, 
with the payload type of the HostedCluster release image. The valid options for this field are: Multi, ARM64, AMD64, or 
PPC64LE.

When a NodePool is added to a HostedCluster, the HyperShift Operator, through the NodePool controller, will check the 
NodePool.Spec.Arch against the HostedCluster.Status.PayloadArch to ensure the NodePool can be managed by the 
HostedCluster. If HostedCluster.Status.PayloadArch is not `Multi` and it does not exactly match NodePool.Spec.Arch, the 
NodePool controller will block reconciliation of the NodePool and set a status condition on the NodePool CR stating the 
NodePool cannot be supported by the HostedCluster payload type.

### HCP CLI
Create Cluster CLI commands will check to see if a multi-arch release image or stream is being used for the 
HostedCluster payload. If a multi-arch release image or stream is not used, the CLI will check the management cluster 
and NodePool CPU architectures match; if they do not match, the CLI will return an error and stop creating the cluster.

The Create NodePool CLI commands for AWS and Azure will attempt to validate the NodePool CPU architecture against the 
HostedCluster.Status.PayloadArch if the HostedCluster exists. If a HostedCluster doesn't exist, for instance when 
creating a new HostedCluster, a warning message will be displayed stating there was a failure to get the HostedCluster 
to check the payload status. If the HostedCluster.Status.PayloadArch exists and isn't multi or does not match the 
NodePool CPU architecture, the CLI will return an error and stop creating resources.