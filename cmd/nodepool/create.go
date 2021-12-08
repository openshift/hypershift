package nodepool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type CreateNodePoolOptions struct {
	Name            string
	Namespace       string
	ClusterName     string
	NodeCount       int32
	ReleaseImage    string
	InstanceType    string
	SubnetID        string
	SecurityGroupID string
	InstanceProfile string
	Render          bool
	RootVolumeType  string
	RootVolumeIOPS  int64
	RootVolumeSize  int64
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := CreateNodePoolOptions{
		Name:           "example",
		Namespace:      "clusters",
		ClusterName:    "example",
		NodeCount:      2,
		ReleaseImage:   "",
		InstanceType:   "m4.large",
		RootVolumeType: "gp2",
		RootVolumeSize: 120,
		RootVolumeIOPS: 0,
	}

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool")
	cmd.Flags().Int32Var(&opts.NodeCount, "node-count", opts.NodeCount, "The number of nodes to create in the NodePool")
	cmd.Flags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The release image for nodes. If empty, defaults to the same release image as the HostedCluster.")
	cmd.Flags().StringVar(&opts.InstanceType, "instance-type", opts.InstanceType, "The AWS instance type of the NodePool")
	cmd.Flags().StringVar(&opts.SubnetID, "subnet-id", opts.SubnetID, "The AWS subnet ID in which to create the NodePool")
	cmd.Flags().StringVar(&opts.SecurityGroupID, "securitygroup-id", opts.SecurityGroupID, "The AWS security group in which to create the NodePool")
	cmd.Flags().StringVar(&opts.InstanceProfile, "instance-profile", opts.InstanceProfile, "The AWS instance profile for the NodePool")

	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")

	cmd.Flags().StringVar(&opts.RootVolumeType, "root-volume-type", opts.RootVolumeType, "The type of the root volume (e.g. gp2, io1) for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.RootVolumeIOPS, "root-volume-iops", opts.RootVolumeIOPS, "The iops of the root volume when specifying type:io1 for machines in the NodePool")
	cmd.Flags().Int64Var(&opts.RootVolumeSize, "root-volume-size", opts.RootVolumeSize, "The size of the root volume (min: 8) for machines in the NodePool")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()
		return opts.Run(ctx)
	}

	return cmd
}

func (o *CreateNodePoolOptions) Run(ctx context.Context) error {
	client := util.GetClientOrDie()

	hcluster := &hyperv1.HostedCluster{}
	err := client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.ClusterName}, hcluster)
	if err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.Namespace, o.Name, err)
	}

	nodePool := &hyperv1.NodePool{}
	err = client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name}, nodePool)
	if err == nil && !o.Render {
		return fmt.Errorf("NodePool %s/%s already exists", o.Namespace, o.Name)
	}

	var releaseImage string
	if len(o.ReleaseImage) > 0 {
		releaseImage = o.ReleaseImage
	} else {
		releaseImage = hcluster.Spec.Release.Image
	}

	nodePool = &hyperv1.NodePool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodePool",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      o.Name,
		},
		Spec: hyperv1.NodePoolSpec{
			Management: hyperv1.NodePoolManagement{
				UpgradeType: hyperv1.UpgradeTypeReplace,
			},
			ClusterName: o.ClusterName,
			NodeCount:   &o.NodeCount,
			Release: hyperv1.Release{
				Image: releaseImage,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hcluster.Spec.Platform.Type,
			},
		},
	}

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
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
	}

	if o.Render {
		err := hyperapi.YamlSerializer.Encode(nodePool, os.Stdout)
		if err != nil {
			panic(err)
		}
		return nil
	}

	var nodePoolBytes bytes.Buffer
	err = hyperapi.YamlSerializer.Encode(nodePool, &nodePoolBytes)
	if err != nil {
		return err
	}

	if o.Render {
		_, err = nodePoolBytes.WriteTo(os.Stdout)
		return err
	}

	err = client.Create(ctx, nodePool)
	if err != nil {
		return err
	}

	fmt.Printf("NodePool %s created\n", o.Name)
	return nil
}
