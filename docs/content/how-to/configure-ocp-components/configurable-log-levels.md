---
title: Configurable Log Levels for Control Plane Components (Tech Preview)
---

!!! warning "Tech Preview"

    Configurable log levels for control plane components is a Tech Preview feature. It requires the `HCPUserFacingOperatorLogs` feature gate to be enabled via the `TechPreviewNoUpgrade` feature set on the HyperShift Operator. Tech Preview features are not supported in production environments.

## Overview

HyperShift supports configuring log verbosity for hosted control plane components via the `spec.operatorConfiguration` field on the HostedCluster CR. This provides a supported, declarative API for tuning control plane log verbosity — replacing ad-hoc pod-level changes with a standard field on the HostedCluster resource managed by the service provider on the management cluster.

## Prerequisites

1. **Feature gate enabled**: The HyperShift Operator must be installed with the `TechPreviewNoUpgrade` feature set:

    ```bash
    hypershift install --tech-preview-no-upgrade
    ```

2. **Existing HostedCluster**: A running HostedCluster to configure.

## Supported Components

| Component                    | JSON Field                 | Logging Framework | Mechanism                |
|------------------------------|----------------------------|-------------------|--------------------------|
| kube-apiserver               | `kubeAPIServer`            | klog              | `--v=N` container arg    |
| kube-controller-manager      | `kubeControllerManager`    | klog              | `--v=N` container arg    |
| kube-scheduler               | `kubeScheduler`            | klog              | `--v=N` container arg    |
| etcd                         | `etcd`                     | zap               | `ETCD_LOG_LEVEL` env var |
| openshift-apiserver          | `openShiftAPIServer`       | klog              | `--v=N` container arg    |
| openshift-controller-manager | `openShiftControllerManager` | klog            | `--v=N` container arg    |
| openshift-oauth-apiserver    | `openShiftOAuthAPIServer`  | klog              | `--v=N` container arg    |
| oauth-server                 | `oauthServer`              | klog              | `--v=N` container arg    |

## Log Levels

| LogLevel   | klog `--v` | etcd level | Use Case                                     |
|------------|------------|------------|----------------------------------------------|
| Normal     | 2          | info       | Production — standard verbosity              |
| Debug      | 4          | debug      | Troubleshooting                              |
| Trace      | 6          | debug      | Deep investigation                           |
| TraceAll   | 8          | debug      | Full request/response dumps                  |

When no `logLevel` is configured for a component, the Control Plane Operator does not inject any verbosity flag. The component runs with its built-in default (klog default 0 for most components).

!!! warning
    `TraceAll` (klog level 8) can log sensitive data including request bodies, tokens, and secrets. Use only in controlled environments and reset promptly after troubleshooting.

## Setting Log Levels

To increase log verbosity for a specific component, patch the HostedCluster on the management cluster:

```bash
oc patch hostedcluster my-cluster -n clusters --type=merge -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":{"logLevel":"Debug"}}}}'
```

Multiple components can be configured in a single patch:

```bash
oc patch hostedcluster my-cluster -n clusters --type=merge -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":{"logLevel":"Debug"},"openShiftControllerManager":{"logLevel":"Trace"}}}}'
```

Setting the `logLevel` field triggers a rolling restart of the affected component. When `controllerAvailabilityPolicy` is set to `HighlyAvailable` (the default), HA guarantees ensure zero downtime:

- Load-balanced components (kube-apiserver, openshift-apiserver, openshift-oauth-apiserver, oauth-server) run 3 replicas — 2 continue serving while 1 restarts.
- Leader-elected components (kube-controller-manager, kube-scheduler, openshift-controller-manager) run 2 replicas — the standby takes over during restart.
- Etcd runs 3 replicas — Raft quorum is maintained during rolling update.

With `SingleReplica` availability policy, expect brief disruption during the restart.

## Checking Current Configuration

```bash
oc get hostedcluster my-cluster -n clusters -o jsonpath='{.spec.operatorConfiguration}' | jq .
```

## Resetting Log Levels

Remove a specific component's log level override using a JSON patch:

```bash
oc patch hostedcluster my-cluster -n clusters --type=json -p '[{"op":"remove","path":"/spec/operatorConfiguration/kubeAPIServer"}]'
```

Removing the field causes the Control Plane Operator to stop injecting the component-specific log-level setting on the next reconciliation, restoring the component's built-in default.

!!! note
    For kube-apiserver specifically, the built-in default is restored only when neither the `operatorConfiguration` API field nor the deprecated KAS annotation (see below) is present. If the annotation is still set, it continues to control verbosity even after removing the API field.

## KAS Annotation Deprecation

The existing `hypershift.openshift.io/kube-apiserver-verbosity-level` annotation on the HostedCluster is deprecated. Use `spec.operatorConfiguration.kubeAPIServer.logLevel` instead.

During the transition period, both are honored. When both are set, the `operatorConfiguration` API field takes precedence. When only the annotation is present, a deprecation warning condition is surfaced on the HostedCluster. To fully reset kube-apiserver verbosity to its built-in default, remove both the API field and the annotation.

## API Reference

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  operatorConfiguration:
    kubeAPIServer:
      logLevel: Debug            # Normal | Debug | Trace | TraceAll
    kubeControllerManager:
      logLevel: Normal
    kubeScheduler:
      logLevel: Normal
    etcd:
      logLevel: Normal
    openShiftAPIServer:
      logLevel: Normal
    openShiftControllerManager:
      logLevel: Normal
    openShiftOAuthAPIServer:
      logLevel: Normal
    oauthServer:
      logLevel: Normal
```
