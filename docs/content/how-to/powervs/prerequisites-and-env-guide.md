# Prerequisites

## Generic
* The HyperShift CLI (`hypershift`).

    Install it using Go 1.18:
        ```shell linenums="1"
        git clone https://github.com/openshift/hypershift.git
        cd hypershift
        make build
        sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift
        ```

* Admin access to an OpenShift cluster (version 4.8+) specified by the `KUBECONFIG` environment variable.
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://console.redhat.com/openshift/install/ibm-cloud) file for the `quay.io/openshift-release-dev` repository.

# Install Hypershift
Install HyperShift into the management cluster.
Once Hypershift CLI and management cluster is ready, run below command to install Hypershift operator and CRDs which are required to setup the cluster.

```
hypershift install
```

## Authentication
There are two ways to set up authentication

* Authenticate IBM Cloud Clients by setting the `IBMCLOUD_API_KEY` environment var to your API Key.
* Authenticate IBM Cloud Clients by setting the `IBMCLOUD_CREDENTIALS` environment var pointing to a file containing your API Key.

## Authorization:

API Key used should have below services with respective roles for hypershift cluster to get created in IBM Cloud.

| Service                                    | Roles                                                   |
|--------------------------------------------|---------------------------------------------------------|
| Workspace for Power Systems Virtual Server | Manager, Administrator                                  |
| VPC Infrastructure Services                | Manager, Administrator                                  |
| Internet Services                          | Manager, Administrator                                  |
| Direct Link                                | Viewer                                                  |
| IAM Identity Service                       | User API key creator, Service ID creator, Administrator |
| All account management services            | Administrator                                           |
| All Identity and Access enabled services   | Manager, Editor                                         |
| Cloud Object Storage                       | Manager, Administrator                                  |
| Transit Gateway                            | Manager, Editor                                         |


## Base Domain
Need to have existing CIS Domain in [IBM Cloud Internet Services](https://cloud.ibm.com/docs/cis) which can be used as a `BASEDOMAIN` while creating the cluster.

## Region and Zones
Refer [this](https://cluster-api-ibmcloud.sigs.k8s.io/reference/regions-zones-mapping.html) to get possible region and zone values. Substitute those with `REGION` `ZONE` `VPC_REGION` and `TRANSIT_GATEWAY_LOCATION` while creating the cluster.

## Release Image
Use [this](https://multi.ocp.releases.ci.openshift.org) to get latest multi arch nightly build as release image. Substitute it with `RELEASE_IMAGE` while creating the cluster.

## Custom Endpoints
Use following environment variables to set custom endpoint.
```
IBMCLOUD_POWER_API_ENDPOINT    - to setup PowerVS custom endpoint
IBMCLOUD_VPC_API_ENDPOINT      - to setup VPC custom endpoint
IBMCLOUD_PLATFORM_API_ENDPOINT - to setup platform services custom endpoint
IBMCLOUD_COS_API_ENDPOINT      - to setup COS custom endpoint, can use this to set up custom endpoints mentioned here https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-endpoints#endpoints-region 
```