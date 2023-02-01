---
title: Use custom operator images
---

# How to install HyperShift with a custom image

1. Build and push a custom image build to your own repository.

    ```shell linenums="1"
      export QUAY_ACCOUNT=example

      make build
      make RUNTIME=podman IMG=quay.io/${QUAY_ACCOUNT}/hypershift:latest docker-build docker-push
    ```

1. Install HyperShift using the custom image:

    ```shell linenums="1"
      hypershift install \
        --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
        --oidc-storage-provider-s3-credentials $AWS_CREDS \
        --oidc-storage-provider-s3-region $REGION \
        --hypershift-image quay.io/${QUAY_ACCOUNT}/hypershift:latest \
    ```

1. (Optional) If your repository is private, create a secret:

    ```shell
      oc create secret --namespace hypershift generic hypershift-operator-pull-secret \
        --from-file=.dockerconfig=/my/pull-secret --type=kubernetes.io/dockerconfig
    ```

    Then update the operator ServiceAccount in the hypershift namespace:

    ```shell
      oc patch serviceaccount --namespace hypershift operator \
        -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'
    ```
