package cluster

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/agent"
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

	cmd.PersistentFlags().Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If 0 or greater, creates a nodepool with that many replicas; else if less than 0, does not create a nodepool.")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "File path to a pull secret.")

	_ = cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))

	return cmd
}

func NewDestroyCommands() *cobra.Command {

	opts := &core.DestroyOptions{
		Namespace:             "clusters",
		Name:                  "",
		ClusterGracePeriod:    10 * time.Minute,
		Log:                   log.Log,
		DestroyCloudResources: true,
	}

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Destroys a HostedCluster and its associated infrastructure.",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A cluster name (required)")
	cmd.PersistentFlags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID inferred from the hosted cluster by default")
	cmd.PersistentFlags().BoolVar(&opts.DestroyCloudResources, "destroy-cloud-resources", opts.DestroyCloudResources, "If true, cloud resources such as load balancers and persistent storage disks created by the cluster during its lifetime are removed")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(agent.NewDestroyCommand(opts))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts))

	return cmd
}
