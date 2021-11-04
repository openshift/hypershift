---
title: Install HyperShift
---

# How to install HyperShift

HyperShift must be deployed into an existing OpenShift cluster.

**Prerequisites:**

* Admin access to an OpenShift cluster (version 4.7+).
* The OpenShift `oc` CLI tool.
* The `hypershift` CLI tool

Install HyperShift into the management cluster:

```shell
hypershift install
```

To uninstall HyperShift, run:

```shell
hypershift install --render | oc delete -f -
```
