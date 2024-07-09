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

!!! note     
 
    `requires go v1.22+

```shell linenums="1"
  make build
```

3. Set the necessary environment variables

  ```shell linenums="1"
    export HYPERSHIFT_REGION="your-region"
    export HYPERSHIFT_BUCKET_NAME="your-bucket"
  ```

!!! note 

    `Consider setting HYPERSHIFT_REGION and HYPERSHIFT_BUCKET_NAME in your shell init script (e.g., $HOME/.bashrc).

!!! note 

    `Default values are provided for HYPERSHIFT_REGION and HYPERSHIFT_BUCKET_NAME so Step #4 will function without requiring you to export any values.

4. Install HyperShift in development mode which causes the operator deployment to be deployment scaled to zero so that it doesn't conflict with your local operator process (see [Prerequisites](../getting-started.md#prerequisites)):
  
```shell linenums="1"
  make hypershift-install-aws-dev
```

5. Run the HyperShift operator locally.

```shell linenums="1"
  make run-operator-locally-aws-dev
```

