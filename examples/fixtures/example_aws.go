package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleAWSResources struct {
	KubeCloudControllerAWSCreds  *corev1.Secret
	NodePoolManagementAWSCreds   *corev1.Secret
	ControlPlaneOperatorAWSCreds *corev1.Secret
	KMSProviderAWSCreds          *corev1.Secret
}

func (o *ExampleAWSResources) AsObjects() []crclient.Object {
	var objects []crclient.Object
	if o.KubeCloudControllerAWSCreds != nil {
		objects = append(objects, o.KubeCloudControllerAWSCreds)
	}
	if o.NodePoolManagementAWSCreds != nil {
		objects = append(objects, o.NodePoolManagementAWSCreds)
	}
	if o.ControlPlaneOperatorAWSCreds != nil {
		objects = append(objects, o.ControlPlaneOperatorAWSCreds)
	}
	if o.KMSProviderAWSCreds != nil {
		objects = append(objects, o.KMSProviderAWSCreds)
	}
	return objects
}
