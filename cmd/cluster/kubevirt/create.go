package kubevirt

import (
	"context"
	"fmt"
	"os"
	"strings"

	kubevirtnodepool "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	corev1 "k8s.io/api/core/v1"
)

func DefaultOptions() *CreateOptions {
	return &CreateOptions{
		ServicePublishingStrategy: IngressServicePublishingStrategy,
		NodePoolOpts:              kubevirtnodepool.DefaultOptions(),
	}
}

func BindOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)
	kubevirtnodepool.BindOptions(opts.NodePoolOpts, flags)
}

func bindCoreOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.InfraKubeConfigFile, "infra-kubeconfig-file", opts.InfraKubeConfigFile, "Path to a kubeconfig file of an external infra cluster to be used to create the guest clusters nodes onto")
	flags.StringVar(&opts.InfraNamespace, "infra-namespace", opts.InfraNamespace, "The namespace in the external infra cluster that is used to host the KubeVirt virtual machines. The namespace must exist prior to creating the HostedCluster")
	flags.StringArrayVar(&opts.InfraStorageClassMappings, "infra-storage-class-mapping", opts.InfraStorageClassMappings, "KubeVirt CSI mapping of an infra StorageClass to a guest cluster StorageCluster. Mapping is structured as <infra storage class>/<guest storage class>. Example, mapping the infra storage class ocs-storagecluster-ceph-rbd to a guest storage class called ceph-rdb. --infra-storage-class-mapping=ocs-storagecluster-ceph-rbd/ceph-rdb. Group storage classes and volumesnapshot classes by adding ,group=<group name>")
}

func BindDeveloperOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	bindCoreOptions(opts, flags)

	flags.StringVar(&opts.APIServerAddress, "api-server-address", opts.APIServerAddress, "The API server address that should be used for components outside the control plane")
	flags.StringVar(&opts.ServicePublishingStrategy, "service-publishing-strategy", opts.ServicePublishingStrategy, fmt.Sprintf("Define how to expose the cluster services. Supported options: %s (Use LoadBalancer and Route to expose services), %s (Select a random node to expose service access through)", IngressServicePublishingStrategy, NodePortServicePublishingStrategy))
	flags.StringArrayVar(&opts.InfraVolumeSnapshotClassMappings, "infra-volumesnapshot-class-mapping", opts.InfraVolumeSnapshotClassMappings, "KubeVirt CSI mapping of an infra VolumeSnapshotClass to a guest cluster VolumeSnapshotCluster. Mapping is structured as <infra volume snapshot class>/<guest volume snapshot class>. Example, mapping the infra volume snapshot class ocs-storagecluster-rbd-snap to a guest volume snapshot class called rdb-snap. --infra-volumesnapshot-class-mapping=ocs-storagecluster-rbd-snap/rdb-snap. Group storage classes and volumesnapshot classes by adding ,group=<group name>")

	kubevirtnodepool.BindDeveloperOptions(opts.NodePoolOpts, flags)
}

type CreateOptions struct {
	ServicePublishingStrategy        string
	APIServerAddress                 string
	InfraKubeConfigFile              string
	InfraNamespace                   string
	InfraStorageClassMappings        []string
	InfraVolumeSnapshotClassMappings []string

	NodePoolOpts *kubevirtnodepool.KubevirtPlatformCreateOptions

	// after complete:
	CompletedNodePoolOpts *kubevirtnodepool.KubevirtPlatformCompletedOptions

	externalDNSDomain string

	name, namespace                                 string
	baseDomainPassthrough                           bool
	allowUnsupportedKubeVirtRHCOSVariantsAnnotation string
}

