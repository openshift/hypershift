package cluster

import (
	"time"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/agent"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/aws"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/azure"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/openstack"

	"github.com/spf13/cobra"
)

func NewCreateCommands(clientProviders ...*core.ClientProvider) *cobra.Command {
	opts := core.DefaultOptions()
	opts.ControlPlaneAvailabilityPolicy = string(v1beta1.HighlyAvailable)
	clientProvider := core.ResolveClientProvider(clientProviders...)

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}

	core.BindOptions(opts, cmd.PersistentFlags())

	cmd.MarkFlagsMutuallyExclusive("service-cidr", "default-dual")
	cmd.MarkFlagsMutuallyExclusive("cluster-cidr", "default-dual")
	cmd.AddCommand(agent.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(aws.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(azure.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(openstack.NewCreateCommand(opts, clientProvider))

	return cmd
}

func NewDestroyCommands(clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)

	opts := &core.DestroyOptions{
		ClusterGracePeriod:    10 * time.Minute,
		DestroyCloudResources: true,
		Log:                   log.Log,
		Name:                  "",
		Namespace:             "clusters",
	}

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Destroys a HostedCluster and its associated infrastructure.",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "Period of time to wait for the HostedCluster to be deleted before forcibly destroying its infrastructure.")
	cmd.PersistentFlags().BoolVar(&opts.DestroyCloudResources, "destroy-cloud-resources", opts.DestroyCloudResources, "If true, cloud resources, such as load balancers and persistent storage disks, created by the HostedCluster during its lifetime are removed.")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "The HostedCluster's infrastructure ID. This is inferred from the HostedCluster by default.")
	cmd.PersistentFlags().StringVar(&opts.Kubeconfig, "kubeconfig", opts.Kubeconfig, util.KubeconfigFlagHelp)
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The HostedCluster's name.")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The HostedCluster's namespace name.")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(agent.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(aws.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(azure.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(openstack.NewDestroyCommand(opts, clientProvider))

	return cmd
}
