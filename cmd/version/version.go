package version

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/cmd/util"
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
			out := cmd.OutOrStdout()
			if commitOnly {
				fmt.Fprintf(out, "%s\n", supportedversion.GetRevision())
				return
			}
			fmt.Fprintf(out, "Client Version: %s\n", supportedversion.String())
			if clientOnly {
				return
			}

			client, err := util.GetClient()
			if err != nil {
				fmt.Fprintf(out, "failed to connect to server: %v", err)
				return
			}

			supportedVersions, serverVersion, err := supportedversion.GetSupportedOCPVersions(cmd.Context(), namespace, client, nil)
			if err != nil {
				fmt.Fprintf(out, "failed to get supported OCP versions: %v\n", err)
				return
			}
			fmt.Fprintf(out, "Server Version: %s\n", serverVersion)
			fmt.Fprintf(out, "Server Supports OCP Versions: %s\n", strings.Join(supportedVersions.Versions, ", "))
		},
	}

	cmd.Flags().BoolVar(&commitOnly, "commit-only", commitOnly, "Output only the code commit")
	cmd.Flags().BoolVar(&clientOnly, "client-only", clientOnly, "Output only the client version")
	cmd.Flags().StringVar(&namespace, "namespace", namespace, "The namespace in which HyperShift is installed")

	return cmd
}
