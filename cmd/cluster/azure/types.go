package azure

import (
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/util"
)

// RawCreateOptions is the raw options for the Azure create cluster command
type RawCreateOptions struct {
	CredentialsFile                  string
	Location                         string
	EncryptionKeyID                  string
	AvailabilityZones                []string
	ResourceGroupName                string
	VnetID                           string
	NetworkSecurityGroupID           string
	ResourceGroupTags                map[string]string
	SubnetID                         string
	RHCOSImage                       string
	KMSUserAssignedCredsSecretName   string
	TechPreviewEnabled               bool
	DNSZoneRGName                    string
	ManagedIdentitiesFile            string
	DataPlaneIdentitiesFile          string
	AssignServicePrincipalRoles      bool
	AssignCustomHCPRoles             bool
	IssuerURL                        string
	ServiceAccountTokenIssuerKeyPath string
	MultiArch                        bool

	NodePoolOpts *azurenodepool.RawAzurePlatformCreateOptions
}

// AzureEncryptionKey contains the information about the encryption key for the HostedCluster to
// be used for etcd encryption with KMSv2
type AzureEncryptionKey struct {
	KeyVaultName string
	KeyName      string
	KeyVersion   string
}

// ValidatedCreateOptions is the validated options for the Azure create cluster command
type ValidatedCreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedCreateOptions
}

// validatedCreateOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedCreateOptions struct {
	*RawCreateOptions

	*azurenodepool.ValidatedAzurePlatformCreateOptions
}

// completedCreateOptions is a private wrapper that enforces a call of Complete() before cluster creation can be invoked.
type completedCreateOptions struct {
	*ValidatedCreateOptions

	externalDNSDomain string
	name, namespace   string

	infra         *azureinfra.CreateInfraOutput
	encryptionKey *AzureEncryptionKey
	creds         util.AzureCreds
}

// CreateOptions is the options for the Azure create cluster command
type CreateOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedCreateOptions
}
