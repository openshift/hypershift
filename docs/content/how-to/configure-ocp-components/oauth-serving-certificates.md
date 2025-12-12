# Configuring OAuth Server Certificates

This guide explains how to configure a custom serving certificate for the OAuth server in a hosted control plane.

## Overview

In Hosted Control Planes, the OAuth server shares its serving certificate configuration with the Kubernetes API server (KAS). To configure a custom serving certificate for the OAuth server, you must modify the `spec.configuration.apiServer` block in the HostedCluster resource.

**Note:** This configuration method is a deviation from standalone OpenShift behavior. In standalone OpenShift, OAuth certificates are configured separately via the Ingress Operator's `componentRoutes`. In Hosted Control Planes, the `namedCertificates` configuration in the API server section applies to both the Kubernetes API Server and the OAuth server.

## Prerequisites
Before configuring OAuth certificates:
1. Ensure you have cluster-admin access to the management cluster.

2. An existing HostedCluster resource must be present.

3. You have the oc command-line interface (CLI) installed.

4. Create a generic `Secret` containing your signed certificate and private key in the hosted control plane namespace with the following keys:
    - `tls.crt`
    - `tls.key`

5. Ensure the certificate includes the OAuth endpoint hostnames, such as:
    - `oauth.<cluster-domain>`
    - `oauth-openshift.apps.<base-domain>`

To find the OAuth endpoint for a HostedCluster:

```bash
oc --kubeconfig <hosted-kubeconfig> get oauth cluster -o jsonpath='{.spec.route.host}'
```

## How HyperShift Uses Certificates for OAuth
HyperShift’s control-plane-operator reads serving certificates through the shared function `GetNamedCertificates()`:

- API Server implementation:
[control-plane-operator/controllers/hostedcontrolplane/v2/kas/config.go#L76](https://github.com/openshift/hypershift/blob/main/control-plane-operator/controllers/hostedcontrolplane/v2/kas/config.go#L76)

- OAuth Server implementation: [control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L66](https://github.com/openshift/hypershift/blob/main/control-plane-operator/controllers/hostedcontrolplane/v2/oauth/config.go#L66)

- Cluster config source: [api/hypershift/v1beta1/clusterconfig.go#L45-L50](https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/clusterconfig.go#L45-L50)

Both components read from:
```yaml
APIServer.ServingCerts.NamedCertificates
```

This means:

- Certificates are not configured in an OAuth-specific section of the HostedCluster.

- OAuth server certificates are not provided through OAuth CRD configuration.

- HyperShift automatically injects the selected certificates into the OAuth server deployment.

## Behavior Differences from Standard OpenShift

HyperShift differs from standalone OpenShift in how OAuth certificates behave:

| Area                  | Standard OpenShift                                                       | HyperShift HostedCluster                                                |
| --------------------- | ------------------------------------------------------------------------ | ----------------------------------------------------------------------- |
| Certificate source    | Ingress Operator generates and maps certificates via **componentRoutes** | OAuth uses **apiServer.servingCerts.namedCertificates**                 |
| Certificate selection | Based on Ingress-managed routes                                          | Based on hostname match in namedCertificates                            |
| User responsibility   | No need to manually provide OAuth certs                                  | User is responsible for supplying certs if custom behavior is required  |
| Code path             | Ingress Operator manages OAuth route                                     | control-plane-operator manages OAuth server container runtime arguments |

## Configuration Example
Below is an example HostedCluster configuration where a custom certificate is supplied for the OAuth endpoint.

1. Identify your Hosted Control Plane namespace: Export the namespace where your hosted cluster control plane is running.

```bash
export HCP_NAMESPACE=<hosted_control_plane_namespace>
export CLUSTER_NAME=<hosted_cluster_name>
```

2. Create the TLS Secret (if not already created): Ensure your certificate secret exists in the hosted control plane namespace.

```bash
oc create secret tls my-oauth-cert-secret \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  -n $HCP_NAMESPACE
```

3. Edit the HostedCluster resource: Open the HostedCluster resource for editing.

```bash
oc edit hostedcluster $CLUSTER_NAME -n $HCP_NAMESPACE
```

4. Configure the named certificates: Locate the spec.configuration.apiServer section. Add the servingCerts.namedCertificates stanza. You must ensure the names list matches the hostname of your OAuth endpoint.

```yaml
spec:
  configuration:
    apiServer:
      servingCerts:
        namedCertificates:
        - names:
          - "oauth.apps.example.com"  # <1>
          servingCertificate:
            name: "my-oauth-cert-secret" # <2>
```
<1> Replace this with the actual hostname of your OAuth route. 

<2> Replace it with the name of the Secret created in step 2.

5. Save and apply the changes. The Hosted Cluster Operator will reconcile the changes. The configuration will propagate to the control plane, and the OAuth server will begin serving the new certificate.

There is no separate OAuth certificate configuration field in a HostedCluster.
