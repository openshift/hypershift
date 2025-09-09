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
	RHCOSImage                  string
	ResourceGroupName           string
	VnetID                      string
	NetworkSecurityGroupID      string
	ResourceGroupTags           map[string]string
	SubnetID                    string
	ManagedIdentitiesFile       string
	DataPlaneIdentitiesFile     string
	AssignServicePrincipalRoles bool
	DNSZoneRG                   string
	AssignCustomHCPRoles        bool
	DisableClusterCapabilities  []string
	OIDCIssuerURL               string
	GenerateManagedIdentities   bool
	WorkloadIdentitiesOutputFile string
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
}

