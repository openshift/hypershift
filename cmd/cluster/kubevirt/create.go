package kubevirt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/infraid"
	corev1 "k8s.io/api/core/v1"
)

const (
	NodePortServicePublishingStrategy = "NodePort"
	IngressServicePublishingStrategy  = "Ingress"
)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubevirt",
		Short:        "Creates basic functional HostedCluster resources for KubeVirt platform",
		SilenceUsage: true,
	}

	opts.KubevirtPlatform = core.KubevirtPlatformCreateOptions{
		ServicePublishingStrategy:  IngressServicePublishingStrategy,
		APIServerAddress:           "",
		Memory:                     "8Gi",
		Cores:                      2,
		ContainerDiskImage:         "",
		RootVolumeSize:             32,
		InfraKubeConfigFile:        "",
		CacheStrategyType:          "",
		NetworkInterfaceMultiQueue: "",
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.APIServerAddress, "api-server-address", opts.KubevirtPlatform.APIServerAddress, "The API server address that should be used for components outside the control plane")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater than or equal to 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeStorageClass, "root-volume-storage-class", opts.KubevirtPlatform.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.RootVolumeSize, "root-volume-size", opts.KubevirtPlatform.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeAccessModes, "root-volume-access-modes", opts.KubevirtPlatform.RootVolumeAccessModes, "The access modes of the root volume to use for machines in the NodePool (comma-delimited list)")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeVolumeMode, "root-volume-volume-mode", opts.KubevirtPlatform.RootVolumeVolumeMode, "The volume mode of the root volume to use for machines in the NodePool. Supported values are \"Block\", \"Filesystem\"")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskImage, "containerdisk", opts.KubevirtPlatform.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ServicePublishingStrategy, "service-publishing-strategy", opts.KubevirtPlatform.ServicePublishingStrategy, fmt.Sprintf("Define how to expose the cluster services. Supported options: %s (Use LoadBalancer and Route to expose services), %s (Select a random node to expose service access through)", IngressServicePublishingStrategy, NodePortServicePublishingStrategy))
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraKubeConfigFile, "infra-kubeconfig-file", opts.KubevirtPlatform.InfraKubeConfigFile, "Path to a kubeconfig file of an external infra cluster to be used to create the guest clusters nodes onto")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraNamespace, "infra-namespace", opts.KubevirtPlatform.InfraNamespace, "The namespace in the external infra cluster that is used to host the KubeVirt virtual machines. The namespace must exist prior to creating the HostedCluster")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.CacheStrategyType, "root-volume-cache-strategy", opts.KubevirtPlatform.CacheStrategyType, "Set the boot image caching strategy; Supported values:\n- \"None\": no caching (default).\n- \"PVC\": Cache into a PVC; only for QCOW image; ignored for container images")
	cmd.Flags().StringArrayVar(&opts.KubevirtPlatform.InfraStorageClassMappings, "infra-storage-class-mapping", opts.KubevirtPlatform.InfraStorageClassMappings, "KubeVirt CSI napping of an infra StorageClass to a guest cluster StorageCluster. Mapping is structured as <infra storage class>/<guest storage class>. Example, mapping the infra storage class ocs-storagecluster-ceph-rbd to a guest storage class called ceph-rdb. --infra-storage-class-mapping=ocs-storagecluster-ceph-rbd/ceph-rdb")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.NetworkInterfaceMultiQueue, "network-multiqueue", opts.KubevirtPlatform.NetworkInterfaceMultiQueue, `If "Enable", virtual network interfaces configured with a virtio bus will also enable the vhost multiqueue feature for network devices. supported values are "Enable" and "Disable"; default = "Disable"`)

	cmd.MarkPersistentFlagRequired("pull-secret")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			return err
		}
		return nil
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions) error {
	return core.CreateCluster(ctx, opts, ApplyPlatformSpecificsValues)
}

func ApplyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
	if opts.KubevirtPlatform.ServicePublishingStrategy != NodePortServicePublishingStrategy && opts.KubevirtPlatform.ServicePublishingStrategy != IngressServicePublishingStrategy {
		return fmt.Errorf("service publish strategy %s is not supported, supported options: %s, %s", opts.KubevirtPlatform.ServicePublishingStrategy, IngressServicePublishingStrategy, NodePortServicePublishingStrategy)
	}
	if opts.KubevirtPlatform.ServicePublishingStrategy != NodePortServicePublishingStrategy && opts.KubevirtPlatform.APIServerAddress != "" {
		return fmt.Errorf("external-api-server-address is supported only for NodePort service publishing strategy, service publishing strategy %s is used", opts.KubevirtPlatform.ServicePublishingStrategy)
	}
	if opts.KubevirtPlatform.APIServerAddress == "" && opts.KubevirtPlatform.ServicePublishingStrategy == NodePortServicePublishingStrategy && !opts.Render {
		if opts.KubevirtPlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx, opts.Log); err != nil {
			return err
		}
	}

	if opts.KubevirtPlatform.Cores < 1 {
		return errors.New("the number of cores inside the machine must be a value greater than or equal to 1")
	}

	if opts.KubevirtPlatform.RootVolumeSize < 16 {
		return fmt.Errorf("the root volume size [%d] must be greater than or equal to 16", opts.KubevirtPlatform.RootVolumeSize)
	}

	for _, mapping := range opts.KubevirtPlatform.InfraStorageClassMappings {
		split := strings.Split(mapping, "/")
		if len(split) != 2 {
			return fmt.Errorf("invalid infra storageclass mapping [%s]", mapping)
		}
	}

	infraID := opts.InfraID
	if len(infraID) == 0 {
		exampleOptions.InfraID = infraid.New(opts.Name)
	} else {
		exampleOptions.InfraID = infraID
	}

	var infraKubeConfigContents []byte
	infraKubeConfigFile := opts.KubevirtPlatform.InfraKubeConfigFile
	if len(infraKubeConfigFile) > 0 {
		infraKubeConfigContents, err = os.ReadFile(infraKubeConfigFile)
		if err != nil {
			return fmt.Errorf("failed to read external infra cluster kubeconfig file: %w", err)
		}
	} else {
		infraKubeConfigContents = nil
	}

	if opts.KubevirtPlatform.InfraKubeConfigFile == "" && opts.KubevirtPlatform.InfraNamespace != "" {
		return fmt.Errorf("external infra cluster namespace was provided but a kubeconfig is missing")
	}

	if opts.KubevirtPlatform.RootVolumeVolumeMode != "" &&
		opts.KubevirtPlatform.RootVolumeVolumeMode != string(corev1.PersistentVolumeBlock) &&
		opts.KubevirtPlatform.RootVolumeVolumeMode != string(corev1.PersistentVolumeFilesystem) {

		return fmt.Errorf(`unsupported value for the --root-volume-volume-mode parameter. May be only "Filesystem" or "Block"`)
	}

	if opts.KubevirtPlatform.InfraNamespace == "" && opts.KubevirtPlatform.InfraKubeConfigFile != "" {
		return fmt.Errorf("external infra cluster kubeconfig was provided but an infra namespace is missing")
	}

	if opts.KubevirtPlatform.CacheStrategyType != "" &&
		opts.KubevirtPlatform.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyNone) &&
		opts.KubevirtPlatform.CacheStrategyType != string(hyperv1.KubevirtCachingStrategyPVC) {
		return fmt.Errorf(`wrong value for the --root-volume-cache-strategy parameter. May be only "None" or "PVC"`)
	}

	var multiQueue *hyperv1.MultiQueueSetting
	switch opts.KubevirtPlatform.NetworkInterfaceMultiQueue {
	case "": // do nothing; value is nil
	case string(hyperv1.MultiQueueEnable), string(hyperv1.MultiQueueDisable):
		value := hyperv1.MultiQueueSetting(opts.KubevirtPlatform.NetworkInterfaceMultiQueue)
		multiQueue = &value
	default:
		return fmt.Errorf(`wrong value for the --network-multiqueue parameter. May be only "enable" or "disable"`)
	}

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		ServicePublishingStrategy:  opts.KubevirtPlatform.ServicePublishingStrategy,
		APIServerAddress:           opts.KubevirtPlatform.APIServerAddress,
		Memory:                     opts.KubevirtPlatform.Memory,
		Cores:                      opts.KubevirtPlatform.Cores,
		Image:                      opts.KubevirtPlatform.ContainerDiskImage,
		RootVolumeSize:             opts.KubevirtPlatform.RootVolumeSize,
		RootVolumeStorageClass:     opts.KubevirtPlatform.RootVolumeStorageClass,
		RootVolumeAccessModes:      opts.KubevirtPlatform.RootVolumeAccessModes,
		RootVolumeVolumeMode:       opts.KubevirtPlatform.RootVolumeVolumeMode,
		InfraKubeConfig:            infraKubeConfigContents,
		InfraNamespace:             opts.KubevirtPlatform.InfraNamespace,
		CacheStrategyType:          opts.KubevirtPlatform.CacheStrategyType,
		InfraStorageClassMappings:  opts.KubevirtPlatform.InfraStorageClassMappings,
		NetworkInterfaceMultiQueue: multiQueue,
	}

	if opts.BaseDomain != "" {
		exampleOptions.BaseDomain = opts.BaseDomain
	} else {
		exampleOptions.Kubevirt.BaseDomainPassthrough = true
	}

	return nil
}
