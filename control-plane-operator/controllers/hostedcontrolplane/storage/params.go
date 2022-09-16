package storage

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	utilpointer "k8s.io/utils/pointer"
)

const (
	storageOperatorImageName = "cluster-storage-operator"
)

var (
	// map env. variable in CSO Deployment -> key in `images` map (= name of the image in payload).
	operatorImageRefs = map[string]string{
		"AWS_EBS_DRIVER_OPERATOR_IMAGE":                   "aws-ebs-csi-driver-operator",
		"AWS_EBS_DRIVER_IMAGE":                            "aws-ebs-csi-driver",
		"GCP_PD_DRIVER_OPERATOR_IMAGE":                    "gcp-pd-csi-driver-operator",
		"GCP_PD_DRIVER_IMAGE":                             "gcp-pd-csi-driver",
		"OPENSTACK_CINDER_DRIVER_OPERATOR_IMAGE":          "openstack-cinder-csi-driver-operator",
		"OPENSTACK_CINDER_DRIVER_IMAGE":                   "openstack-cinder-csi-driver",
		"OVIRT_DRIVER_OPERATOR_IMAGE":                     "ovirt-csi-driver-operator",
		"OVIRT_DRIVER_IMAGE":                              "ovirt-csi-driver",
		"MANILA_DRIVER_OPERATOR_IMAGE":                    "csi-driver-manila-operator",
		"MANILA_DRIVER_IMAGE":                             "csi-driver-manila",
		"MANILA_NFS_DRIVER_IMAGE":                         "csi-driver-nfs",
		"PROVISIONER_IMAGE":                               "csi-external-provisioner",
		"ATTACHER_IMAGE":                                  "csi-external-attacher",
		"RESIZER_IMAGE":                                   "csi-external-resizer",
		"SNAPSHOTTER_IMAGE":                               "csi-external-snapshotter",
		"NODE_DRIVER_REGISTRAR_IMAGE":                     "csi-node-driver-registrar",
		"LIVENESS_PROBE_IMAGE":                            "csi-livenessprobe",
		"VSPHERE_PROBLEM_DETECTOR_OPERATOR_IMAGE":         "vsphere-problem-detector",
		"AZURE_DISK_DRIVER_OPERATOR_IMAGE":                "azure-disk-csi-driver-operator",
		"AZURE_DISK_DRIVER_IMAGE":                         "azure-disk-csi-driver",
		"AZURE_FILE_DRIVER_OPERATOR_IMAGE":                "azure-file-csi-driver-operator",
		"AZURE_FILE_DRIVER_IMAGE":                         "azure-file-csi-driver",
		"KUBE_RBAC_PROXY_IMAGE":                           "kube-rbac-proxy",
		"VMWARE_VSPHERE_DRIVER_OPERATOR_IMAGE":            "vsphere-csi-driver-operator",
		"VMWARE_VSPHERE_DRIVER_IMAGE":                     "vsphere-csi-driver",
		"VMWARE_VSPHERE_SYNCER_IMAGE":                     "vsphere-csi-driver-syncer",
		"CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE": "cluster-cloud-controller-manager-operator",
		"SHARED_RESOURCE_DRIVER_OPERATOR_IMAGE":           "csi-driver-shared-resource-operator",
		"SHARED_RESOURCE_DRIVER_IMAGE":                    "csi-driver-shared-resource",
		"SHARED_RESOURCE_DRIVER_WEBHOOK_IMAGE":            "csi-driver-shared-resource-webhook",
		"ALIBABA_DISK_DRIVER_OPERATOR_IMAGE":              "alibaba-disk-csi-driver-operator",
		"ALIBABA_CLOUD_DRIVER_IMAGE":                      "alibaba-cloud-csi-driver",
		"IBM_VPC_BLOCK_DRIVER_OPERATOR_IMAGE":             "ibm-vpc-block-csi-driver-operator",
		"IBM_VPC_BLOCK_DRIVER_IMAGE":                      "ibm-vpc-block-csi-driver",
		"IBM_VPC_NODE_LABEL_UPDATER_IMAGE":                "ibm-vpc-node-label-updater",
		"POWERVS_BLOCK_CSI_DRIVER_OPERATOR_IMAGE":         "powervs-block-csi-driver-operator",
		"POWERVS_BLOCK_CSI_DRIVER_IMAGE":                  "powervs-block-csi-driver",
		"HYPERSHIFT_IMAGE":                                "token-minter",
	}
)

type Params struct {
	OwnerRef             config.OwnerRef
	StorageOperatorImage string
	// Map env. variable -> image name
	EnvImages map[string]string

	AvailabilityProberImage string
	Version                 string
	APIPort                 *int32
	config.DeploymentConfig
}

func NewParams(
	hcp *hyperv1.HostedControlPlane,
	version string,
	images map[string]string,
	setDefaultSecurityContext bool) *Params {

	params := Params{
		OwnerRef:                config.OwnerRefFrom(hcp),
		StorageOperatorImage:    images[storageOperatorImageName],
		AvailabilityProberImage: images[util.AvailabilityProberImageName],
		EnvImages:               make(map[string]string),
		Version:                 version,
		APIPort:                 util.APIPort(hcp),
	}

	for envVar, imageRef := range operatorImageRefs {
		params.EnvImages[envVar] = images[imageRef]
	}

	params.DeploymentConfig.SetDefaultSecurityContext = setDefaultSecurityContext
	// Run only one replica of the operator
	params.DeploymentConfig.Scheduling = config.Scheduling{
		PriorityClass: config.DefaultPriorityClass,
	}
	params.DeploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))
	params.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)

	return &params
}
