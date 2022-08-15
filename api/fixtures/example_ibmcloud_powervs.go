package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

type ExamplePowerVSOptions struct {
	AccountID       string
	ResourceGroup   string
	Region          string
	Zone            string
	CISInstanceCRN  string
	CloudInstanceID string
	Subnet          string
	SubnetID        string
	CloudConnection string
	VPCRegion       string
	VPC             string
	VPCSubnet       string
	Resources       ExamplePowerVSResources

	// nodepool related options
	SysType    string
	ProcType   hyperv1.PowerVSNodePoolProcType
	Processors string
	Memory     int32
}

type ExamplePowerVSResources struct {
	KubeCloudControllerCreds  *corev1.Secret
	NodePoolManagementCreds   *corev1.Secret
	IngressOperatorCloudCreds *corev1.Secret
	StorageOperatorCloudCreds *corev1.Secret
}

func (o *ExamplePowerVSResources) AsObjects() []crclient.Object {
	var objects []crclient.Object
	if o.KubeCloudControllerCreds != nil {
		objects = append(objects, o.KubeCloudControllerCreds)
	}
	if o.NodePoolManagementCreds != nil {
		objects = append(objects, o.NodePoolManagementCreds)
	}
	if o.IngressOperatorCloudCreds != nil {
		objects = append(objects, o.IngressOperatorCloudCreds)
	}
	if o.StorageOperatorCloudCreds != nil {
		objects = append(objects, o.StorageOperatorCloudCreds)
	}
	return objects
}
