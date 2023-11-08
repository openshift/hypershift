package fixtures

type ExampleAzureOptions struct {
	Creds             AzureCreds
	Location          string
	ResourceGroupName string
	VnetName          string
	VnetID            string
	SubnetName        string
	BootImageID       string
	MachineIdentityID string
	InstanceType      string
	SecurityGroupName string
	DiskSizeGB        int32
	AvailabilityZones []string
}

// AzureCreds is the fileformat we expect for credentials. It is copied from the installer
// to allow using the same crededentials file for both:
// https://github.com/openshift/installer/blob/8fca1ade5b096d9b2cd312c4599881d099439288/pkg/asset/installconfig/azure/session.go#L36
type AzureCreds struct {
	SubscriptionID string `json:"subscriptionId,omitempty"`
	ClientID       string `json:"clientId,omitempty"`
	ClientSecret   string `json:"clientSecret,omitempty"`
	TenantID       string `json:"tenantId,omitempty"`
}
