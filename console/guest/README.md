# In-guest-cluster API resources

Resources applied to the **guest** (hosted) cluster's kube-API, not the
management cluster. Everything else under `console/` targets the management
cluster's HCP namespace; these target the guest.

| File | What |
|------|------|
| `oc-cli-downloads.yaml` | The `oc-cli-downloads` ConsoleCLIDownload CR the console UI's "Command Line Tools" page reads. Hand-applied equivalent of what console-operator would generate, pointing at our control-plane-side downloads server. Still hand-applied because the console-operator (which normally creates it) is stripped; other CLI-download CRs (helm, netobserv) come from their own operators/CVO. |

The `consoleclidownloads.console.openshift.io` CRD is no longer carried here: with the
`Console` capability enabled, CVO installs it automatically (it carries
`capability.openshift.io/name: Console`).

Apply with the guest kubeconfig:

```
./apply.sh
```

(Uses `../hostedcluster/kubeadmin.kubeconfig`.)
