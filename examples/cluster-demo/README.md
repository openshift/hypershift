
### Provision and Destroy HyperShift clusters using PlatformConfiguration
#### Confiugre once
1. Connect to OpenShift
2. Install hypershift 
    ```bash
    hypershift install --oidc-storage-provider-s3-bucket-name=my-s3-bucket --oidc-storage-provider-s3-region=us-east-1
    ```
3. Update the `operator` deployment in project `hypershift` to use the following image
    ```yaml
    quay.io/jpacker/hypershift:latest
    ```
4. Create the namespace `clusters`
    ```bash
    oc new-project clusters
    ```
5. In the `clusters` namespace, create an AWS Cloud Provider secret (ACM format)
  ```yaml
  ---
  apiVersion: v1
  kind: Secret
  type: Opaque
  metadata:
    name: aws
    namespace: clusters
  stringData:
    aws_access_key_id: AWS_ID_KEY
    aws_secret_access_key: AWS_SECRET_KEY
  ```
3. In the `clusters` namespace, create a pull secret (ACM/Hive format)
  ```yaml
  ---
  apiVersion: v1
  kind: Secret
  metadata:
    name: pull-secret
    namespace: clusters
  stringData:
    .dockerconfigjson: |-
      {"auths": MY_PULL_SECRET }
  type: kubernetes.io/dockerconfigjson
   ```
#### For each HyperShift cluster
1. Edit the file `./examples/cluster-demo/platformconfiguration.yaml` and set the `base-domain` value
2. Make any changes you want to the `hostedcluster.yaml` and `nodepool.yaml`, the defaults will work
3. Apply the three resources `PlatformConfiguration`, `HostedCluster` & `NodePool` 
  ```bash
  oc apply -f examples/cluster-demo/
  ```
7. It will take 5-6min for the deploy to complete!
8. To clean up delete the three resources
    ```bash
    oc delete -f examples/cluster-demo/
    ```