package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExamplePowerVSOptions struct {
	ApiKey                 string
	AccountID              string
	ResourceGroup          string
	PowerVSRegion          string
	PowerVSZone            string
	PowerVSCloudInstanceID string
	PowerVSSubnetID        string
	PowerVSCloudConnection string
	VpcRegion              string
	Vpc                    string
	VpcSubnet              string

	// nodepool related options
	SysType    string
	ProcType   string
	Processors string
	Memory     string
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
