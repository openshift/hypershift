!!! important

    This section is only relevant in disconnected scenarios. If this doesn't apply to your situation, please proceed to the next section.

In this section, we'll cover the TLS certificates involved in the process, primarily focusing on the private registries from which the images will be pulled. While there may be additional certificates, we'll concentrate on these particular ones.

It's important to distinguish between the various methods and their impact on the associated cluster. All of these methods essentially modify the content of the following files on the OCP (OpenShift Container Platform) control plane (Master nodes) and data plane (worker nodes):

- `/etc/pki/ca-trust/extracted/pem/`
- `/etc/pki/ca-trust/source/anchors/`
- `/etc/pki/tls/certs/`

## Adding a CA to the Management Cluster

There exist numerous methods to accomplish this within the OpenShift environment. However, we have chosen to integrate the less intrusive approach.

1. Initially, you must create a ConfigMap with a name of your choosing. In our specific case, we will utilize the name `registry-config` The content should resemble the following:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: registry-config
  namespace: openshift-config
data:
  registry.hypershiftbm.lab..5000: |
    -----BEGIN CERTIFICATE-----
    -----END CERTIFICATE-----
```

!!! note

        The data field ought to contain the registry name, while the value should encompass the Registry certificate. As is evident, the ":" character is being replaced by ".."; therefore, it is imperative to ensure this correction.

2. Now we need to patch the clusterwide object `image.config.openshift.io` including this:

```yaml
spec:
  additionalTrustedCA:
    name: registry-config
```

This modification will result in two significant consequences:

- Granting masters the capability to retrieve images from the private registry.
- Allowing the Hypershift Operator to extract the Openshift payload for the HostedCluster deployments.

!!! note

        The modification required several minutes to be successfully executed.

## Alternative: Adding a CA to the Management Cluster

We consider this as an alternative, given that it entails the masters undergoing a reboot facilitated by the Machine Config Operator.

It's described [here](https://docs.openshift.com/container-platform/latest/security/certificates/updating-ca-bundle.html). This method involves utilizing the `image-registry-operator`, which deploys the CAs to the OCP nodes.

Hypershift's operators and controllers automatically handle this process, so if you're using a GA (Generally Available) released version, it should work seamlessly, and you won't need to apply these steps. This Hypershift feature is included in the payload of the 2.4 MCE release.

However, if this feature is not working as expected or if it doesn't apply to your situation, you can follow this procedure:

- Check if the `openshift-config` namespace in the Management cluster contains a ConfigMap named `user-ca-bundle`.
- If the ConfigMap doesn't exist, execute the following command:

```bash
## REGISTRY_CERT_PATH=<PATH/TO/YOUR/CERTIFICATE/FILE>
export REGISTRY_CERT_PATH=/opt/registry/certs/domain.crt

oc create configmap user-ca-bundle -n openshift-config --from-file=ca-bundle.crt=${REGISTRY_CERT_PATH}
```

- Otherwise, if that ConfigMap exists, execute this other command:

```bash
## REGISTRY_CERT_PATH=<PATH/TO/YOUR/CERTIFICATE/FILE>
export REGISTRY_CERT_PATH=/opt/registry/certs/domain.crt
export TMP_FILE=$(mktemp)

oc get cm -n openshift-config user-ca-bundle -ojsonpath='{.data.ca-bundle\.crt}' > ${TMP_FILE}
echo >> ${TMP_FILE}
echo \#registry.$(hostname --long) >> ${TMP_FILE}
cat ${REGISTRY_CERT_PATH} >> ${TMP_FILE}
oc create configmap user-ca-bundle -n openshift-config --from-file=ca-bundle.crt=${TMP_FILE} --dry-run=client -o yaml | kubectl apply -f -
```

You have a functional script located in the `assets/<NetworkStack>/09-tls-certificates/01-config.sh`, [this is the sample for IPv6](https://github.com/jparrill/hypershift-disconnected/blob/main/assets/ipv6/09-tls-certificates/01-config.sh).
