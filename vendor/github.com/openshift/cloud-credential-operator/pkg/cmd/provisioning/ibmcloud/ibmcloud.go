package ibmcloud

import (
	"github.com/spf13/cobra"
)

type options struct {
	TargetDir         string
	Name              string
	CredRequestDir    string
	ResourceGroupName string
	Force             bool
	KubeConfigFile    string
	Create            bool
	EnableTechPreview bool
}

// NewIBMCloudCmd implements the "ibmcloud" subcommand for the credentials provisioning
func NewIBMCloudCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ibmcloud",
		Short: "Manage credentials objects for IBM Cloud",
		Long:  "Creating/deleting cloud credentials objects for IBM Cloud",
	}

	cmd.AddCommand(NewCreateServiceIDCmd())
	cmd.AddCommand(NewDeleteServiceIDCmd())
	cmd.AddCommand(NewRefreshKeysCmd())

	return cmd
}
