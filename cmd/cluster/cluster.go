package cluster

import (
	"time"

	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/gcp"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"

	"github.com/spf13/cobra"
)

func NewCreateCommands(clientProviders ...*core.ClientProvider) *cobra.Command {
	opts := core.DefaultOptions()
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}

	core.BindDeveloperOptions(opts, cmd.PersistentFlags())

	cmd.MarkFlagsMutuallyExclusive("service-cidr", "default-dual")
	cmd.MarkFlagsMutuallyExclusive("cluster-cidr", "default-dual")

	cmd.AddCommand(aws.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(none.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(agent.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(azure.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(powervs.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(openstack.NewCreateCommand(opts, clientProvider))
	cmd.AddCommand(gcp.NewCreateCommand(opts, clientProvider))

	return cmd
}

func NewDestroyCommands(clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)

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
	cmd.PersistentFlags().StringVar(&opts.Kubeconfig, "kubeconfig", opts.Kubeconfig, util.KubeconfigFlagHelp)
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A cluster name (required)")
	cmd.PersistentFlags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID; inferred from the hosted cluster by default")
	cmd.PersistentFlags().BoolVar(&opts.DestroyCloudResources, "destroy-cloud-resources", opts.DestroyCloudResources, "If true, cloud resources such as load balancers and persistent storage disks created by the cluster during its lifetime are removed")
	cmd.PersistentFlags().BoolVar(&opts.ForceDestroy, "force", opts.ForceDestroy, "Dev/CI only: when the grace period expires, force-remove all finalizers from child resources and continue with best-effort infrastructure cleanup")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(aws.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(none.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(agent.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(azure.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(powervs.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(openstack.NewDestroyCommand(opts, clientProvider))
	cmd.AddCommand(gcp.NewDestroyCommand(opts, clientProvider))

	return cmd
}
