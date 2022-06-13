package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExamplePowerVSOptions struct {
	ApiKey          string
	AccountID       string
	ResourceGroup   string
	Region          string
	Zone            string
	CISInstanceCRN  string
	CloudInstanceID string
	Subnet          string
	SubnetID        string
	CloudConnection string
	VpcRegion       string
	Vpc             string
	VpcSubnet       string

	// nodepool related options
	SysType    string
	ProcType   string
	Processors string
	Memory     int32
}

type ExamplePowerVSResources struct {
	KubeCloudControllerCreds  *corev1.Secret
	NodePoolManagementCreds   *corev1.Secret
	ControlPlaneOperatorCreds *corev1.Secret
	IngressOperatorCloudCreds *corev1.Secret
}

func (o *ExamplePowerVSResources) AsObjects() []crclient.Object {
	var objects []crclient.Object
	if o.KubeCloudControllerCreds != nil {
		objects = append(objects, o.KubeCloudControllerCreds)
	}
	if o.NodePoolManagementCreds != nil {
		objects = append(objects, o.NodePoolManagementCreds)
	}
	if o.ControlPlaneOperatorCreds != nil {
		objects = append(objects, o.ControlPlaneOperatorCreds)
	}
	if o.IngressOperatorCloudCreds != nil {
		objects = append(objects, o.IngressOperatorCloudCreds)
	}
	return objects
}
