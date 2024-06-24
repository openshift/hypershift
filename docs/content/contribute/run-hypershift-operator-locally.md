---
title: Run the hypershift-operator locally
---

## Run the HyperShift Operator locally

To run the HyperShift Operator locally, follow these steps:

1. Ensure that the `KUBECONFIG` environment variable is set to a management cluster where HyperShift has not been installed yet.

  ```shell linenums="1"
   export KUBECONFIG="/path/to/your/kubeconfig"
  ```

2. Build HyperShift.

        # requires go v1.22+
        $ make build

3. Remove previous artifacts (if any). The command below ensures any existing artifacts are cleared before proceeding with installation:

```shell linenums="1"
hypershift install render | oc delete -f -
```

4. Install HyperShift in development mode which causes the operator deployment to be deployment scaled to zero so that it doesn't conflict with your local operator process (see [Prerequisites](../getting-started.md#prerequisites)):

```shell linenums="1"
REGION=your-region
BUCKET_NAME=your-bucket-name
AWS_CREDS="$HOME/.aws/credentials"

./bin/hypershift install \
  --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
  --oidc-storage-provider-s3-credentials $AWS_CREDS \
  --oidc-storage-provider-s3-region $REGION \
  --enable-defaulting-webhook=false \
  --enable-conversion-webhook=false \
  --enable-validating-webhook=false \
  --development=true
```

5. Run the HyperShift operator locally.

```shell linenums="1"
set -euo pipefail
export METRICS_SET="Telemetry"

export MY_NAMESPACE=hypershift
export MY_NAME=operator

./bin/hypershift-operator \
  run \
  --metrics-addr=0 \
  --enable-ocp-cluster-monitoring=false \
  --enable-ci-debug-output=true \
  --private-platform=AWS \
  --enable-ci-debug-output=true \
  --oidc-storage-provider-s3-bucket-name=${BUCKET_NAME} \
  --oidc-storage-provider-s3-region=${REGION} \
  --oidc-storage-provider-s3-credentials=$HOME/.aws/credentials \
  --control-plane-operator-image=quay.io/hypershift/hypershift:latest
```