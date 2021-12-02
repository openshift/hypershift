---
title: Onboard a platform
---

# How to extend HyperShift to support a new platform

A Platform represents a series of assumptions and choices that Hyperhift makes about the environment where it's running, e.g AWS, IBMCloud, Kubevirt.
The implementation of a new platform crosses multiple controllers.

The HostedCluster controller requires an implementation of the [Platform interface](hypershift-operator/controllers/hostedcluster/internal/platform) to shim a particular CAPI implementation and manage required cloud credentials.

The NodePool controller requires an implementation of the [machine template reconciliation](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/hypershift-operator/controllers/nodepool/nodepool_controller.go#L496).

The ControlPlane Operator requires the following:

- [Implement cloud credentials](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1039-L1049)

- [Reconcile Kubernetes cloud provider config](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go#L1329)

- [Reconcile the OCP Infrastructure CR](https://github.com/openshift/hypershift/blob/58cabbac00c541b55c7e7925fe7e46f0a55b5ceb/support/globalconfig/infrastructure.go#L21)

## Supported platforms

- AWS.