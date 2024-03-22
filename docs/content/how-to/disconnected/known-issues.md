# Known Issues

## OLM default catalog sources in ImagePullBackOff state

When you work in a disconnected environment the OLM catalog sources will be still pointing to their original source, so all of these container images will keep it in ImagePullBackOff state even if the OLMCatalogPlacement is set to `Management` or `Guest`. From this point you have some options ahead:

1. Disable those OLM default catalog sources and using the oc-mirror binary, mirror the desired images into your private registry, creating a new Custom Catalog Source.
2. Mirror all the Container Images from all the catalog sources and apply an ImageContentSourcePolicy to request those images from the private registry.

The most practical one is the first choice. To proceed with this option, you will need to follow [these instructions](https://docs.openshift.com/container-platform/4.14/installing/disconnected_install/installing-mirroring-disconnected.html). The process will make sure all the images get mirrored and also the ICSP will be generated properly.

Additionally when you're provisioning the HostedCluster you will need to add a flag to indicate that the OLMCatalogPlacement is set to `Guest` because if that's not set, you will not be able to disable them.

## Hypershift operator is failing to reconcile in Disconnected environments

If you are operating in a disconnected environment and have deployed the Hypershift operator, you may encounter an issue with the UWM telemetry writer. Essentially, it exposes Openshift deployment data in your RedHat account, but this functionality does not operate in a disconnected environments.

**Symptoms:**

- The Hypershift operator appears to be running correctly in the `hypershift` namespace but even if you creates the Hosted Cluster nothing happens.
- There will be a couple of log entries in the Hypershift operator:

```
{"level":"error","ts":"2023-12-20T15:23:01Z","msg":"Reconciler error","controller":"deployment","controllerGroup":"apps","controllerKind":"Deployment","Deployment":{"name":"operator","namespace":"hypershift"},"namespace":"hypershift","name":"operator","reconcileID":"451fde3c-eb1b-4cf0-98cb-ad0f8c6a6288","error":"cannot get telemeter client secret: Secret \"telemeter-client\" not found","stacktrace":"sigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).reconcileHandler\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:329\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).processNextWorkItem\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:266\nsigs.k8s.io/controller-runtime/pkg/internal/controller.(*Controller).Start.func2.2\n\t/hypershift/vendor/sigs.k8s.io/controller-runtime/pkg/internal/controller/controller.go:227"}

{"level":"debug","ts":"2023-12-20T15:23:01Z","logger":"events","msg":"Failed to ensure UWM telemetry remote write: cannot get telemeter client secret: Secret \"telemeter-client\" not found","type":"Warning","object":{"kind":"Deployment","namespace":"hypershift","name":"operator","uid":"c6628a3c-a597-4e32-875a-f5704da2bdbb","apiVersion":"apps/v1","resourceVersion":"4091099"},"reason":"ReconcileError"}
```

**Solution:**

To resolve this issue, the solution will depend on how you deployed Hypershift:

- **The HO was deployed using ACM/MCE:** In this case you will need to create a ConfigMap in the `local-cluster` namespace (the namespace and ConfigMap name cannot be changed) called `hypershift-operator-install-flags` with this content:

```
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: hypershift-operator-install-flags
  namespace: local-cluster
data:
  installFlagsToRemove: --enable-uwm-telemetry-remote-write
```

- **The HO was deployed using the Hypershift binary:** In this case you will just need to remove the flag `--enable-uwm-telemetry-remote-write` from the hypershift deployment command.
