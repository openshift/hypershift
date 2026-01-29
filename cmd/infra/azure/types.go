package azure

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
)

type CreateInfraOptions struct {
	Name                        string
	BaseDomain                  string
	Location                    string
	InfraID                     string
	CredentialsFile             string
	Credentials                 *util.AzureCreds
	OutputFile                  string
	ResourceGroupName           string
	VnetID                      string
	NetworkSecurityGroupID      string
	ResourceGroupTags           map[string]string
	SubnetID                    string
	ManagedIdentitiesFile       string
	DataPlaneIdentitiesFile     string
	WorkloadIdentitiesFile      string
	AssignServicePrincipalRoles bool
	DNSZoneRG                   string
	AssignCustomHCPRoles        bool
	DisableClusterCapabilities  []string
	Cloud                       string
}

type CreateInfraOutput struct {
	BaseDomain          string                                  `json:"baseDomain"`
	PublicZoneID        string                                  `json:"publicZoneID"`
	PrivateZoneID       string                                  `json:"privateZoneID"`
	Location            string                                  `json:"region"`
	ResourceGroupName   string                                  `json:"resourceGroupName"`
	VNetID              string                                  `json:"vnetID"`
	SubnetID            string                                  `json:"subnetID"`
	BootImageID         string                                  `json:"bootImageID"`
	InfraID             string                                  `json:"infraID"`
	SecurityGroupID     string                                  `json:"securityGroupID"`
	ControlPlaneMIs     *hyperv1.AzureResourceManagedIdentities `json:"controlPlaneMIs"`
	DataPlaneIdentities hyperv1.DataPlaneManagedIdentities      `json:"dataPlaneIdentities"`
	WorkloadIdentities  *hyperv1.AzureWorkloadIdentities        `json:"workloadIdentities"`
}

// CreateIAMOptions holds options for creating Azure IAM resources (managed identities and federated credentials)
type CreateIAMOptions struct {
	Name              string
	Location          string
	InfraID           string
	CredentialsFile   string
	Credentials       *util.AzureCreds
	ResourceGroupName string
	OIDCIssuerURL     string
	OutputFile        string
	Cloud             string
}

// DestroyIAMOptions holds options for destroying Azure IAM resources
type DestroyIAMOptions struct {
	Name                   string
	InfraID                string
	WorkloadIdentitiesFile string
	CredentialsFile        string
	Credentials            *util.AzureCreds
	ResourceGroupName      string
	Cloud                  string
}
