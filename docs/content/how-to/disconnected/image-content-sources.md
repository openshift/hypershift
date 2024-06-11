# Configuring disconnected workloads in the HostedCluster through ImageContentSources

## What is ImageContentSources and why do we need it?

Users can set up alternative image registry information for their guest clusters to use through a field called `ImageContentSources` in a hosted control plane's specification. Alternatively, this field can also be set through the HyperShift CLI by specifying a filepath to a file containing the source and mirrors for the alternative registry information, `--image-content-sources=/path/to/file/with/sources_and_mirrors.yml`.

Here is an example of the expected format for this field, `ImageContentSources`:
```
- mirrors:
    - brew.registry.redhat.io
  source: registry.redhat.io
- mirrors:
    - brew.registry.redhat.io
  source: registry.xyz.redhat.io
- mirrors:
    - brew.registry.redhat.io
  source: registry-proxy.engineering.redhat.com
```
!!! note

    This is also the expected format for the file if you choose to use the HyperShift CLI flag.

## How ImageContentSources are used in the nodes within a NodePool
`ImageContentSources` are reconciled, through the HostedClusterConfigOperator (HCCO), to either an ImageContentSourcePolicy (ICSP) custom resources (CR) or an ImageDigestMirrorSet (IDMS) CR. The CR is then included in the configuration for the Nodes in a NodePool through the NodePool controller's functions, `reconcile > getConfig > defaultAndValidateConfigManifest`.

!!! important

    ICSPs will be deprecated in future OpenShift releases, likely starting with v4.17. IDMSs are the replacement CR for ICSPs but are only available in OpenShift starting in v4.13.
    The HCCO will automatically delete any ICSPs previously used in the node configuration setup through the Control Plane Operator (CPO) starting in OpenShift v4.13.