---
title: Install the CLI
---

# How to install the CLI

The `hypershift` CLI helps perform some basic tasks for trying HyperShift.

**Prerequisites:**

* Git
* Go 1.17

Clone the HyperShift repository:

```shell
git clone git@github.com:openshift/hypershift.git
```

Build the CLI:

```shell
make hypershift
```

The `hypershift` executable will be installed to `./bin/hypershift`.
