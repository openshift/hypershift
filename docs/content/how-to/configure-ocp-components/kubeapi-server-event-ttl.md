---
title: Configuring the kube-apiserver Event TTL
---

# Configuring the kube-apiserver Event TTL

## Overview

The kube-apiserver stores Kubernetes Events in etcd and deletes them once they exceed the event
TTL. Hosted control planes use a 3 hour TTL by default, matching standalone OpenShift.

In event-heavy hosted clusters — CI/CD, GitOps, and pipeline workloads in particular — accumulated
events consume a meaningful share of etcd storage. Lowering the TTL reduces that storage without
affecting any other cluster behaviour.

You configure the TTL per hosted cluster with the `hypershift.openshift.io/event-ttl-minutes`
annotation on the HostedCluster.

!!! note

    This follows the same annotation pattern as
    `hypershift.openshift.io/kube-apiserver-goaway-chance`. HyperShift has no KubeAPIServer
    operator, so the standalone OpenShift `eventTTLMinutes` field on the KubeAPIServer operator CR
    does not apply to hosted clusters. See the
    [event-ttl enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/kube-apiserver/event-ttl.md)
    for the full design.

## Valid values

| | |
|---|---|
| Format | Integer number of minutes, as a string (for example `"60"`) |
| Minimum | 5 minutes |
| Maximum | 180 minutes |
| Default | 180 minutes (3h), used when the annotation is absent |

Values below 5 minutes expire events faster than a typical troubleshooting window. The maximum is
the existing default, because raising it would only increase etcd storage pressure.

## Setting the event TTL

Annotate the HostedCluster on the management cluster:

```bash
oc annotate hostedcluster my-cluster -n clusters \
  hypershift.openshift.io/event-ttl-minutes=60 --overwrite
```

Or set it at creation time in the HostedCluster manifest:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
  annotations:
    hypershift.openshift.io/event-ttl-minutes: "60"
```

## Reverting to the default

Remove the annotation:

```bash
oc annotate hostedcluster my-cluster -n clusters \
  hypershift.openshift.io/event-ttl-minutes-
```

## Behaviour

The annotation is **day-2 mutable** — you can change it on a running hosted cluster and you do not
need to recreate the cluster. Applying a change works as follows:

1. The HyperShift Operator validates the value and mirrors the annotation onto the
   HostedControlPlane.
2. The Control Plane Operator renders `--event-ttl` into the kube-apiserver configuration.
3. The changed configuration rolls the kube-apiserver pods.

Because the change rolls the kube-apiserver, expect a brief control plane API rollout. On a
highly available control plane the rollout is performed one replica at a time.

!!! important

    The TTL applies to **events created after the change**. Events that already exist keep the TTL
    they were written with and expire on their original schedule. Storage savings therefore appear
    gradually, over roughly one TTL period.

Existing hosted clusters are unaffected by upgrades to a HyperShift version that supports this
annotation: with no annotation set, the kube-apiserver continues to run with the 3 hour default.

## Verifying

Check the rendered kube-apiserver argument in the control plane namespace:

```bash
oc get configmap kas-config -n clusters-my-cluster \
  -o jsonpath='{.data.config\.json}' | jq '.apiServerArguments["event-ttl"]'
```

```json
[
  "60m"
]
```

## Invalid values

A value that is not an integer, or that falls outside the 5–180 range, is rejected by the
HyperShift Operator. It reports the problem on the HostedCluster's `ValidConfiguration` condition
and the kube-apiserver keeps its current setting:

```bash
oc get hostedcluster my-cluster -n clusters \
  -o jsonpath='{.status.conditions[?(@.type=="ValidConfiguration")]}'
```

```
invalid hypershift.openshift.io/event-ttl-minutes annotation "240": must be between 5 and 180 minutes
```

Note that the value is minutes as a bare integer. `"60m"` is **not** valid; use `"60"`.
