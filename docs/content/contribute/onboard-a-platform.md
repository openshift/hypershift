---
title: Onboard a platform
---

# How to extend HyperShift to support a new platform

A Platform represents a series of assumptions and choices that HyperShift makes about the environment where it's running, e.g AWS, IBMCloud, Kubevirt.
The implementation of a new platform crosses multiple controllers.

The HostedCluster controller requires an implementation of the [Platform interface](https://github.com/openshift/hypershift/tree/main/hypershift-operator/controllers/hostedcluster/internal/platform) to shim a particular CAPI implementation and manage required cloud credentials.

The NodePool controller requires an implementation of the [machine template reconciliation](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/hypershift-operator/controllers/nodepool/nodepool_controller.go#L496).

The ControlPlane Operator requires the following:

- [Implement cloud credentials](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1039-L1049)

- [Reconcile Kubernetes cloud provider config](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1329)

- [Reconcile the OCP Infrastructure CR](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/support/globalconfig/infrastructure.go#L21)

- [Reconcile secret encryption (if your provider supports KMS)](https://github.com/openshift/hypershift/blob/37c45b83f9d453578e05bbd073bcb12437335efd/control-plane-operator/controllers/hostedcontrolplane/kas/deployment.go#L189-L206)

## End to end testing

The end-to-end tests require an implementation of the [Cluster interface](https://github.com/openshift/hypershift/blob/fe6cde3472473f28ac5c95c3d4f6c5785d12ac16/test/e2e/util/cluster/cluster.go#L9-L14).
As a starting point, check out
the [None-Platform implementation](https://github.com/openshift/hypershift/blob/fe6cde3472473f28ac5c95c3d4f6c5785d12ac16/test/e2e/util/cluster/none/cluster.go)
and
its [basic test](https://github.com/openshift/hypershift/blob/fe6cde3472473f28ac5c95c3d4f6c5785d12ac16/test/e2e/create_cluster_test.go#L60-L87)

## Supported platforms

- AWS.