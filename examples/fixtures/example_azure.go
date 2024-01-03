package fixtures

import "github.com/openshift/hypershift/cmd/util"

type ExampleAzureOptions struct {
	Creds               util.AzureCreds
	Location            string
	ResourceGroupName   string
	VnetName            string
	VnetID              string
	SubnetName          string
	BootImageID         string
	MachineIdentityID   string
	InstanceType        string
	SecurityGroupName   string
	DiskSizeGB          int32
	AvailabilityZones   []string
	DiskEncryptionSetID string
	EncryptionKey       *AzureEncryptionKey
}

type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}
