package cluster

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/spf13/cobra"
)

func NewCreateCommands() *cobra.Command {
	opts := &core.CreateOptions{
		Namespace:                      "clusters",
		Name:                           "example",
		ReleaseImage:                   "",
		PullSecretFile:                 "",
		ControlPlaneAvailabilityPolicy: "SingleReplica",
		Render:                         false,
		NetworkType:                    string(hyperv1.OpenShiftSDN),
		InfrastructureJSON:             "",
		InfraID:                        "",
	}
	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.PersistentFlags().StringVar(&opts.NetworkType, "network-type", opts.NetworkType, "Enum specifying the cluster SDN provider. Supports either Calico or OpenshiftSDN.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "Path to a pull secret (required)")
	cmd.PersistentFlags().StringVar(&opts.ControlPlaneAvailabilityPolicy, "control-plane-availability-policy", opts.ControlPlaneAvailabilityPolicy, "Availability policy for hosted cluster components. Supported options: SingleReplica, HighlyAvailable")
	cmd.PersistentFlags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")
	cmd.PersistentFlags().StringVar(&opts.ControlPlaneOperatorImage, "control-plane-operator-image", opts.ControlPlaneOperatorImage, "Override the default image used to deploy the control plane operator")
	cmd.PersistentFlags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.PersistentFlags().Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If >-1, create a default NodePool with this many replicas")
	cmd.PersistentFlags().StringArrayVar(&opts.Annotations, "annotations", opts.Annotations, "Annotations to apply to the hostedcluster (key=value). Can be specified multiple times.")
	cmd.PersistentFlags().BoolVar(&opts.FIPS, "fips", opts.FIPS, "Enables FIPS mode for nodes in the cluster")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine autorepair with machine health checks")
	cmd.PersistentFlags().StringVar(&opts.InfrastructureAvailabilityPolicy, "infra-availability-policy", opts.InfrastructureAvailabilityPolicy, "Availability policy for infrastructure services in guest cluster. Supported options: SingleReplica, HighlyAvailable")
	cmd.PersistentFlags().BoolVar(&opts.GenerateSSH, "generate-ssh", opts.GenerateSSH, "If true, generate SSH keys")
	cmd.PersistentFlags().StringVar(&opts.EtcdStorageClass, "etcd-storage-class", opts.EtcdStorageClass, "The persistent volume storage class for etcd data volumes")
	cmd.PersistentFlags().StringVar(&opts.InfrastructureJSON, "infra-json", opts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for hosted cluster resources.")

	cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(none.NewCreateCommand(opts))

	return cmd
}

func NewDestroyCommands() *cobra.Command {

	opts := &core.DestroyOptions{
		Namespace:          "clusters",
		Name:               "",
		ClusterGracePeriod: 10 * time.Minute,
	}

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Destroys a HostedCluster and its associated infrastructure.",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A cluster namespace")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A cluster name (required)")
	cmd.PersistentFlags().DurationVar(&opts.ClusterGracePeriod, "cluster-grace-period", opts.ClusterGracePeriod, "How long to wait for the cluster to be deleted before forcibly destroying its infra")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID; inferred from the hosted cluster by default")

	cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(aws.NewDestroyCommand(opts))
	cmd.AddCommand(none.NewDestroyCommand(opts))

	return cmd
}
