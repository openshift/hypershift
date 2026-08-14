# GCP E2E Test Artifacts (v2 Framework)

This document describes the artifact directory structure produced by the `e2e-v2-gke` workflow.

## Top-Level Files

```
<run-id>/
├── build-log.txt        # main CI operator execution log
├── clone-log.txt        # human-readable git clone and PR merge output (presubmit only)
├── clone-records.json   # machine-readable clone details: commands, durations, PR metadata (presubmit only)
├── finished.json        # job completion: result (SUCCESS/FAILURE), timestamp, commit SHAs
├── podinfo.json         # full Kubernetes Pod spec of the CI pod
├── prowjob.json         # full ProwJob CR: job name, refs, spec, labels
├── prowjob_junit.xml    # single JUnit test: "Job run should complete before timeout"
├── sidecar-logs.json    # Prow sidecar: secret censoring and artifact upload logs
├── started.json         # job start: timestamp, PR number, repos and commit SHAs
└── artifacts/           # all CI step artifacts and build outputs
```

## `artifacts/`

```
artifacts/
├── ci-operator-metrics.json    # step durations and success/failure events per step
├── ci-operator-step-graph.json # full step graph: names, dependencies, timing, K8s manifests
├── ci-operator.log             # CI operator execution log
├── junit_operator.xml          # JUnit results for all step graph steps
├── metadata.json               # repo, PR, commit SHAs, pod name, work namespace
├── build-logs/                 # binary compilation logs (presubmit only)
│   ├── hypershift-amd64.log
│   ├── hypershift-cli-amd64.log
│   ├── hypershift-operator-amd64.log
│   ├── hypershift-tests-amd64.log
│   └── src-amd64.log
├── build-resources/            # K8s resources created during the build phase
│   ├── builds.json             # OpenShift Build objects
│   ├── events.json             # Kubernetes events
│   ├── imagestreams.json       # ImageStream pipeline tags
│   └── pods.json               # build pods
├── release/                    # resolved release payload (periodic only)
│   └── artifacts/
│       └── release-images-latest  # ImageStream with all component image SHAs for this run
└── e2e-v2-gke/                 # all step artifacts for the e2e-v2-gke workflow
```

## `artifacts/e2e-v2-gke/`

The workflow runs 13 explicit steps. Each step directory contains `build-log.txt`, `finished.json`, and `sidecar-logs.json`. Steps with additional artifacts are noted below.

```
e2e-v2-gke/
├── ipi-install-rbac/  # grants image-puller RBAC to the CI pod
├── hypershift-gcp-gke-prerequisites/  # installs CRDs (prometheus-operator, Route, DNSEndpoint)
├── hypershift-gcp-gke-provision/  # creates ephemeral GCP projects, GKE Autopilot cluster
├── hypershift-install/  # installs HyperShift operator with GCP support
├── hypershift-gcp-control-plane-setup/  # creates operator GCP SA, configures WIF for PSC and ExternalDNS
├── hypershift-gcp-hosted-cluster-setup/  # generates RSA keypair, creates WIF pool/SAs, HC network
├── create-hostedcluster/  # runs `hypershift create cluster gcp`
│   └── artifacts/
│       └── junit_hosted_cluster.xml
├── tests/  # runs Ginkgo v2 test suite
│   └── artifacts/
│       └── junit_report.xml
├── dump/  # runs `hypershift dump cluster`, collects cluster state
│   └── artifacts/
│       ├── hypershift-dump.tar
│       ├── aggregated-discovery-api.yaml
│       ├── aggregated-discovery-apis.yaml
│       ├── event-filter.html
│       ├── timestamp
│       ├── cluster-scoped-resources/
│       │   └── core/
│       │       └── nodes/
│       │           └── <node-name>.yaml
│       ├── hostedcluster-<hc-name>/  # guest cluster dump (worker node logs + all guest namespaces)
│       │   ├── aggregated-discovery-api.yaml
│       │   ├── aggregated-discovery-apis.yaml
│       │   ├── event-filter.html
│       │   ├── timestamp
│       │   ├── worker.nodes.log      # journal logs from worker nodes
│       │   ├── cluster-scoped-resources/
│       │   └── namespaces/           # guest namespaces (kube-system, openshift-*, etc.)
│       └── namespaces/
│           ├── clusters/
│           │   ├── core/
│           │   │   └── configmaps/
│           │   └── hypershift.openshift.io/
│           │       ├── hostedclusters/
│           │       └── nodepools/
│           ├── clusters-<hc-name>/
│           │   ├── apps/
│           │   │   ├── deployments/
│           │   │   ├── replicasets/
│           │   │   └── statefulsets/
│           │   ├── batch/
│           │   ├── cluster.x-k8s.io/
│           │   │   ├── clusters/
│           │   │   ├── machinedeployments/
│           │   │   ├── machines/
│           │   │   └── machinesets/
│           │   ├── core/
│           │   │   ├── configmaps/
│           │   │   ├── endpoints/
│           │   │   ├── events/
│           │   │   ├── persistentvolumeclaims/
│           │   │   ├── pods/
│           │   │   │   └── logs/     # one log file per container
│           │   │   └── services/
│           │   ├── hypershift.openshift.io/
│           │   │   ├── controlplanecomponents/
│           │   │   └── hostedcontrolplanes/
│           │   ├── monitoring.coreos.com/
│           │   ├── networking.k8s.io/
│           │   ├── policy/
│           │   └── route.openshift.io/
│           └── hypershift/
│               ├── apps/
│               │   ├── deployments/
│               │   └── replicasets/
│               ├── core/
│               │   ├── configmaps/
│               │   ├── endpoints/
│               │   ├── events/
│               │   ├── pods/
│               │   └── services/
│               └── monitoring.coreos.com/
│                   ├── podmonitors/
│                   └── servicemonitors/
├── hypershift-k8sgpt/  # K8sGPT AI-powered analysis per namespace
│   └── artifacts/
│       ├── hostedcluster/
│       │   └── result.json
│       └── namespaces/
│           ├── clusters/
│           │   └── result.json
│           ├── clusters-<hc-name>/
│           │   └── result.json
│           └── hypershift/
│               └── result.json
├── hypershift-debug/  # generates quick-access debug links
│   └── artifacts/
│       └── custom-link-tools.html
├── hypershift-gcp-gke-deprovision/  # deletes hosted-cluster and control-plane GCP projects
└── destroy/  # runs `hypershift destroy cluster gcp`
```

