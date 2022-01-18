package aws

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSPlatformCreateOptions struct {
	InstanceProfile string
	SubnetID        string
	SecurityGroupID string
	InstanceType    string
	RootVolumeType  string
	RootVolumeIOPS  int64
	RootVolumeSize  int64
}

func NewCreateCommand(coreOpts core.CreateNodePoolOptions) *cobra.Command {
	platformOpts := AWSPlatformCreateOptions{
		InstanceType:   "m5.large",
		RootVolumeType: "gp3",
		RootVolumeSize: 120,
		RootVolumeIOPS: 0,
	}
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Creates basic functional NodePool resources for AWS platform",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&platformOpts.InstanceType, "instance-type", platformOpts.InstanceType, "The AWS instance type of the NodePool")
	cmd.Flags().StringVar(&platformOpts.SubnetID, "subnet-id", platformOpts.SubnetID, "The AWS subnet ID in which to create the NodePool")
	cmd.Flags().StringVar(&platformOpts.SecurityGroupID, "securitygroup-id", platformOpts.SecurityGroupID, "The AWS security group in which to create the NodePool")
	cmd.Flags().StringVar(&platformOpts.InstanceProfile, "instance-profile", platformOpts.InstanceProfile, "The AWS instance profile for the NodePool")
	cmd.Flags().StringVar(&platformOpts.RootVolumeType, "root-volume-type", platformOpts.RootVolumeType, "The type of the root volume (e.g. gp3, io2) for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeIOPS, "root-volume-iops", platformOpts.RootVolumeIOPS, "The iops of the root volume for machines in the NodePool")
	cmd.Flags().Int64Var(&platformOpts.RootVolumeSize, "root-volume-size", platformOpts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := coreOpts.CreateNodePool(ctx, platformOpts); err != nil {
			log.Error(err, "Failed to create nodepool")
			os.Exit(1)
		}
	}

	return cmd
}

func (o AWSPlatformCreateOptions) UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error {
	if len(o.InstanceProfile) == 0 {
		o.InstanceProfile = fmt.Sprintf("%s-worker", hcluster.Spec.InfraID)
	}
	if len(o.SubnetID) == 0 {
		if hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID != nil {
			o.SubnetID = *hcluster.Spec.Platform.AWS.CloudProviderConfig.Subnet.ID
		} else {
			return fmt.Errorf("subnet ID was not specified and cannot be determined from HostedCluster")
		}
	}
	if len(o.SecurityGroupID) == 0 {
		defaultNodePool := &hyperv1.NodePool{}
		if err := client.Get(ctx, types.NamespacedName{Namespace: hcluster.Namespace, Name: hcluster.Name}, defaultNodePool); err != nil {
			return fmt.Errorf("security group ID was not specified and cannot be determined from default nodepool: %v", err)
		}
		if defaultNodePool.Spec.Platform.AWS == nil || len(defaultNodePool.Spec.Platform.AWS.SecurityGroups) == 0 ||
			defaultNodePool.Spec.Platform.AWS.SecurityGroups[0].ID == nil {
			return fmt.Errorf("security group ID was not specified and cannot be determined from default nodepool")
		}
		o.SecurityGroupID = *defaultNodePool.Spec.Platform.AWS.SecurityGroups[0].ID
	}
	nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
		InstanceType:    o.InstanceType,
		InstanceProfile: o.InstanceProfile,
		Subnet: &hyperv1.AWSResourceReference{
			ID: &o.SubnetID,
		},
		SecurityGroups: []hyperv1.AWSResourceReference{
			{
				ID: &o.SecurityGroupID,
			},
		},
		RootVolume: &hyperv1.Volume{
			Type: o.RootVolumeType,
			Size: o.RootVolumeSize,
			IOPS: o.RootVolumeIOPS,
		},
	}
	return nil
}

func (o AWSPlatformCreateOptions) Type() hyperv1.PlatformType {
	return hyperv1.AWSPlatform
}
