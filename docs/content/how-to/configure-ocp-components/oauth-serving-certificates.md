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

4. Create a TLS `Secret` containing your signed certificate and private key in the HostedCluster namespace with the following keys:
    - `tls.crt`
    - `tls.key`

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

### 1. Identify your Hosted Cluster namespace

Export the namespace where your hosted cluster is running.

```bash
export HC_NAMESPACE=<hostedcluster_namespace>
export CLUSTER_NAME=<hostedcluster_name>
```

### 2. Generate a quick test certificate (most common for testing, if you don't already have)

```bash
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout tls.key \
  -out tls.crt \
  -subj "/CN=openshift-oauth" \
  -addext "subjectAltName=DNS:oauth-${HC_NAMESPACE}-${CLUSTER_NAME}.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com"
```
**Note:** This example uses a placeholder hostname. After discovering your actual OAuth route in step 4, you must regenerate this certificate with the correct hostname before proceeding to step 5.

Confirm file exists:
```bash
ls tls.crt tls.key
```

### 3. Create the TLS Secret (if not already created) in the HostedCluster namespace

The OAuth and API server namedCertificates configuration only references secrets from the HostedCluster namespace.

```bash
oc create secret tls my-oauth-cert-secret \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  -n $HC_NAMESPACE
```
You should see:
```text
secret/my-oauth-cert-secret created
```
**Note:** Even though the OAuth server runs in the hosted control plane namespace, the serving certificate secret must exist in the HostedCluster namespace. Secrets created in the hosted control plane namespace will not be picked up.

### 4. Discover the correct OAuth Route (this is critical)

Run this using the management cluster kubeconfig -
```bash
oc get routes -n ${HC_NAMESPACE}-${CLUSTER_NAME}
```

You should see something like:
```bash
NAME                  HOST/PORT                                                                                        PATH   SERVICES                PORT    TERMINATION        WILDCARD
oauth                 oauth-${HC_NAMESPACE}-${CLUSTER_NAME}.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com               oauth-openshift         <all>   passthrough/None   None
```

If the route name is `oauth`, confirm it:
```bash
oc get route oauth -n ${HC_NAMESPACE}-${CLUSTER_NAME} -o yaml
```

Now extract the OAuth route host:
```bash
OAUTH_HOST=$(oc get route oauth \
  -n ${HC_NAMESPACE}-${CLUSTER_NAME} \
  -o jsonpath='{.spec.host}')

echo "${OAUTH_HOST}"
```

Example output:
```bash
oauth-${HC_NAMESPACE}-${CLUSTER_NAME}.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com
```

### 5. Edit the HostedCluster resource

Open the HostedCluster resource for editing.

```bash
oc edit hostedcluster $CLUSTER_NAME -n $HC_NAMESPACE
```

Configure the named certificates.

Locate the `spec.configuration.apiServer` section. Add the `servingCerts.namedCertificates` stanza. You must ensure the names list matches the hostname of your OAuth endpoint.

```yaml
spec:
  configuration:
    apiServer:
      audit:
        profile: Default
      servingCerts:
        namedCertificates:
        - names:
          - oauth-${HC_NAMESPACE}-${CLUSTER_NAME}.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com   # [1]
          servingCertificate:
            name: my-oauth-cert-secret   # [2]
```
<1> Replace this with the actual host name of your OAuth route. 

<2> Replace it with the name of the Secret created in step 3.

**Important:** The serving certificate secret referenced by
`spec.configuration.apiServer.servingCerts.namedCertificates`
must exist in the HostedCluster namespace. Creating the secret in the hosted control plane namespace will not apply the certificate.

Save and apply the changes. The Control Plane Operator will reconcile the changes. The configuration will propagate to the control plane, and the OAuth server will begin serving the new certificate.

There is no separate OAuth certificate configuration field in a HostedCluster.

### 6. Verifying the OAuth serving certificate

Verify the certificate served by the route:
```bash
echo | openssl s_client \
  -connect "${OAUTH_HOST}:443" \
  -servername "${OAUTH_HOST}" \
  2>/dev/null \
  | openssl x509 -noout -subject -issuer -ext subjectAltName
```

It would be something like:
```bash
subject=CN=openshift-oauth
issuer=CN=openshift-oauth
X509v3 Subject Alternative Name: 
    DNS:oauth-${HC_NAMESPACE}-${CLUSTER_NAME}.apps.rosa.hypershift-ci-2.1xls.p3.openshiftapps.com
```

This proves:
- The OAuth route is serving the custom certificate
- The cert comes from your `my-oauth-cert-secret` secret
- The change is externally observable
