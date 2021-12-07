---
title: Use custom operator images
---

# How to install HyperShift with a custom image

1. Build and push a custom image build to your own repository.

        make IMG=quay.io/my/hypershift:latest docker-build docker-push

1. Install HyperShift using the custom image:

        bin/hypershift install --hypershift-image quay.io/my/hypershift:latest

1. (Optional) If your repository is private, create a secret:

        oc create secret --namespace hypershift generic hypershift-operator-pull-secret \
        --from-file=.dockerconfig=/my/pull-secret --type=kubernetes.io/dockerconfig

    Then update the operator ServiceAccount in the hypershift namespace:

        oc patch serviceaccount --namespace hypershift operator \
        -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'

