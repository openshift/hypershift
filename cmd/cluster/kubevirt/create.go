package kubevirt

import (
	"context"
	"errors"
	"fmt"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/spf13/cobra"

	apifixtures "github.com/openshift/hypershift/api/fixtures"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
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
		Memory:                    "4Gi",
		Cores:                     2,
		ContainerDiskImage:        "",
	}

	cmd.Flags().StringVar(&opts.KubevirtPlatform.Memory, "memory", opts.KubevirtPlatform.Memory, "The amount of memory which is visible inside the Guest OS (type BinarySI, e.g. 5Gi, 100Mi)")
	cmd.Flags().Uint32Var(&opts.KubevirtPlatform.Cores, "cores", opts.KubevirtPlatform.Cores, "The number of cores inside the vmi, Must be a value greater or equal 1")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.RootVolumeStorageClass, "root-volume-storage-class", opts.KubevirtPlatform.RootVolumeStorageClass, "The storage class to use for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.KubevirtPlatform.RootVolumeSize, "root-volume-size", opts.KubevirtPlatform.RootVolumeSize, "The size of the root volume for machines in the NodePool in Gi")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskImage, "containerdisk", opts.KubevirtPlatform.ContainerDiskImage, "A reference to docker image with the embedded disk to be used to create the machines")
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ServicePublishingStrategy, "service-publishing-strategy", opts.KubevirtPlatform.ServicePublishingStrategy, fmt.Sprintf("Define how to expose the cluster services. Supported options: %s (Use LoadBalancer and Route to expose services), %s (Select a random node to expose service access through)", IngressServicePublishingStrategy, NodePortServicePublishingStrategy))
	cmd.Flags().StringVar(&opts.KubevirtPlatform.ContainerDiskRepo, "containerdisk-repository", opts.KubevirtPlatform.ContainerDiskRepo, "A reference to docker image registry with the embedded disk to be used to create the machines, the tag will be auto-discovered")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}

		if err := CreateCluster(ctx, opts); err != nil {
			log.Log.Error(err, "Failed to create cluster")
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
		if opts.KubevirtPlatform.APIServerAddress, err = core.GetAPIServerAddressByNode(ctx); err != nil {
			return err
		}
	}

	if opts.NodePoolReplicas > -1 {
		// TODO (nargaman): replace with official container image, after RFE-2501 is completed
		// As long as there is no official container image
		// The image must be provided by user
		// Otherwise it must fail
		if (opts.KubevirtPlatform.ContainerDiskImage != "" && opts.KubevirtPlatform.ContainerDiskRepo != "") && (opts.KubevirtPlatform.RootVolumeSize > 0 || opts.KubevirtPlatform.RootVolumeStorageClass != "") {
			return errors.New("container disk options (\"--containerdisk*\" flag) can not be used together with customized root volume options (\"--root-volume-*\" flags)")
		}
		if opts.KubevirtPlatform.ContainerDiskRepo != "" {
			opts.KubevirtPlatform.ContainerDiskImage = fmt.Sprintf("%v:%v", opts.KubevirtPlatform.ContainerDiskRepo, util.RHCOSOpenStackChecksumParameter)
		}
	}

	if opts.KubevirtPlatform.Cores < 1 {
		return errors.New("the number of cores inside the machine must be a value greater or equal 1")
	}

	infraID := opts.InfraID
	exampleOptions.InfraID = infraID

	if opts.BaseDomain != "" {
		exampleOptions.BaseDomain = opts.BaseDomain
	} else {
		exampleOptions.BaseDomain = "example.com"
	}

	exampleOptions.Kubevirt = &apifixtures.ExampleKubevirtOptions{
		ServicePublishingStrategy: opts.KubevirtPlatform.ServicePublishingStrategy,
		APIServerAddress:          opts.KubevirtPlatform.APIServerAddress,
		Memory:                    opts.KubevirtPlatform.Memory,
		Cores:                     opts.KubevirtPlatform.Cores,
		Image:                     opts.KubevirtPlatform.ContainerDiskImage,
	}
	return nil
}
