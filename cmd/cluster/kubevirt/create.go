package kubevirt

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/infraid"
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
		ServicePublishingStrategy: IngressServicePublishingStrategy,
		APIServerAddress:          "",
		Memory:                    "4Gi",
		Cores:                     2,
		ContainerDiskImage:        "",
		RootVolumeSize:            16,
		InfraKubeConfigFile:       "",
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.APIServerAddress, "api-server-address", opts.KubevirtPlatform.APIServerAddress, "The API server address that should be used for components outside the control plane")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater than or equal to 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeStorageClass, "root-volume-storage-class", opts.KubevirtPlatform.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.RootVolumeSize, "root-volume-size", opts.KubevirtPlatform.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeAccessModes, "root-volume-access-modes", opts.KubevirtPlatform.RootVolumeAccessModes, "The access modes of the root volume to use for machines in the NodePool (comma-delimited list)")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskImage, "containerdisk", opts.KubevirtPlatform.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ServicePublishingStrategy, "service-publishing-strategy", opts.KubevirtPlatform.ServicePublishingStrategy, fmt.Sprintf("Define how to expose the cluster services. Supported options: %s (Use LoadBalancer and Route to expose services), %s (Select a random node to expose service access through)", IngressServicePublishingStrategy, NodePortServicePublishingStrategy))
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraKubeConfigFile, "infra-kubeconfig-file", opts.KubevirtPlatform.InfraKubeConfigFile, "Path to a kubeconfig file of an external infra cluster to be used to create the guest clusters nodes onto")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.InfraNamespace, "infra-namespace", opts.KubevirtPlatform.InfraNamespace, "The namespace in the external infra cluster that is used to host the KubeVirt virtual machines. The namespace must exist prior to creating the HostedCluster")

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
	return core.CreateCluster(ctx, opts, applyPlatformSpecificsValues)
}

func applyPlatformSpecificsValues(ctx context.Context, exampleOptions *apifixtures.ExampleOptions, opts *core.CreateOptions) (err error) {
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

	if opts.KubevirtPlatform.RootVolumeSize < 8 {
		return fmt.Errorf("the root volume size [%d] must be greater than or equal to 8", opts.KubevirtPlatform.RootVolumeSize)
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

	if opts.KubevirtPlatform.InfraNamespace == "" && opts.KubevirtPlatform.InfraKubeConfigFile != "" {
		return fmt.Errorf("external infra cluster kubeconfig was provided but an infra namespace is missing")
	}

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		ServicePublishingStrategy: opts.KubevirtPlatform.ServicePublishingStrategy,
		APIServerAddress:          opts.KubevirtPlatform.APIServerAddress,
		Memory:                    opts.KubevirtPlatform.Memory,
		Cores:                     opts.KubevirtPlatform.Cores,
		Image:                     opts.KubevirtPlatform.ContainerDiskImage,
		RootVolumeSize:            opts.KubevirtPlatform.RootVolumeSize,
		RootVolumeStorageClass:    opts.KubevirtPlatform.RootVolumeStorageClass,
		RootVolumeAccessModes:     opts.KubevirtPlatform.RootVolumeAccessModes,
		InfraKubeConfig:           infraKubeConfigContents,
		InfraNamespace:            opts.KubevirtPlatform.InfraNamespace,
	}

	if opts.BaseDomain != "" {
		exampleOptions.BaseDomain = opts.BaseDomain
	} else {
		exampleOptions.Kubevirt.BaseDomainPassthrough = true
	}

	return nil
}
