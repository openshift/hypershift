package version

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/supportedversion"

	"github.com/spf13/cobra"
)

// NewVersionCommand creates a new cobra command for printing information about the HyperShift CLI version, server
// version, and supported OCP versions.
func NewVersionCommand() *cobra.Command {
	var commitOnly, clientOnly bool
	namespace := "hypershift"
	cmd := &cobra.Command{
		Use:          "version",
		Short:        "Prints HyperShift CLI version",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, args []string) {
			if commitOnly {
				fmt.Printf("%s\n", supportedversion.GetRevision())
				return
			}
			fmt.Printf("Client Version: %s\n", supportedversion.String())
			if clientOnly {
				return
			}

			supportedVersions, serverVersion, err := supportedversion.GetSupportedOCPVersions(cmd.Context(), namespace)
			if err != nil {
				fmt.Printf("failed to get supported OCP versions: %v\n", err)
				return
			}
			fmt.Printf("Server Version: %s\n", serverVersion)
			fmt.Printf("Server Supports OCP Versions: %s\n", strings.Join(supportedVersions.Versions, ", "))
		},
	}

	cmd.Flags().BoolVar(&commitOnly, "commit-only", commitOnly, "Output only the code commit")
	cmd.Flags().BoolVar(&clientOnly, "client-only", clientOnly, "Output only the client version")
	cmd.Flags().StringVar(&namespace, "namespace", namespace, "The namespace in which HyperShift is installed")

	return cmd
}
