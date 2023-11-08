package fixtures

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

type ExampleAWSOptions struct {
	Region                  string
	Zones                   []ExampleAWSOptionsZones
	VPCID                   string
	SecurityGroupID         string
	InstanceProfile         string
	InstanceType            string
	Roles                   hyperv1.AWSRolesRef
	KMSProviderRoleARN      string
	KMSKeyARN               string
	RootVolumeSize          int64
	RootVolumeType          string
	RootVolumeIOPS          int64
	RootVolumeEncryptionKey string
	ResourceTags            []hyperv1.AWSResourceTag
	EndpointAccess          string
	ProxyAddress            string
}

type ExampleAWSOptionsZones struct {
	Name     string
	SubnetID *string
}
