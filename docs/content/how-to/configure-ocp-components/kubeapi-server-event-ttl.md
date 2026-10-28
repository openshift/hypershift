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

You configure the TTL per hosted cluster with the
`spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes` field on the HostedCluster.

!!! note

    Hosted control planes have no kube-apiserver operator, so the standalone OpenShift
    `eventTTLMinutes` field on the `KubeAPIServer` operator resource does not apply to hosted
    clusters. The HostedCluster field uses the same name, units, range and default as the
    standalone field. See the
    [event-ttl enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/kube-apiserver/event-ttl.md)
    for the full design.

!!! important

    This field is behind the `KubeAPIServerEventTTL` feature gate and is currently available only
    in the `TechPreviewNoUpgrade` feature set.

## Valid values

| | |
|---|---|
| Type | Integer number of minutes (`int32`) |
| Minimum | 5 minutes |
| Maximum | 180 minutes |
| Default | 180 minutes (3 hours), used when the field is omitted |

Values below 5 minutes expire events faster than a typical troubleshooting window. The maximum is
the existing default, because raising it would only increase etcd storage pressure.

The field is optional and has no CRD-level default. Omitting it means "no opinion", and the
platform applies the 180 minute default when it renders the kube-apiserver arguments. The
rendered flag is always expressed in minutes, so the default appears as `--event-ttl=180m` and
setting `eventTTLMinutes: 180` explicitly renders exactly the same value.

## Setting the event TTL

Patch the HostedCluster on the management cluster:

```bash
oc patch hostedcluster my-cluster -n clusters --type=merge \
  -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":60}}}}'
```

Or set it at creation time in the HostedCluster manifest:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  operatorConfiguration:
    kubeAPIServer:
      eventTTLMinutes: 60
```

## Reverting to the default

Remove the field:

```bash
oc patch hostedcluster my-cluster -n clusters --type=merge \
  -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":null}}}}'
```

!!! warning

    `kubeAPIServer` must contain at least one setting. If `eventTTLMinutes` is the only one you
    set, the patch above leaves an empty `kubeAPIServer` object and the API server rejects it:

    ```
    The HostedCluster "my-cluster" is invalid: spec.operatorConfiguration.kubeAPIServer:
    Invalid value: 0: spec.operatorConfiguration.kubeAPIServer in body should have at least 1 properties
    ```

    Remove the whole object instead:

    ```bash
    oc patch hostedcluster my-cluster -n clusters --type=merge \
      -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":null}}}'
    ```

    Check what else is set before choosing which patch to use:

    ```bash
    oc get hostedcluster my-cluster -n clusters \
      -o jsonpath='{.spec.operatorConfiguration.kubeAPIServer}'
    ```

    If `operatorConfiguration` itself holds nothing but `kubeAPIServer`, you can remove it in one
    step with `-p '{"spec":{"operatorConfiguration":null}}'`.

## Behaviour

The field is **day-2 mutable** — you can change it on a running hosted cluster and you do not
need to recreate the cluster. Applying a change works as follows:

1. The HyperShift Operator copies `spec.operatorConfiguration` onto the HostedControlPlane.
2. The Control Plane Operator renders `--event-ttl` into the kube-apiserver configuration.
3. The changed configuration rolls the kube-apiserver pods.

Because the change rolls the kube-apiserver, expect a brief control plane API rollout. On a
highly available control plane the rollout is performed one replica at a time.

!!! important

    The TTL applies to **events created after the change**. Events that already exist keep the TTL
    they were written with and expire on their original schedule. Storage savings therefore appear
    gradually, over roughly one TTL period.

Existing hosted clusters are unaffected by upgrades to a HyperShift version that supports this
field: with no value set, the kube-apiserver continues to run with the 180 minute default.

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

An unconfigured hosted cluster reports the default, `["180m"]`.

## Invalid values

The range is enforced by the CRD schema, so a value outside 5–180 is rejected by the management
cluster's API server at admission time and you get an immediate error:

```bash
oc patch hostedcluster my-cluster -n clusters --type=merge \
  -p '{"spec":{"operatorConfiguration":{"kubeAPIServer":{"eventTTLMinutes":240}}}}'
```

```
The HostedCluster "my-cluster" is invalid: spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes:
Invalid value: 240: spec.operatorConfiguration.kubeAPIServer.eventTTLMinutes in body should be less than or equal to 180
```

Setting `eventTTLMinutes: 0` explicitly is rejected by the same validation, so the field cannot be
used to disable event expiry.
