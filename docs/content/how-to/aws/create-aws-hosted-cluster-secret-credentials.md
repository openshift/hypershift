---
title: Create an AWS Hosted Cluster using a credential secret
---

# Create an AWS Hosted Cluster using a credential secret

## Creating the credential secret

If using the Multi-cluster Engine, the console provides a credential page that allows you to create the AWS credential secret.

To manually create the secret, apply the following secret yaml:

```yaml
apiVersion: v1
kind: Secret
type: Opaque
metadata:
  name: my-aws-credentials
  namespace: clusters
  labels:
    cluster.open-cluster-management.io/credentials: ""
    cluster.open-cluster-management.io/type: aws
stringData:
  aws_access_key_id: <value>
  aws_secret_access_key: <value>
  baseDomain: <value>
  pullSecret: <value>
  ssh-privatekey: <value>
  ssh-publickey: <value>
```

!!! important
    
    The required parameters when using a secret to create a cluster are `--secret-creds <SECRET_NAME> --namespace <NAMESPACE_NAME>`. 
    
    If `--namespace` is not included, then the "clusters" namespace will used.

!!! note
    
    The labels on this secret allow it to be displayed by the multi-cluster engine console.

## Create a HostedCluster using a credential secret

[Prerequisites](#prerequisites):

```shell linenums="1"
REGION=us-east-1
CLUSTER_NAME=example
SECRET_CREDS="example-multi-cluster-engine-credential-secret"
NAMESPACE="example-namespace"

hypershift create cluster aws \
  --name $CLUSTER_NAME \
  --namespace $NAMESPACE \
  --node-pool-replicas=3 \
  --secret-creds $SECRET_CREDS \
  --region $REGION \
```

## Credential secret defaults
The following values will be extracted from the credential and used when creating the hosted cluster:

  * AWS key id and AWS secret key
  * Base domain
  * Pull secret
  * SSH key

## Using credential secret overrides
The following command line parameters can be used with `--secret-creds`. Each is optional, but when provided they will override the
values found in the credential secret.

  * `--aws-creds`
  * `--base-domain`
  * `--pull-secret`
  * `--ssh-key`
