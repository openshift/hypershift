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

	corev1 "k8s.io/api/core/v1"
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
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := CreateNodePoolOptions{
		Name:         "example",
		Namespace:    "clusters",
		ClusterName:  "example",
		NodeCount:    2,
		ReleaseImage: "",
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
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.Namespace,
			Name:      o.Name,
		},
		Spec: hyperv1.NodePoolSpec{
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
