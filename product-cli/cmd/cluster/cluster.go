package cluster

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/kubevirt"
)

func NewCreateCommands() *cobra.Command {
	opts := &core.CreateOptions{
		Namespace:                      "clusters",
		Name:                           "example",
		ReleaseImage:                   "",
		PullSecretFile:                 "",
		ControlPlaneAvailabilityPolicy: "HighlyAvailable",
		Render:                         false,
		InfraID:                        "",
		ServiceCIDR:                    "172.31.0.0/16",
		ClusterCIDR:                    "10.132.0.0/14",
		Wait:                           false,
		Timeout:                        0,
		ExternalDNSDomain:              "",
		AdditionalTrustBundle:          "",
		ImageContentSources:            "",
		NodeSelector:                   nil,
		Log:                            log.Log,
		NodeDrainTimeout:               0,
		NodeUpgradeType:                v1beta1.UpgradeTypeReplace,
		Arch:                           "amd64",
	}
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}

	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))

	return cmd
}
