# Custom Kube API Server DNS Configuration

This guide explains how to configure a custom DNS domain for your HyperShift cluster's Kube API Server and how to consume the generated Kubeconfig that points to this custom domain. This feature is included in OpenShift 4.19 version and MCE 2.9.

## Prerequisites

- A running HyperShift cluster
- Access to modify the HostedCluster resource
- A custom DNS domain that you want to use for the Kube API Server
- A custom certificate configured in the Kubeapi-server as we already explained [here](kubeapi-server.md)

## Configuration

To configure a custom DNS domain for your cluster's Kube API Server, you need to:

1. First, configure the DNS record in your provider platform (AWS, Azure, etc.). This is your responsibility as the cluster administrator. The DNS record must be properly configured and resolvable from your OpenShift cluster.

2. Then, modify the `kubeAPIServerDNSName` field in your HostedCluster specification. This field accepts a URI that will be used as the API server endpoint.

### Example Configuration

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example-cluster
  namespace: clusters
spec:
  configuration:
    apiServer:
      servingCerts:
        namedCertificates:
        - names:
          - api-custom-cert-sample-hosted.sample-hosted.example.com
          servingCertificate:
            name: sample-hosted-kas-custom-cert
  kubeAPIServerDNSName: api-custom-cert-sample-hosted.sample-hosted.example.com
  # ... other spec fields ...
```

## Consuming the Kubeconfig

After applying the configuration described above:

1. The HyperShift operator will generate a new Kubeconfig that points to your custom DNS domain
2. You can retrieve the Kubeconfig using the standard methods, accessing the secret directly:
```bash
kubectl get secret <cluster-name>-custom-admin-kubeconfig -n <cluster-namespace> -o jsonpath='{.data.kubeconfig}' | base64 -d
```

## Important Considerations

1. Ensure that your custom DNS domain is properly configured and resolvable
2. The DNS domain should have valid TLS certificates configured
3. Network access to the custom DNS domain should be properly configured in your environment
4. The custom DNS domain should be unique across your HyperShift clusters

## Troubleshooting

If you encounter issues accessing the cluster using the custom DNS:

1. Verify that the DNS record is properly configured and resolving
2. Check that the TLS certificates for the custom domain are valid. (Verify the SAN is correct for your domain)
```
oc get secret -n clusters sample-hosted-kas-custom-cert -o jsonpath='{.data.tls\.crt}' | base64 -d |openssl x509 -text -noout -
```
3. Ensure network connectivity to the custom domain is working
4. Verify that the HostedCluster status shows the custom Kubeconfig properly
```
status:
  customKubeconfig:
    name: sample-hosted-custom-admin-kubeconfig
```
5. Check the `kube-apiserver` logs in the HostedControlPlane namespace
```
oc logs -n <HostedControlPlane namespace> -l app=kube-apiserver -f -c kube-apiserver
```
