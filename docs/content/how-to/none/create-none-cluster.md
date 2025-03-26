# Create a None cluster

This document explains how to create HostedClusters and NodePools using the 'None' platform to create bare metal worker nodes.

## Hypershift Operator requirements

* cluster-admin access to an OpenShift Cluster (We tested it with 4.9.17) to deploy the CRDs + operator
* 1 x filesystem type Persistent Volume to store the `etcd` database for demo purposes (3x for 'production' environments)

## Versions used

* OCP compact cluster (3 masters) version 4.9.17
* HyperShift Operator built from sources (Commit ID [0371f889](https://github.com/openshift/hypershift/commit/0371f889))

## Prerequisites: Building the Hypershift Operator

Currently, the HyperShift operator is deployed using the `hypershift` binary, which needs to be compiled manually.
RHEL8 doesn't include go1.18 officially but it can be installed via `gvm` by following the next steps:

~~~sh
# Install prerequisites
sudo dnf install -y curl git make bison gcc glibc-devel
git clone https://github.com/openshift/hypershift.git
pushd hypershift

# Install gvm to install go 1.18
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source ${HOME}/.gvm/scripts/gvm
gvm install go1.18
gvm use go1.18

# build the binary
make hypershift
popd
~~~

Then, the `hypershift` binary can be moved to a convenient place as:

~~~sh
sudo install -m 0755 -o root -g root hypershift/bin/hypershift /usr/local/bin/hypershift
~~~

Alternatively, it can be compiled using a container as:

~~~sh
# Install prerequisites
sudo dnf install podman -y
# Compile hypershift
mkdir -p ./tmp/ && \
podman run -it -v ${PWD}/tmp:/var/tmp/hypershift-bin/:Z --rm docker.io/golang:1.18 sh -c \
  'git clone --depth 1 https://github.com/openshift/hypershift.git /var/tmp/hypershift/ && \
  cd /var/tmp/hypershift && \
  make hypershift && \
  cp bin/hypershift /var/tmp/hypershift-bin/'
sudo install -m 0755 -o root -g root ./tmp/hypershift /usr/local/bin/hypershift
~~~

> **WARNING**: At the time of writing this document, there were some issues already fixed in HyperShift but unfortunately those weren't included in the latest release of the container.

## Prerequisite (optional): Create a custom HyperShift image

The official container image containing the HyperShift bits is hosted in [quay.io/hypershift/hypershift](https://quay.io/repository/hypershift/hypershift) but if creating a custom HyperShift image is needed, the following steps can be performed:

~~~sh
QUAY_ACCOUNT='testuser'
podman login -u ${QUAY_ACCOUNT} -p testpassword quay.io
sudo dnf install -y curl git make bison gcc glibc-devel
git clone https://github.com/openshift/hypershift.git
pushd hypershift

# Install gvm to install go 1.18
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source ${HOME}/.gvm/scripts/gvm
gvm install go1.18 -B
gvm use go1.18

# Build the binaries and the container
make build
make RUNTIME=podman IMG=quay.io/${QUAY_ACCOUNT}/hypershift:latest docker-build docker-push

sudo install -m 0755 -o root -g root hypershift/bin/hypershift /usr/local/bin/hypershift
popd
~~~

## Deploy HyperShift

Once the binary is in place, the operator deployment is performed as:

~~~sh
hypershift install
~~~

Or if using a custom image:

~~~sh
hypershift install --hypershift-image quay.io/${QUAY_ACCOUNT}/hypershift:latest
~~~

Using `hypershift install --render > hypershift-install.yaml` will create a yaml file with all the assets required to deploy HyperShift, then they can be applied as:

~~~sh
oc apply -f ./hypershift-install.yaml
~~~

## Deploy a hosted cluster

There are two main CRDs to describe a hosted cluster:

* [`HostedCluster`](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.HostedCluster) defines the control plane hosted in the management OpenShift

* [`NodePool`](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.NodePool) defines the nodes that will be created/attached to a hosted cluster

The `hostedcluster.spec.platform` specifies the underlying infrastructure provider for the cluster and is used to configure platform specific behavior, so depending on the environment it is required to configure it properly.

In this repo we will cover the 'none' provider.

### Requisites

* Proper DNS entries for the workers (if the worker uses `localhost` it won't work)
* DNS entry to point `api.${cluster}.${domain}` to each of the nodes where the hostedcluster will be running. This is because the hosted cluster API is exposed as a `nodeport`. For example:

~~~sh
api.hosted0.example.com.  IN A  10.19.138.32
api.hosted0.example.com.  IN A  10.19.138.33
api.hosted0.example.com.  IN A  10.19.138.37
~~~

* DNS entry to point `*.apps.${cluster}.${domain}` to a load balancer deployed to redirect incoming traffic to the ingresses pod [the OpenShift documentation](https://docs.openshift.com/container-platform/4.9/installing/installing_platform_agnostic/installing-platform-agnostic.html#installation-load-balancing-user-infra-example_installing-platform-agnostic) provides some instructions about this)
> **NOTE**: This is not strictly required to deploy a sample cluster but to access the exposed routes there. Also, it can be simply an A record pointing to a worker IP where the ingress pods are running and enabling the `hostedcluster.spec.infrastructureAvailabilityPolicy: SingleReplica` configuration parameter.

* Pull-secret (available at cloud.redhat.com)
* ssh public key already available (it can be created as `ssh-keygen -t rsa -f /tmp/sshkey -q -N ""`)
* Any httpd server available to host a ignition file (text) and a modified RHCOS iso

### Procedure

* Create a file containing all the variables depending on the environment:

~~~sh
cat <<'EOF' > ./myvars
export CLUSTERS_NAMESPACE="clusters"
export HOSTED="hosted0"
export HOSTED_CLUSTER_NS="clusters-${HOSTED}"
export PULL_SECRET_NAME="${HOSTED}-pull-secret"
export MACHINE_CIDR="10.19.138.0/24"
export OCP_RELEASE_VERSION="4.9.17"
export OCP_ARCH="x86_64"
export BASEDOMAIN="example.com"

export PULL_SECRET_CONTENT=$(cat ~/clusterconfigs/pull-secret.txt)
export SSH_PUB=$(cat ~/.ssh/id_rsa.pub)
EOF
source ./myvars
~~~

* Create a namespace to host the HostedCluster and secrets

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
kind: Namespace
metadata:
 name: ${CLUSTERS_NAMESPACE}
EOF

export PS64=$(echo -n ${PULL_SECRET_CONTENT} | base64 -w0)
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
data:
 .dockerconfigjson: ${PS64}
kind: Secret
metadata:
 name: ${PULL_SECRET_NAME}
 namespace: ${CLUSTERS_NAMESPACE}
type: kubernetes.io/dockerconfigjson
EOF

envsubst <<"EOF" | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ${HOSTED}-ssh-key
  namespace: ${CLUSTERS_NAMESPACE}
stringData:
  id_rsa.pub: ${SSH_PUB}
EOF
~~~

* Create the `hostedcluster` and the `nodepool` objects:

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  name: ${HOSTED}
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  release:
    image: "quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE_VERSION}-${OCP_ARCH}"
  pullSecret:
    name: ${PULL_SECRET_NAME}
  sshKey:
    name: "${HOSTED}-ssh-key"
  networking:
    serviceCIDR: "172.31.0.0/16"
    podCIDR: "10.132.0.0/14"
    machineCIDR: "${MACHINE_CIDR}"
  platform:
    type: None
  infraID: ${HOSTED}
  dns:
    baseDomain: ${BASEDOMAIN}
  services:
  - service: APIServer
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: OAuthServer
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: OIDC
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: None
  - service: Konnectivity
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: Ignition
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
EOF

envsubst <<"EOF" | oc apply -f -
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  name: ${HOSTED}-workers
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  clusterName: ${HOSTED}
  replicas: 0
  management:
    autoRepair: false
    upgradeType: Replace
  platform:
    type: None
  release:
    image: "quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE_VERSION}-${OCP_ARCH}"
EOF
~~~

> **NOTE**: The `HostedCluster` and `NodePool` objects can be created using the `hypershift` binary as `hypershift create cluster`. See the `hypershift create cluster -h` output for more information.

After a while, a number of pods will be created in the `${CLUSTERS_NAMESPACE}` namespace. Those pods are the control plane of the hosted cluster.

~~~sh
oc get pods -n ${HOSTED_CLUSTER_NS}

NAME                                              READY   STATUS     RESTARTS        AGE
catalog-operator-54d47cbbdb-29mzf                 2/2     Running    0               6m24s
certified-operators-catalog-78db79f86-6hlk9       1/1     Running    0               6m30s
cluster-api-655c8ff4fb-598zs                      1/1     Running    1 (5m57s ago)   7m26s
cluster-autoscaler-86d9474fcf-rmwzr               0/1     Running    0               6m11s
cluster-policy-controller-bf87c9858-nnlgw         1/1     Running    0               6m37s
cluster-version-operator-ff9475794-dc9hf          2/2     Running    0               6m37s
community-operators-catalog-6f5797cdc4-2hlcp      1/1     Running    0               6m29s
control-plane-operator-749b94cf54-p2lg2           1/1     Running    0               7m23s
etcd-0                                            1/1     Running    0               6m46s
hosted-cluster-config-operator-6646d8f868-h9r2w   0/1     Running    0               6m34s
ignition-server-7797c5f7-vkb2b                    1/1     Running    0               7m20s
ingress-operator-5dc47b99b7-jttpg                 0/2     Init:0/1   0               6m35s
konnectivity-agent-85f979fcb4-67c5h               1/1     Running    0               6m45s
konnectivity-server-576dc7b8b7-rxgms              1/1     Running    0               6m46s
kube-apiserver-66d99fd9fb-dvslc                   2/2     Running    0               6m43s
kube-controller-manager-68dd9fb75f-mgd22          1/1     Running    0               6m42s
kube-scheduler-748d9f5bcb-mlk52                   0/1     Running    0               6m42s
machine-approver-c8c68ffb9-psc6n                  0/1     Running    0               6m11s
oauth-openshift-7fc7dc9c66-fg258                  1/1     Running    0               6m8s
olm-operator-54d7d78b89-f9dng                     2/2     Running    0               6m22s
openshift-apiserver-64b4669d54-ffpw2              2/2     Running    0               6m41s
openshift-controller-manager-7847ddf4fb-x5659     1/1     Running    0               6m38s
openshift-oauth-apiserver-554c449b8f-lk97w        1/1     Running    0               6m41s
packageserver-6fd9f8479-pbvzl                     0/2     Init:0/1   0               6m22s
redhat-marketplace-catalog-8cc88f5cb-hbxv9        1/1     Running    0               6m29s
redhat-operators-catalog-b749d6945-2bx8k          1/1     Running    0               6m29s
~~~

The hosted cluster's kubeconfig can be extracted as:

~~~sh
oc extract -n ${CLUSTERS_NAMESPACE} secret/${HOSTED}-admin-kubeconfig --to=- > ${HOSTED}-kubeconfig
oc get clusterversion --kubeconfig=${HOSTED}-kubeconfig
~~~

### Adding a bare metal worker

* Download the RHCOS live ISO into the httpd server (for example into `/var/www/html/hypershift-none/` on an apache server hosted at www.example.com)

~~~sh
mkdir -p /var/www/html/hypershift-none/
curl -s -o /var/www/html/hypershift-none/rhcos-live.x86_64.iso https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.9/4.9.0/rhcos-live.x86_64.iso
~~~

* Download the ignition generated in the hostedcluster

~~~sh
IGNITION_ENDPOINT=$(oc get hc ${HOSTED} -o json | jq -r '.status.ignitionEndpoint')
IGNITION_TOKEN_SECRET=$(oc -n clusters-${HOSTED} get secret | grep token-${HOSTED}  | awk '{print $1}')
set +x
IGNITION_TOKEN=$(oc -n clusters-${HOSTED} get secret ${IGNITION_TOKEN_SECRET} -o jsonpath={.data.token})
curl -s -k -H "Authorization: Bearer ${IGNITION_TOKEN}" https://${IGNITION_ENDPOINT}/ignition > /var/www/html/hypershift-none/worker.ign
~~~

* Modify the RHCOS live ISO to install the worker using that ignition file into the `/dev/sda` device (your milleage may vary)

~~~sh
podman run --rm -it -v /var/www/html/hypershift-none/:/data:z --workdir /data quay.io/coreos/coreos-installer:release iso customize --live-karg-append="coreos.inst.ignition_url=http://www.example.com/hypershift-none/worker.ign coreos.inst.install_dev=/dev/sda" -o ./rhcos.iso ./rhcos-live.x86_64.iso
podman run --rm -it -v /var/www/html/hypershift-none/:/data:z --workdir /data quay.io/coreos/coreos-installer:release iso kargs show ./rhcos.iso
chmod a+r /var/www/html/hypershift-none/rhcos.iso
~~~

* (Optionally) Check the ISO can be downloaded

~~~sh
curl -v -o rhcos.iso http://www.example.com/hypershift-none/rhcos.iso
~~~

* Attach the ISO to the BMC and boot from it once.

This step is highly dependent on the hardware used. In this example, using Dell hardware, the following steps can be done, but your milleage may vary:

~~~sh
export IDRACIP=10.19.136.22
export IDRACUSER="root"
export IDRACPASS="calvin"

curl -s -L -k https://raw.githubusercontent.com/dell/iDRAC-Redfish-Scripting/master/Redfish%20Python/SetNextOneTimeBootVirtualMediaDeviceOemREDFISH.py -O
curl -s -L -k https://raw.githubusercontent.com/dell/iDRAC-Redfish-Scripting/master/Redfish%20Python/InsertEjectVirtualMediaREDFISH.py -O
curl -s -L -k https://raw.githubusercontent.com/dell/iDRAC-Redfish-Scripting/master/Redfish%20Python/GetSetPowerStateREDFISH.py -O

# Turn the server off
python3 ./SetPowerStateREDFISH.py -ip ${IDRACIP} -u ${IDRACUSER} -p ${IDRACPASS} -r Off
# Insert the ISO as virtual media
python3 ./InsertEjectVirtualMediaREDFISH.py -ip ${IDRACIP} -u ${IDRACUSER} -p ${IDRACPASS} -o 1 -d 1 -i http://www.example.com/hypershift-none/rhcos.iso
# Set boot once using the Virtual media previously attached
python3 ./SetNextOneTimeBootVirtualMediaDeviceOemREDFISH.py -ip ${IDRACIP} -u ${IDRACUSER} -p ${IDRACPASS} -d 1 -r y
~~~

After a while, the worker will be installed.


* Sign the CSR:

~~~sh
oc get csr --kubeconfig=${HOSTED}-kubeconfig -o go-template='{{range .items}}{{if not .status}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}' | xargs oc adm certificate approve --kubeconfig=${HOSTED}-kubeconfig
~~~

* Then the worker is added to the cluster:

~~~sh
oc get nodes --kubeconfig=${HOSTED}-kubeconfig
NAME                                         STATUS   ROLES    AGE   VERSION
kni1-worker-0.cloud.lab.eng.bos.redhat.com   Ready    worker   28m   v1.22.3+e790d7f

oc get co --kubeconfig=${HOSTED}-kubeconfig
NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.9.17    True        False         False      17m
csi-snapshot-controller                    4.9.17    True        False         False      24m
dns                                        4.9.17    True        False         False      24m
image-registry                             4.9.17    False       False         False      4m49s   NodeCADaemonAvailable: The daemon set node-ca does not have available replicas...
ingress                                    4.9.17    True        False         False      14m
kube-apiserver                             4.9.17    True        False         False      3h45m
kube-controller-manager                    4.9.17    True        False         False      3h45m
kube-scheduler                             4.9.17    True        False         False      3h45m
kube-storage-version-migrator              4.9.17    True        False         False      24m
monitoring                                           False       True          True       9m      Rollout of the monitoring stack failed and is degraded. Please investigate the degraded status error.
network                                    4.9.17    True        False         False      25m
node-tuning                                4.9.17    True        False         False      24m
openshift-apiserver                        4.9.17    True        False         False      3h45m
openshift-controller-manager               4.9.17    True        False         False      3h45m
openshift-samples                          4.9.17    True        False         False      23m
operator-lifecycle-manager                 4.9.17    True        False         False      3h45m
operator-lifecycle-manager-catalog         4.9.17    True        False         False      3h45m
operator-lifecycle-manager-packageserver   4.9.17    True        False         False      3h45m
service-ca                                 4.9.17    True        False         False      25m
storage                                    4.9.17    True        False         False      25m

oc get clusterversion --kubeconfig=${HOSTED}-kubeconfig
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version             False       True          3h46m   Unable to apply 4.9.17: some cluster operators have not yet rolled out
~~~

> **NOTE**: Some cluster operators are degraded because there is only a single worker and they require at least 2. However setting the `hostedcluster.spec.infrastructureAvailabilityPolicy: SingleReplica` configuration parameter disables the requirement and will make the clusters operator available with a single worker.

* After adding 2 workers, the hosted cluster is completely available as well as all the cluster operators:

~~~sh
oc get hostedcluster -n clusters hosted0
NAME      VERSION   KUBECONFIG                 PROGRESS    AVAILABLE   REASON
hosted0   4.9.17    hosted0-admin-kubeconfig   Completed   True        HostedClusterAsExpected

KUBECONFIG=./hosted0-kubeconfig oc get co
NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.9.17    True        False         False      64m
csi-snapshot-controller                    4.9.17    True        False         False      71m
dns                                        4.9.17    True        False         False      71m
image-registry                             4.9.17    True        False         False      11m
ingress                                    4.9.17    True        False         False      61m
kube-apiserver                             4.9.17    True        False         False      4h32m
kube-controller-manager                    4.9.17    True        False         False      4h32m
kube-scheduler                             4.9.17    True        False         False      4h32m
kube-storage-version-migrator              4.9.17    True        False         False      71m
monitoring                                 4.9.17    True        False         False      6m51s
network                                    4.9.17    True        False         False      72m
node-tuning                                4.9.17    True        False         False      71m
openshift-apiserver                        4.9.17    True        False         False      4h32m
openshift-controller-manager               4.9.17    True        False         False      4h32m
openshift-samples                          4.9.17    True        False         False      70m
operator-lifecycle-manager                 4.9.17    True        False         False      4h32m
operator-lifecycle-manager-catalog         4.9.17    True        False         False      4h32m
operator-lifecycle-manager-packageserver   4.9.17    True        False         False      4h32m
service-ca                                 4.9.17    True        False         False      72m
storage                                    4.9.17    True        False         False      72m

KUBECONFIG=./hosted0-kubeconfig oc get clusterversion
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.9.17    True        False         8m42s   Cluster version is 4.9.17
~~~
