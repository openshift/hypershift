Once the mirroring process is complete, you will have two main objects that need to be applied in the Management Cluster:

1. ICSP (Image Content Source Policies) or IDMS (Image Digest Mirror Set).
2. Catalog Sources.

Using the `oc-mirror` tool, the output artifacts will be located in a new folder called `oc-mirror-workspace/results-XXXXXX/`.

ICSP/IDMS will trigger a "special" MachineConfig change that will not reboot your nodes but will reboot the kubelet on each of them.

Once all nodes are schedulable and marked as `READY`, you will need to apply the new catalog sources generated.

The catalog sources will trigger some actions in the `openshift-marketplace operator`, such as downloading the catalog image and processing it to retrieve all the `PackageManifests` included in that image. You can check the new sources by executing `oc get packagemanifest` using the new CatalogSource as a source.

## Applying the Artifacts

First, we need to create the ICSP/IDMS artifacts:


```bash
oc apply -f oc-mirror-workspace/results-XXXXXX/imageContentSourcePolicy.yaml
```

Now, wait for the nodes to become ready again and execute the following command:

```bash
oc apply -f catalogSource-XXXXXXXX-index.yaml
```