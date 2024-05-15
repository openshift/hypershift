package cluster

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/agent"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/aws"
	"github.com/openshift/hypershift/product-cli/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/support/globalconfig"
)

func NewCreateCommands() *cobra.Command {
	opts := &core.CreateOptions{
		AdditionalTrustBundle:          "",
		Arch:                           "amd64",
		ClusterCIDR:                    []string{globalconfig.DefaultIPv4ClusterCIDR},
		ControlPlaneAvailabilityPolicy: "HighlyAvailable",
		ImageContentSources:            "",
		InfraID:                        "",
		Log:                            log.Log,
		Name:                           "example",
		Namespace:                      "clusters",
		NodeDrainTimeout:               0,
		NodeSelector:                   nil,
		NodeUpgradeType:                "",
		PullSecretFile:                 "",
		ReleaseImage:                   "",
		Render:                         false,
		ServiceCIDR:                    []string{globalconfig.DefaultIPv4ServiceCIDR},
		DefaultDual:                    false,
		Timeout:                        0,
		Wait:                           false,
		PausedUntil:                    "",
		OLMCatalogPlacement:            v1beta1.ManagementOLMCatalogPlacement,
		OLMDisableDefaultSources:       false,
		NetworkType:                    string(v1beta1.OVNKubernetes),
	}

	cmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Creates basic functional HostedCluster resources",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVar(&opts.AdditionalTrustBundle, "additional-trust-bundle", opts.AdditionalTrustBundle, "Filepath to a file with a user CA bundle.")
	cmd.PersistentFlags().StringArrayVar(&opts.Annotations, "annotations", opts.Annotations, "Annotations to apply to the HostedCluster (format: key=value). Annotations can be specified multiple times.")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine auto-repair with machine health checks.")
	cmd.PersistentFlags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "Ingress base domain for the cluster.")
	cmd.PersistentFlags().StringArrayVar(&opts.ClusterCIDR, "cluster-cidr", opts.ClusterCIDR, "CIDR of the cluster network. Can be specified multiple times.")
	cmd.PersistentFlags().StringVar(&opts.ControlPlaneAvailabilityPolicy, "control-plane-availability-policy", opts.ControlPlaneAvailabilityPolicy, "Availability policy for HostedCluster components. Supported options: SingleReplica, HighlyAvailable.")
	cmd.PersistentFlags().StringVar(&opts.EtcdStorageClass, "etcd-storage-class", opts.EtcdStorageClass, "Persistent volume storage class for etcd data volumes")
	cmd.PersistentFlags().BoolVar(&opts.FIPS, "fips", opts.FIPS, "Enables FIPS mode for nodes in the cluster.")
	cmd.PersistentFlags().BoolVar(&opts.GenerateSSH, "generate-ssh", opts.GenerateSSH, "If true, generates an SSH key that can be used to access the nodes in a NodePool.")
	cmd.PersistentFlags().StringVar(&opts.ImageContentSources, "image-content-sources", opts.ImageContentSources, "Filepath to a file with image content sources.")
	cmd.PersistentFlags().StringVar(&opts.InfrastructureAvailabilityPolicy, "infra-availability-policy", opts.InfrastructureAvailabilityPolicy, "Availability policy for infrastructure services in guest cluster. Supported options: SingleReplica, HighlyAvailable.")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID of the HostedCluster. Inferred from the HostedCluster by default.")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The HostedCluster's name.")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The HostedCluster's namespace name.")
	cmd.PersistentFlags().DurationVar(&opts.NodeDrainTimeout, "node-drain-timeout", opts.NodeDrainTimeout, "The NodeDrainTimeout for any created NodePools.")
	cmd.PersistentFlags().Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If set to 0 or greater, NodePools will be created with that many replicas. If set to less than 0, no NodePools will be created.")
	cmd.PersistentFlags().StringToStringVar(&opts.NodeSelector, "node-selector", opts.NodeSelector, "A comma separated list of key=value pairs to use as the node selector for the Hosted Control Plane pods to stick to. (e.g. role=cp,disk=fast)")
	cmd.PersistentFlags().Var(&opts.NodeUpgradeType, "node-upgrade-type", "The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace")
	cmd.PersistentFlags().Var(&opts.OLMCatalogPlacement, "olm-catalog-placement", "The OLM Catalog Placement for the HostedCluster. Supported options: Management, Guest")
	cmd.PersistentFlags().BoolVar(&opts.OLMDisableDefaultSources, "olm-disable-default-sources", opts.OLMDisableDefaultSources, "Disables the OLM default catalog sources for the HostedCluster.")
	cmd.PersistentFlags().StringVar(&opts.NetworkType, "network-type", opts.NetworkType, "Enum specifying the cluster SDN provider. Supports either Calico, OVNKubernetes, OpenShiftSDN or Other.")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "Filepath to a pull secret.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the HostedCluster.")
	cmd.PersistentFlags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML instead of applying")
	cmd.PersistentFlags().StringVar(&opts.RenderInto, "render-into", opts.RenderInto, "Render output as YAML into this file instead of applying. If unset, YAML will be output to stdout.")
	cmd.PersistentFlags().StringArrayVar(&opts.ServiceCIDR, "service-cidr", opts.ServiceCIDR, "The CIDR of the service network. Can be specified multiple times.")
	cmd.PersistentFlags().BoolVar(&opts.DefaultDual, "default-dual", opts.DefaultDual, "Defines the Service and Cluster CIDRs as dual-stack default values. This flag is ignored if service-cidr or cluster-cidr are set. Cannot be defined with service-cidr or cluster-cidr flag.")
	cmd.PersistentFlags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Filepath to an SSH key file.")
	cmd.PersistentFlags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "If the --wait flag is set, set the optional timeout to limit the duration of the wait (Examples: 30s, 1h30m45s, etc.) 0 means no timeout.")
	cmd.PersistentFlags().BoolVar(&opts.Wait, "wait", opts.Wait, "If true, the create command will block until the HostedCluster is up. Requires at least one NodePool with at least one node.")
	cmd.PersistentFlags().StringVar(&opts.PausedUntil, "pausedUntil", opts.PausedUntil, "If a date is provided in RFC3339 format, HostedCluster creation is paused until that date. If the boolean true is provided, HostedCluster creation is paused until the field is removed.")
	cmd.PersistentFlags().StringVar(&opts.Arch, "arch", opts.Arch, "The default processor architecture for the NodePool (e.g. arm64, amd64)")

	cmd.MarkFlagsMutuallyExclusive("service-cidr", "default-dual")
	cmd.MarkFlagsMutuallyExclusive("cluster-cidr", "default-dual")
	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))

	return cmd
}

func NewDestroyCommands() *cobra.Command {

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
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The HostedCluster's name.")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The HostedCluster's namespace name.")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(agent.NewDestroyCommand(opts))
	cmd.AddCommand(aws.NewDestroyCommand(opts))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts))

	return cmd
}
