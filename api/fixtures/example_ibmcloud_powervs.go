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
	KubeCloudControllerPowerVSCreds  *corev1.Secret
	NodePoolManagementPowerVSCreds   *corev1.Secret
	ControlPlaneOperatorPowerVSCreds *corev1.Secret
}

func (o *ExamplePowerVSResources) AsObjects() []crclient.Object {
	var objects []crclient.Object
	if o.KubeCloudControllerPowerVSCreds != nil {
		objects = append(objects, o.KubeCloudControllerPowerVSCreds)
	}
	if o.NodePoolManagementPowerVSCreds != nil {
		objects = append(objects, o.NodePoolManagementPowerVSCreds)
	}
	if o.ControlPlaneOperatorPowerVSCreds != nil {
		objects = append(objects, o.ControlPlaneOperatorPowerVSCreds)
	}
	return objects
}