func (o *CreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) error {
	if o.ServicePublishingStrategy != NodePortServicePublishingStrategy && o.ServicePublishingStrategy != IngressServicePublishingStrategy {
		return fmt.Errorf("service publish strategy %s is not supported, supported options: %s, %s", o.ServicePublishingStrategy, IngressServicePublishingStrategy, NodePortServicePublishingStrategy)
	}
	if o.ServicePublishingStrategy != NodePortServicePublishingStrategy && o.APIServerAddress != "" {
		return fmt.Errorf("external-api-server-address is supported only for NodePort service publishing strategy, service publishing strategy %s is used", o.ServicePublishingStrategy)
	}
	if o.APIServerAddress == "" && o.ServicePublishingStrategy == NodePortServicePublishingStrategy && (!opts.Render || opts.RenderInto != "") {
		var err error
		if o.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log); err != nil {
			return err
		}
	}

	for _, mapping := range o.InfraStorageClassMappings {
		split := strings.Split(mapping, "/")
		if len(split) != 2 {
			return fmt.Errorf("invalid infra storageclass mapping [%s]", mapping)
		}
	}

	for _, mapping := range o.InfraVolumeSnapshotClassMappings {
		split := strings.Split(mapping, "/")
		if len(split) != 2 {
			return fmt.Errorf("invalid infra volume snapshot class mapping [%s]", mapping)
		}
	}

	if o.InfraKubeConfigFile == "" && o.InfraNamespace != "" {
		return fmt.Errorf("external infra cluster namespace was provided but a kubeconfig is missing")
	}

	if o.InfraNamespace == "" && o.InfraKubeConfigFile != "" {
		return fmt.Errorf("external infra cluster kubeconfig was provided but an infra namespace is missing")
	}

	return o.NodePoolOpts.Validate()
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	o.externalDNSDomain = opts.ExternalDNSDomain
	o.baseDomainPassthrough = opts.BaseDomain == ""
	o.name, o.namespace = opts.Name, opts.Namespace

	completed, err := o.NodePoolOpts.Complete()
	o.CompletedNodePoolOpts = completed
	return err
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	// TODO: this is a very weird way to pass input in, would be best to have it explicit
	if val, exists := cluster.Annotations[hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation]; exists {
		o.allowUnsupportedKubeVirtRHCOSVariantsAnnotation = val
	}

	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type:     hyperv1.KubevirtPlatform,
		Kubevirt: &hyperv1.KubevirtPlatformSpec{},
	}

	if len(o.InfraKubeConfigFile) > 0 {
		cluster.Spec.Platform.Kubevirt.Credentials = &hyperv1.KubevirtPlatformCredentials{
			InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{
				Name: infraKubeconfigSecret(cluster.Namespace, cluster.Name).Name,
				Key:  "kubeconfig",
			},
		}
	}

	switch o.ServicePublishingStrategy {
	case "NodePort":
		cluster.Spec.Services = core.GetServicePublishingStrategyMappingByAPIServerAddress(o.APIServerAddress, cluster.Spec.Networking.NetworkType)
	case "Ingress":
		cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, o.externalDNSDomain != "")
	default:
		panic(fmt.Sprintf("service publishing type %s is not supported", o.ServicePublishingStrategy))
	}

	if o.InfraNamespace != "" {
		cluster.Spec.Platform.Kubevirt.Credentials.InfraNamespace = o.InfraNamespace
	}

	if o.baseDomainPassthrough {
		cluster.Spec.Platform.Kubevirt.BaseDomainPassthrough = &o.baseDomainPassthrough
	}

	if len(o.InfraStorageClassMappings) > 0 {
		cluster.Spec.Platform.Kubevirt.StorageDriver = &hyperv1.KubevirtStorageDriverSpec{
			Type:   hyperv1.ManualKubevirtStorageDriverConfigType,
			Manual: &hyperv1.KubevirtManualStorageDriverConfig{},
		}

		for _, mapping := range o.InfraStorageClassMappings {
			split := strings.Split(mapping, "/")
			if len(split) != 2 {
				// This is sanity checked by the hypershift cli as well, so this error should
				// not be encountered here. This check is left here as a safety measure.
				panic(fmt.Sprintf("invalid KubeVirt infra storage class mapping [%s]", mapping))
			}
			guestName, groupName := parseTenantClassString(split[1])
			newMap := hyperv1.KubevirtStorageClassMapping{
				InfraStorageClassName: split[0],
				GuestStorageClassName: guestName,
				Group:                 groupName,
			}
			cluster.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping =
				append(cluster.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping, newMap)
		}
	}
	if len(o.InfraVolumeSnapshotClassMappings) > 0 {
		if cluster.Spec.Platform.Kubevirt.StorageDriver == nil {
			cluster.Spec.Platform.Kubevirt.StorageDriver = &hyperv1.KubevirtStorageDriverSpec{
				Type:   hyperv1.ManualKubevirtStorageDriverConfigType,
				Manual: &hyperv1.KubevirtManualStorageDriverConfig{},
			}
		}
		for _, mapping := range o.InfraVolumeSnapshotClassMappings {
			split := strings.Split(mapping, "/")
			if len(split) != 2 {
				// This is sanity checked by the hypershift cli as well, so this error should
				// not be encountered here. This check is left here as a safety measure.
				panic(fmt.Sprintf("invalid KubeVirt infra volume snapshot class mapping [%s]", mapping))
			}
			guestName, groupName := parseTenantClassString(split[1])
			newMap := hyperv1.KubevirtVolumeSnapshotClassMapping{
				InfraVolumeSnapshotClassName: split[0],
				GuestVolumeSnapshotClassName: guestName,
				Group:                        groupName,
			}
			cluster.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping =
				append(cluster.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping, newMap)
		}
	}
	return nil
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.KubevirtPlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}
	nodePool.Spec.Platform.Kubevirt = o.CompletedNodePoolOpts.NodePoolPlatform()
	if o.allowUnsupportedKubeVirtRHCOSVariantsAnnotation != "" {
		if nodePool.Annotations == nil {
			nodePool.Annotations = map[string]string{}
		}
		nodePool.Annotations[hyperv1.AllowUnsupportedKubeVirtRHCOSVariantsAnnotation] = o.allowUnsupportedKubeVirtRHCOSVariantsAnnotation
	}
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	if len(o.InfraKubeConfigFile) > 0 {
		infraKubeConfigContents, err := os.ReadFile(o.InfraKubeConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read external infra cluster kubeconfig file: %w", err)
		}
		infraKubeConfigSecret := infraKubeconfigSecret(o.namespace, o.name)
		infraKubeConfigSecret.Data = map[string][]byte{
			"kubeconfig": infraKubeConfigContents,
		}
		return []client.Object{infraKubeConfigSecret}, nil
	}

	return nil, nil
}

func infraKubeconfigSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-infra-credentials",
			Namespace: namespace,
			Labels:    map[string]string{util.DeleteWithClusterLabelName: "true"},
		},
		Type: corev1.SecretTypeOpaque,
	}
}

var _ core.Platform = (*CreateOptions)(nil)

type NetworkOpts struct {
	Name string `param:"name"`
}

const (
	NodePortServicePublishingStrategy = "NodePort"
	IngressServicePublishingStrategy  = "Ingress"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources on KubeVirt platform",
		SilenceUsage: true,
	}

	kubevirtOpts := DefaultOptions()
	BindDeveloperOptions(kubevirtOpts, cmd.Flags())
	cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts, kubevirtOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, kubevirtOpts *CreateOptions) error {
	return core.CreateCluster(ctx, opts, kubevirtOpts)
}

func parseTenantClassString(optionString string) (string, string) {
	guestName := optionString
	optionsSplit := strings.Split(optionString, ",")
	if len(optionsSplit) > 1 {
		guestName = optionsSplit[0]
		for i := 1; i < len(optionsSplit); i++ {
			optionSplit := strings.Split(optionsSplit[i], "=")
			if len(optionSplit) != 2 {
				panic(fmt.Sprintf("invalid KubeVirt infra storage class mapping option [%s]", optionsSplit[i]))
			}
			if isValidGroupOption(optionSplit[0]) {
				return guestName, optionSplit[1]
			}
		}
	}
	return guestName, ""
}

func isValidGroupOption(input string) bool {
	return strings.TrimSpace(strings.ToLower(input)) == "group"
}
