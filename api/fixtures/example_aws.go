package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type ExampleAWSResources struct {
	KubeCloudControllerAWSCreds  *corev1.Secret
	NodePoolManagementAWSCreds   *corev1.Secret
	ControlPlaneOperatorAWSCreds *corev1.Secret
	KMSProviderAWSCreds          *corev1.Secret
}

func (o *ExampleAWSResources) AsObjects() []runtime.Object {
	var objects []runtime.Object
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
