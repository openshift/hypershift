package core

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type CreateNodePoolOptions struct {
	Name         string
	Namespace    string
	ClusterName  string
	NodeCount    int32
	ReleaseImage string
	Render       bool
}

type PlatformOptions interface {
	// UpdateNodePool is used to update the platform specific values in the NodePool
	UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error
	// Type returns the platform type
	Type() hyperv1.PlatformType
}

func (o *CreateNodePoolOptions) CreateRunFunc(platformOpts PlatformOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := o.CreateNodePool(cmd.Context(), platformOpts); err != nil {
			log.Log.Error(err, "Failed to create nodepool")
			return err
		}
		return nil
	}
}

func (o *CreateNodePoolOptions) CreateNodePool(ctx context.Context, platformOpts PlatformOptions) error {
	client, err := util.GetClient()
	if err != nil {
		return err
	}

	hcluster := &hyperv1.HostedCluster{}
	err = client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.ClusterName}, hcluster)
	if err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.Namespace, o.ClusterName, err)
	}

	if platformOpts.Type() != hcluster.Spec.Platform.Type {
		return fmt.Errorf("NodePool platform type %s must be HostedCluster type %s", platformOpts.Type(), hcluster.Spec.Platform.Type)
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
			Replicas:    &o.NodeCount,
			Release: hyperv1.Release{
				Image: releaseImage,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hcluster.Spec.Platform.Type,
			},
		},
	}

	if err := platformOpts.UpdateNodePool(ctx, nodePool, hcluster, client); err != nil {
		return err
	}

	if o.Render {
		err := hyperapi.YamlSerializer.Encode(nodePool, os.Stdout)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "NodePool %s was rendered to yaml output file\n", o.Name)
		return nil
	}

	err = client.Create(ctx, nodePool)
	if err != nil {
		return err
	}

	fmt.Printf("NodePool %s created\n", o.Name)
	return nil
}
