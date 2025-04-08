# Configuring Custom API Server Certificate in HostedCluster

This guide explains how to configure a custom certificate for the API server in a HostedCluster.

## Overview

You can configure a custom certificate for the API server by specifying the certificate details in the `spec.configuration.apiServer` section of your HostedCluster configuration.

## Considerations creating the certificate

When creating a custom certificate for the API server, there are important considerations to keep in mind regarding Subject Alternative Names (SANs):

### SAN Conflicts with Internal API

If your HostedCluster configuration includes a service publishing strategy like:
```yaml
services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
      loadBalancer:
        hostname: api-custom-cert-sample-hosted.sample-hosted.example.com
```

You must ensure that the certificate's Subject Alternative Names (SANs) do not conflict with the internal API endpoint (`api-int`). This is because:

- The internal API endpoint is automatically created and managed by the platform
- Using the same hostname in both the custom certificate and the internal API endpoint can cause routing conflicts

!!! Important

    The only exception to this rule is when using AWS as the provider with either Private or PublicAndPrivate configurations. In these specific cases, the SAN conflict is allowed and managed by the platform.

### Certificate Requirements

   - The certificate must be valid for the external API endpoint
   - The certificate should not include SANs that match the internal API endpoint pattern (except for AWS Private/PublicAndPrivate configurations)
   - Ensure the certificate's validity period aligns with your cluster's expected lifecycle

## Configuration Example

Here's an example of how to configure a custom certificate for the API server:

```yaml
spec:
  configuration:
    apiServer:
      servingCerts:
        namedCertificates:
        - names:
          - api-custom-cert-sample-hosted.sample-hosted.example.com
          servingCertificate:
            name: sample-hosted-kas-custom-cert
```

## Configuration Fields

- `names`: List of DNS names that the certificate should be valid for.
- `servingCertificate.name`: Name of the secret containing the custom certificate

## Prerequisites

Before configuring a custom certificate:

1. Create a Kubernetes secret containing your custom certificate in the management cluster
2. The secret should contain the following keys:
    * `tls.crt`: The certificate
    * `tls.key`: The private key

## Steps to Configure

1. Create a secret with your custom certificate:
   ```bash
   oc create secret tls sample-hosted-kas-custom-cert \
     --cert=path/to/cert.crt \
     --key=path/to/key.key \
     -n <namespace>
   ```

2. Update your HostedCluster configuration with the custom certificate details as shown in the example above.

3. Apply the changes to your HostedCluster.

## Verification

After applying the configuration, you can verify that the API server is using the custom certificate by:

1. Checking the API server pods to ensure they have the new certificate mounted
2. Testing the connection to the API server using the custom domain name
3. Verifying the certificate details in your browser or using tools like `openssl`

## References

- Additional validation to prevent the SAN conflict [PR](https://github.com/openshift/hypershift/pull/5875)
- Common error creating a custom certificate for the KAS - [Solution 6984698](https://access.redhat.com/solutions/6984698)
- [OpenShift Documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/security_and_compliance/configuring-certificates#customize-certificates-api-add-named_api-server-certificates)