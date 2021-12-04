---
title: Run operators locally
---

# How to run the HyperShift Operator in a local process

1. Ensure the `KUBECONFIG` environment variable points to a management cluster
   with no HyperShift installed yet.

2. Build HyperShift.

        make build

3. Install HyperShift in development mode which causes the operator deployment
   to be deployment scaled to zero so that it doesn't conflict with your local
   operator process.

        bin/hypershift install --development

4. Run the HyperShift operator locally.

        bin/hypershift-operator run
