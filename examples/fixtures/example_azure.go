package fixtures

import "github.com/openshift/hypershift/cmd/util"

type ExampleAzureOptions struct {
	Creds                  util.AzureCreds
	Location               string
	ResourceGroupName      string
	VnetID                 string
	SubnetID               string
	BootImageID            string
	MachineIdentityID      string
	InstanceType           string
	SecurityGroupID        string
	DiskSizeGB             int32
	AvailabilityZones      []string
	DiskEncryptionSetID    string
	EnableEphemeralOSDisk  bool
	DiskStorageAccountType string
	EncryptionKey          *AzureEncryptionKey
	MultiArch              bool
}

type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}
