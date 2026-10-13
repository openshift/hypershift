# Secrets CSI Usage
The Secrets CSI driver is used in HyperShift's managed Azure architecture in order to read secrets from Azure Key Vault 
and mount them as files in a pod. This allows for the secure storage of sensitive information such as credentials and 
certificates.

More information on Secrets CSI driver can be found in the [official documentation](https://secrets-store-csi-driver.sigs.k8s.io/).

## Overview
A single managed identity is used to pull any secrets or certificates from Azure Key Vault. The managed identity is 
created when the AKS cluster is created. For example, this happens when the flag 
`enable-addons azure-keyvault-secrets-provider` is provided when creating the AKS cluster using the Azure CLI. 

!!! important

    The created managed identity is expected to have the `Key Vault Secrets User` role assigned to it so that it can read 
    secrets and credentials from the Azure Key Vault.

!!! important

    This managed identity will be used by any HostedClusters managed by the HO to read secrets from the Azure Key Vault.

This managed identity is passed in as a client ID to the HyperShift Operator during installation through the flag 
`aro-hcp-key-vault-users-client-id`. This client ID will be passed in to every created SecretProviderClass CR and used 
in the field called `userAssignedIdentityID`.

## SecretsProviderClass CRs
The HyperShift Operator creates a SecretProviderClass CR for:

- the control plane operator (CPO)
- the nodepool management provider (CAPZ)

The CPO creates a SecretProviderClass CR for:

- key management service (KMS)
- cloud-network-config-controller (CNCC)
- cloud provider (CP, aka CCM)
- ingress
- image registry
- Azure disk CSI driver
- Azure file CSI driver

Here is an example of a SecretProviderClass CR for the CPO:

```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  annotations:
    hypershift.openshift.io/cluster: e2e-clusters-example/example-cluster
spec:
  parameters:
    keyvaultName: aks-e2e
    objects: |2

      array:
        - |
          objectName: cpo-secret
          objectEncoding: utf-8
          objectType: secret
    tenantId: f47a1c3d-89ab-4def-b456-123456789abc
    usePodIdentity: "false"
    useVMManagedIdentity: "true"
    userAssignedIdentityID: 92bd5e7a-3cfe-41a2-9f88-df0123456789
  provider: azure
```

## How SecretProviderClass CR is Used
The SecretProviderClass CR is then used by the Secrets CSI driver to mount the secret, `objectName` in the above 
example, into the pod as a file in a volume mount. Here is an example of a pod spec that mounts the secret:

```yaml
  containers:
...
    name: control-plane-operator
...
    volumeMounts:
      - mountPath: /mnt/certs
        name: cpo-cert
        readOnly: true
...
    volumes:
      - csi:
          driver: secrets-store.csi.k8s.io
          readOnly: true
          volumeAttributes:
            secretProviderClass: managed-azure-cpo
        name: cpo-cert
```

The mounted secret can be viewed in the pod by navigating to the `/mnt/certs` directory and catting the file. In this 
example, something like:

```bash
k exec -it control-plane-operator -- /bin/bash
cat /mnt/certs/cpo-secret
```

## How Secret Information is Used By Components
All the different components using the Secrets CSI driver will have their own way of consuming the secret.

### Consumed directly in the operator/controller
The following components directly use the secret file mounted in the pod to authenticate with Azure cloud:

- CPO
- CNCC
- ingress
- image registry

For example:
```go
	certPath := config.ManagedAzureCertificatePath + hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ControlPlaneOperator.CredentialsSecretName
	creds, err := dataplane.NewUserAssignedIdentityCredential(ctx, certPath, dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}))
	if err != nil {
		return fmt.Errorf("failed to create azure creds to verify resource group locations: %v", err)
	}
```

### Consumed by a configuration file
The following components use a configuration file in order to know where to find the secret mounted in the pod:

- CP/CCM
- Azure disk CSI driver
- Azure file CSI driver

For an example, see the [official documentation](https://cloud-provider-azure.sigs.k8s.io/install/configs/).

### Consumed through a CR
Finally, the nodepool management provider (CAPZ) uses a CR, AzureClusterIdentity, to identify where the secret is 
mounted in the pod.

For an example, see the [official documentation](https://capz.sigs.k8s.io/topics/identities).
