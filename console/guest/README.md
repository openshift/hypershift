# In-guest-cluster API resources

Resources applied to the **guest** (hosted) cluster's kube-API, not the
management cluster. Everything else under `console/` targets the management
cluster's HCP namespace; these target the guest.

| File | What |
|------|------|
| `consoleclidownloads-crd.yaml` | Verbatim upstream `consoleclidownloads.console.openshift.io` CRD (from `openshift/api`). The guest has the `Console` capability disabled, so this CRD isn't installed by default. |
| `oc-cli-downloads.yaml` | The `oc-cli-downloads` ConsoleCLIDownload CR the console UI's "Command Line Tools" page reads. Hand-applied equivalent of what console-operator would generate, pointing at our control-plane-side downloads server. |

Apply with the guest kubeconfig:

```
./apply.sh
```

(Uses `../hostedcluster/kubeadmin.kubeconfig`.)
