package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleAWSResources struct {
	KMSProviderAWSCreds *corev1.Secret
}

func (o *ExampleAWSResources) AsObjects() []crclient.Object {
	var objects []crclient.Object
	if o.KMSProviderAWSCreds != nil {
		objects = append(objects, o.KMSProviderAWSCreds)
	}
	return objects
}
