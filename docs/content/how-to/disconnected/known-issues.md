# Known Issues

## OLM default catalog sources in ImagePullBackOff state

When you work in a disconnected environment the OLM catalog sources will be still pointing to their original source, so all of these container images will keep it in ImagePullBackOff state even if the OLMCatalogPlacement is set to `Management` or `Guest`. From this point you have some options ahead:

1. Disable those OLM default catalog sources and using the oc-mirror binary, mirror the desired images into your private registry, creating a new Custom Catalog Source.
2. Mirror all the Container Images from all the catalog sources and apply an ImageContentSourcePolicy to request those images from the private registry.

The most practical one is the first choice. To proceed with this option, you will need to follow [these instructions](https://docs.openshift.com/container-platform/4.14/installing/disconnected_install/installing-mirroring-disconnected.html). The process will make sure all the images get mirrored and also the ICSP will be generated properly.

Additionally when you're provisioning the HostedCluster you will need to add a flag to indicate that the OLMCatalogPlacement is set to `Guest` because if that's not set, you will not be able to disable them.