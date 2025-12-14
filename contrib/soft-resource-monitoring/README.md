# Kubernetes Workload Resource Monitoring Tool

A simple tool to monitor resource usage of Kubernetes workloads and get optimization recommendations.

## What it does

Monitors CPU and memory usage of any Kubernetes workload (Deployment, DaemonSet, StatefulSet, Job, etc.) and provides resource request recommendations based on real usage data.

## Usage

```bash
./monitor-workload-resources.sh [OPTIONS] <WORKLOAD_NAME>
```

### Examples

```bash
# Monitor a Deployment for 5 minutes
./monitor-workload-resources.sh my-app

# Monitor a DaemonSet
./monitor-workload-resources.sh -t daemonset global-pull-secret-syncer -n kube-system

# Monitor for 10 minutes with custom interval
./monitor-workload-resources.sh -t deployment my-app -d 600 -i 10
```

### Options

- `-t, --type`: Workload type (deployment, daemonset, statefulset, job, cronjob, replicaset, pod)
- `-n, --namespace`: Kubernetes namespace
- `-d, --duration`: Monitoring duration in seconds (default: 300)
- `-i, --interval`: Sample interval in seconds (default: 5)
- `-l, --label`: Use label selector instead of workload name

## Results

The tool creates a directory with:

- **`resources.csv`**: Raw monitoring data
- **`summary.txt`**: Analysis report with recommendations

### Sample output:

```
CPU Usage (millicores):
  Average: 27m
  Maximum: 45m
  Minimum: 15m

Memory Usage (MB):
  Average: 128Mi
  Maximum: 156Mi
  Minimum: 98Mi

Resource Recommendations:
  Conservative CPU Request: 47m
  Conservative Memory Request: 161Mi
  Optimized CPU Request: 28m
  Optimized Memory Request: 131Mi
```

Use these recommendations to set appropriate resource requests in your workload manifests.

## Requirements

- `kubectl` or `oc` CLI tools
- Metrics server running in the cluster