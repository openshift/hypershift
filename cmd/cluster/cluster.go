package cluster

import (
	"time"

	"github.com/openshift/hypershift/api/v1beta1"

	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	"github.com/openshift/hypershift/cmd/log"
)

func NewCreateCommands() *cobra.Command {
	opts := &core.CreateOptions{
		Namespace:                      "clusters",
		Name:                           "example",
		ReleaseImage:                   "",
		ReleaseStream:                  "",
		PullSecretFile:                 "",
		ControlPlaneAvailabilityPolicy: "SingleReplica",
		Render:                         false,
		InfrastructureJSON:             "",
		InfraID:                        "",
		ServiceCIDR:                    []string{"172.31.0.0/16"},
		ClusterCIDR:                    []string{"10.132.0.0/14"},
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

	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.PersistentFlags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.PersistentFlags().StringVar(&opts.BaseDomainPrefix, "base-domain-prefix", opts.BaseDomainPrefix, "The ingress base domain prefix for the cluster, defaults to cluster name. Use 'none' for an empty prefix")
	cmd.PersistentFlags().StringVar(&opts.ExternalDNSDomain, "external-dns-domain", opts.ExternalDNSDomain, "Sets hostname to opinionated values in the specificed domain for services with publishing type LoadBalancer or Route.")
	cmd.PersistentFlags().StringVar(&opts.NetworkType, "network-type", opts.NetworkType, "Enum specifying the cluster SDN provider. Supports either Calico, OVNKubernetes, OpenShiftSDN or Other.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The OCP release image for the cluster")
	cmd.PersistentFlags().StringVar(&opts.ReleaseStream, "release-stream", opts.ReleaseStream, "The OCP release stream for the cluster (e.g. 4.14.0-0.nightly), this flag is ignored if release-image is set")
	cmd.PersistentFlags().StringVar(&opts.PullSecretFile, "pull-secret", opts.PullSecretFile, "File path to a pull secret.")
	cmd.PersistentFlags().StringVar(&opts.ControlPlaneAvailabilityPolicy, "control-plane-availability-policy", opts.ControlPlaneAvailabilityPolicy, "Availability policy for hosted cluster components. Supported options: SingleReplica, HighlyAvailable")
	cmd.PersistentFlags().BoolVar(&opts.Render, "render", opts.Render, "Render output as YAML to stdout instead of applying")
	cmd.PersistentFlags().StringVar(&opts.ControlPlaneOperatorImage, "control-plane-operator-image", opts.ControlPlaneOperatorImage, "Override the default image used to deploy the control plane operator")
	cmd.PersistentFlags().StringVar(&opts.SSHKeyFile, "ssh-key", opts.SSHKeyFile, "Path to an SSH key file")
	cmd.PersistentFlags().StringVar(&opts.AdditionalTrustBundle, "additional-trust-bundle", opts.AdditionalTrustBundle, "Path to a file with user CA bundle")
	cmd.PersistentFlags().StringVar(&opts.ImageContentSources, "image-content-sources", opts.ImageContentSources, "Path to a file with image content sources")
	cmd.PersistentFlags().Int32Var(&opts.NodePoolReplicas, "node-pool-replicas", opts.NodePoolReplicas, "If 0 or greater, creates a nodepool with that many replicas; else if less than 0, does not create a nodepool.")
	cmd.PersistentFlags().DurationVar(&opts.NodeDrainTimeout, "node-drain-timeout", opts.NodeDrainTimeout, "The NodeDrainTimeout on any created NodePools")
	cmd.PersistentFlags().StringArrayVar(&opts.Annotations, "annotations", opts.Annotations, "Annotations to apply to the hostedcluster (key=value). Can be specified multiple times.")
	cmd.PersistentFlags().BoolVar(&opts.FIPS, "fips", opts.FIPS, "Enables FIPS mode for nodes in the cluster")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine autorepair with machine health checks")
	cmd.PersistentFlags().StringVar(&opts.InfrastructureAvailabilityPolicy, "infra-availability-policy", opts.InfrastructureAvailabilityPolicy, "Availability policy for infrastructure services in guest cluster. Supported options: SingleReplica, HighlyAvailable")
	cmd.PersistentFlags().BoolVar(&opts.GenerateSSH, "generate-ssh", opts.GenerateSSH, "If true, generate SSH keys")
	cmd.PersistentFlags().StringVar(&opts.EtcdStorageClass, "etcd-storage-class", opts.EtcdStorageClass, "The persistent volume storage class for etcd data volumes")
	cmd.PersistentFlags().StringVar(&opts.InfrastructureJSON, "infra-json", opts.InfrastructureJSON, "Path to file containing infrastructure information for the cluster. If not specified, infrastructure will be created")
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID to use for hosted cluster resources.")
	cmd.PersistentFlags().StringArrayVar(&opts.ServiceCIDR, "service-cidr", opts.ServiceCIDR, "The CIDR of the service network. Can be specified multiple times.")
	cmd.PersistentFlags().StringArrayVar(&opts.ClusterCIDR, "cluster-cidr", opts.ClusterCIDR, "The CIDR of the cluster network. Can be specified multiple times.")
	cmd.PersistentFlags().StringToStringVar(&opts.NodeSelector, "node-selector", opts.NodeSelector, "A comma separated list of key=value to use as node selector for the Hosted Control Plane pods to stick to. E.g. role=cp,disk=fast")
	cmd.PersistentFlags().BoolVar(&opts.Wait, "wait", opts.Wait, "If the create command should block until the cluster is up. Requires at least one node.")
	cmd.PersistentFlags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "If the --wait flag is set, set the optional timeout to limit the waiting duration. The format is duration; e.g. 30s or 1h30m45s; 0 means no timeout; default = 0")
	cmd.PersistentFlags().Var(&opts.NodeUpgradeType, "node-upgrade-type", "The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace")
	cmd.PersistentFlags().StringVar(&opts.Arch, "arch", opts.Arch, "The default processor architecture for the NodePool (e.g. arm64, amd64)")
	cmd.PersistentFlags().StringVar(&opts.PausedUntil, "pausedUntil", opts.PausedUntil, "If a date is provided in RFC3339 format, HostedCluster creation is paused until that date. If the boolean true is provided, HostedCluster creation is paused until the field is removed.")

	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(none.NewCreateCommand(opts))
	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(azure.NewCreateCommand(opts))
	cmd.AddCommand(powervs.NewCreateCommand(opts))

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
	cmd.PersistentFlags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Infrastructure ID; inferred from the hosted cluster by default")
	cmd.PersistentFlags().BoolVar(&opts.DestroyCloudResources, "destroy-cloud-resources", opts.DestroyCloudResources, "If true, cloud resources such as load balancers and persistent storage disks created by the cluster during its lifetime are removed")

	cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(aws.NewDestroyCommand(opts))
	cmd.AddCommand(none.NewDestroyCommand(opts))
	cmd.AddCommand(agent.NewDestroyCommand(opts))
	cmd.AddCommand(kubevirt.NewDestroyCommand(opts))
	cmd.AddCommand(azure.NewDestroyCommand(opts))
	cmd.AddCommand(powervs.NewDestroyCommand(opts))

	return cmd
}
