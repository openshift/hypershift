## Prerequisites

* Set up the [authentication](#setting-up-ibm-cloud-authentication) by following this section
* `CIS Domain` Need to have existing CIS Domain in [IBM Cloud Internet Services](https://cloud.ibm.com/docs/cis) which can be used as a `BASEDOMAIN` in create part

PowerVS and VPC region zone's possible values can be found [here](https://cluster-api-ibmcloud.sigs.k8s.io/reference/regions-zones-mapping.html)

If you want to set up custom endpoints, please see this [section](#setting-custom-endpoints-for-ibm-cloud-services)

### Setting up IBM Cloud Authentication
There are two ways to set up authentication
- Authenticate IBM Cloud Clients by setting the `IBMCLOUD_API_KEY` environment var to your API Key.
- Authenticate IBM Cloud Clients by setting the `IBMCLOUD_CREDENTIALS` environment var pointing to a file containing your API Key.


### Setting custom endpoints for IBM Cloud services
Set the following environment var to set the custom endpoint.
```
IBMCLOUD_POWER_API_ENDPOINT - to setup PowerVS custom endpoint
IBMCLOUD_VPC_API_ENDPOINT - to setup VPC custom endpoint
IBMCLOUD_PLATFORM_API_ENDPOINT - to setup platform services custom endpoint
```