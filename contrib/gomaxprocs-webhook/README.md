gomaxprocs-webhook
===================

Overview
--------

This webhook sets the `GOMAXPROCS` environment variable on containers that run in hosted control plane namespaces managed by HyperShift. It targets only namespaces labeled with `hypershift.openshift.io/hosted-control-plane: "true"` and only mutates Pod CREATE admissions.

Behavior:
- Does not override an existing `GOMAXPROCS` env var in a container.
- Computes the top-level owner for context (e.g., follows ReplicaSet → Deployment, Job → CronJob) to match configuration overrides accurately.
- Uses a YAML configuration (mounted from a ConfigMap) to determine a default value, per-workload/container overrides, and explicit exclusions.
- If a container is marked as excluded, no injection occurs.
- If no match is found and no default is defined in the config, an optional process-wide default can be provided via `--default`.
- Failure policy for the webhook is `Ignore`; if the webhook is unavailable, Pod creation proceeds without mutation.


Installation
------------

Prerequisites:
- An OpenShift management cluster running HyperShift and an existing `hypershift` Namespace

1) Build and push the image (optional)

If you want to build your own image:

```bash
podman build -t <your-registry>/<your-namespace>/gomaxprocs-webhook:latest contrib/gomaxprocs-webhook
podman push <your-registry>/<your-namespace>/gomaxprocs-webhook:latest
```

2) Point the manifests at your image

Edit `contrib/gomaxprocs-webhook/manifests/kustomization.yaml` and update the `images:` section to reference your image name and tag.
You can do this with the following command:
`cd contrib/gomaxprocs-webhook/manifests && kustomize edit set image gomaxprocs-webhook=<your-image-pull-spec>`

3) Edit the configuration
   You can edit the initial configuration in `contrib/gomaxprocs-webhook/manifests/config.yaml`
   See the configuration section below.

3) Deploy with kustomize

```bash
oc apply -k contrib/gomaxprocs-webhook/manifests
```

This installs:
- ServiceAccount, ClusterRole, and ClusterRoleBinding
- Service with serving cert annotations
- Deployment for the webhook
- `MutatingWebhookConfiguration` targeting namespaces labeled as hosted control planes
- ConfigMap `gomaxprocs-webhook` holding the configuration

4) Verify

```bash
kubectl -n hypershift get deploy,svc,configmap
kubectl get mutatingwebhookconfiguration gomaxprocs-webhook -o yaml | grep namespaceSelector -A3
```

Uninstall
---------

```bash
kubectl delete -k contrib/gomaxprocs-webhook/manifests
```

Note: The `hypershift` Namespace is not managed by these manifests and will not be deleted.


Configuration
-------------

The webhook reads configuration from a file mounted at `/etc/config/config.yaml` (provided by the `gomaxprocs-webhook` ConfigMap). The schema:

```yaml
default: "<string>"     # Optional default GOMAXPROCS for all matched containers
overrides:               # Optional per-workload/container overrides
  - workloadKind: Deployment
    workloadName: some-deployment
    containerName: some-container   # exact match
    value: "<string>"
  - workloadKind: Deployment
    workloadName: other-deployment
    containerName: "*"              # wildcard: applies to all containers in the workload
    value: "<string>"
exclusions:              # Optional explicit exclusions (take precedence)
  - workloadKind: Deployment
    workloadName: excluded-deployment
    containerName: excluded-container  # exact match
  - workloadKind: StatefulSet
    workloadName: excluded-statefulset
    containerName: "*"                # wildcard: exclude all containers in the workload
```

workloadKind values:
- Deployment
- StatefulSet
- DaemonSet
- Job (if not owned by a CronJob)
- CronJob (when the Job is owned by a CronJob)
- ReplicaSet (rare; when not owned by a Deployment)
- Pod (only when the Pod has no owner)

Notes:
- `workloadKind` is matched case-insensitively. `workloadName` is matched exactly. `containerName` supports exact match and the wildcard `"*"`.
- Matching precedence within a workload:
  - Exact `containerName` match beats wildcard `"*"`.
  - Exclusions win over overrides and defaults.
- If an override has an empty `value`, it inherits the `default` (if present).
- If neither the config `default` nor a matching override provides a value, the server falls back to the process `--default` flag when set.
- The binary validates that the configuration file exists and is readable at startup. If you intentionally want to run without a config file, set `--config-path` to empty and rely on `--default`.

Example configuration (the default manifests include a sample `config.yaml`):

```yaml
default: "32"
overrides:
- workloadKind: Deployment
  workloadName: kube-apiserver
  containerName: kube-apiserver
  value: "20"
exclusions:
- workloadKind: Deployment
  workloadName: oauth-openshift
  containerName: oauth-openshift
```

Live reload behavior
--------------------

The server reads the configuration file from disk on demand with a small throttle window (≈1s). When you update the ConfigMap, the mounted file changes and the new configuration is picked up automatically without restarting the Deployment.


Scope and opting out
--------------------

- The webhook only mutates Pods in namespaces labeled with `hypershift.openshift.io/hosted-control-plane: "true"`.
- To opt out at container granularity, either:
  - Set `GOMAXPROCS` explicitly on that container, or
  - Add an entry under `exclusions:` for the container.


CLI flags
---------

These are the most relevant flags for the `serve` command (see `cmd/serve.go` for full list and defaults):

- `--metrics-bind-address` (default `:8080`): Metrics endpoint bind address.
- `--health-probe-bind-address` (default `:8081`): Liveness/readiness probe bind address.
- `--cert-dir` (default `/var/run/secrets/serving-cert`): Directory containing TLS cert/key for the webhook server.
- `--port` (default `9443`): Webhook server port.
- `--config-path` (default `/etc/config/config.yaml`): Path to the YAML configuration file.
- `--default` (no default): Fallback `GOMAXPROCS` value when not specified in the config.
- `--log-dev` (default `false`): Enable development logging.
- `--log-level` (default `0`): Verbosity level (0=info, 1=verbose, 2=debug).



Testing locally in a labeled namespace
-------------------------------------

```bash
kubectl create ns hcp-test
kubectl label ns hcp-test hypershift.openshift.io/hosted-control-plane=true

cat <<'EOF' | kubectl -n hcp-test apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: go-sleeper
spec:
  containers:
  - name: sleeper
    image: registry.access.redhat.com/ubi9/ubi-minimal
    command: ["bash", "-c", "echo GOMAXPROCS=$GOMAXPROCS; sleep 3600"]
EOF

kubectl -n hcp-test get pod go-sleeper -o jsonpath='{.spec.containers[0].env}' | jq .
```

You should see `GOMAXPROCS` injected according to your configuration.