## Quick Navigation Index

### Key Resources

| What you're looking for | Path |
|-------------------------|------|
| Guest cluster dump | `artifacts/e2e-v2-gke/dump/artifacts/hostedcluster-<hc-name>/` |
| Guest cluster namespaces | `artifacts/e2e-v2-gke/dump/artifacts/hostedcluster-<hc-name>/namespaces/` |
| Worker node logs | `artifacts/e2e-v2-gke/dump/artifacts/hostedcluster-<hc-name>/worker.nodes.log` |
| Control plane pod logs | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters-<hc-name>/core/pods/logs/` |
| HostedCluster CR | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters/hypershift.openshift.io/hostedclusters/` |
| NodePool CR | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters/hypershift.openshift.io/nodepools/` |
| HostedControlPlane CR | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters-<hc-name>/hypershift.openshift.io/hostedcontrolplanes/` |
| Control plane pods | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters-<hc-name>/core/pods/` |
| Control plane deployments | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters-<hc-name>/apps/deployments/` |
| CAPI machines | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/clusters-<hc-name>/cluster.x-k8s.io/machines/` |
| HyperShift operator pods | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/hypershift/core/pods/` |
| HyperShift operator deployments | `artifacts/e2e-v2-gke/dump/artifacts/namespaces/hypershift/apps/deployments/` |
| K8sGPT analysis (control plane) | `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/namespaces/clusters-<hc-name>/result.json` |
| K8sGPT analysis (operator) | `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/namespaces/hypershift/result.json` |
| Cluster creation result | `artifacts/e2e-v2-gke/create-hostedcluster/artifacts/junit_hosted_cluster.xml` |
| Test results | `artifacts/e2e-v2-gke/tests/artifacts/junit_report.xml` |

> `<hc-name>` is the HostedCluster name (e.g. `gcp-hc-2e2dd848`), making the full namespace `clusters-<hc-name>`. The actual value is visible in the `artifacts/e2e-v2-gke/dump/artifacts/namespaces/` directory listing.

## Test Results

| File | Description |
|------|-------------|
| `artifacts/e2e-v2-gke/create-hostedcluster/artifacts/junit_hosted_cluster.xml` | Cluster creation result |
| `artifacts/e2e-v2-gke/tests/artifacts/junit_report.xml` | Full Ginkgo v2 test results |
| `artifacts/junit_operator.xml` | CI operator-level results |

## K8sGPT Analysis

The `hypershift-k8sgpt` step produces AI-powered analysis per scope:

| File | Scope |
|------|-------|
| `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/hostedcluster/result.json` | HostedCluster |
| `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/namespaces/clusters/result.json` | `clusters` namespace |
| `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/namespaces/clusters-<hc-name>/result.json` | Control plane namespace |
| `artifacts/e2e-v2-gke/hypershift-k8sgpt/artifacts/namespaces/hypershift/result.json` | `hypershift` namespace |
