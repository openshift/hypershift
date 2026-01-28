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

func NewCreateCommands() *cobra.Command {
	opts := core.DefaultOptions()
	var kubeconfigPath string

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return util.SetKubeconfig(kubeconfigPath)
		},
	}

	core.BindDeveloperOptions(opts, cmd.PersistentFlags())
	cmd.PersistentFlags().StringVar(&kubeconfigPath, "kubeconfig", "", "Path to the kubeconfig file for the management cluster. If not set, uses the KUBECONFIG environment variable or the default kubeconfig")

	cmd.MarkFlagsMutuallyExclusive("service-cidr", "default-dual")
	cmd.MarkFlagsMutuallyExclusive("cluster-cidr", "default-dual")

	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(none.NewCreateCommand(opts))
	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(azure.NewCreateCommand(opts))
	cmd.AddCommand(powervs.NewCreateCommand(opts))
	cmd.AddCommand(openstack.NewCreateCommand(opts))
	cmd.AddCommand(gcp.NewCreateCommand(opts))

	return cmd
}

func NewDestroyCommands() *cobra.Command {
	var kubeconfigPath string

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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return util.SetKubeconfig(kubeconfigPath)
		},
	}

	cmd.PersistentFlags().StringVar(&kubeconfigPath, "kubeconfig", "", "Path to the kubeconfig file for the management cluster. If not set, uses the KUBECONFIG environment variable or the default kubeconfig")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A cluster name (required)")
	cmd.PersistentFlags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID; inferred from the hosted cluster by default")
	cmd.PersistentFlags().BoolVar(&opts.DestroyCloudResources, "destroy-cloud-resources", opts.DestroyCloudResources, "If true, cloud resources such as load balancers and persistent storage disks created by the cluster during its lifetime are removed")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(aws.NewDestroyCommand(opts))
	cmd.AddCommand(none.NewDestroyCommand(opts))
	cmd.AddCommand(agent.NewDestroyCommand(opts))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts))
	cmd.AddCommand(azure.NewDestroyCommand(opts))
	cmd.AddCommand(powervs.NewDestroyCommand(opts))
	cmd.AddCommand(openstack.NewDestroyCommand(opts))
	cmd.AddCommand(gcp.NewDestroyCommand(opts))

	return cmd
}
