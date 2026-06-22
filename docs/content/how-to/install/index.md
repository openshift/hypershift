# HyperShift Operator Installation

This document describes different installation flags or methods for HyperShift Operator (HO).

## Limiting the CAPI CRDs installed
The HO uses the Cluster API (CAPI) to manage the nodes in the NodePool. By default, the HO installation will install all 
CAPI related CRDs. If you want to limit the CRDs installed, you can set the `--limit-crd-install` flag to a 
comma-separated list of CRDs to install. The valid values for this flag are: AWS, Azure, IBMCloud, KubeVirt, Agent, 
OpenStack.

For example, to only install the AWS and Azure related CAPI CRDs, you would use 
the following flag in your HO install command:

```bash
--limit-crd-install=AWS,Azure
```

!!! important

    Limiting the CAPI CRDs installed means the HO will only be able to manage HostedClusters of the same platform.
    For example, in the above example, if you limit the CRDs to AWS and Azure, the HO will only be able to manage 
    AWS and Azure HostedClusters.