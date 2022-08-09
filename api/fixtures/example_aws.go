package fixtures

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleAWSOptions struct {
	Region             string
	Zones              []ExampleAWSOptionsZones
	VPCID              string
	SecurityGroupID    string
	InstanceProfile    string
	InstanceType       string
	Roles              hyperv1.AWSRolesRef
	KMSProviderRoleARN string
	KMSKeyARN          string
	RootVolumeSize     int64
	RootVolumeType     string
	RootVolumeIOPS     int64
	ResourceTags       []hyperv1.AWSResourceTag
	EndpointAccess     string
	ProxyAddress       string
}

type ExampleAWSOptionsZones struct {
	Name     string
	SubnetID *string
}

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
