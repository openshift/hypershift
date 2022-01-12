package core

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
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
	AutoRepair   bool
}

type PlatformOptions interface {
	// UpdateNodePool is used to update the platform specific values in the NodePool
	UpdateNodePool(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, client crclient.Client) error
	// Type returns the platform type
	Type() hyperv1.PlatformType
	// Validate checks if the platform options configured as expected, otherwise return error
	Validate() error
}

func NewCreateNodePoolOptions(cmd *cobra.Command, defaultNodeCount int32) *CreateNodePoolOptions {
	opts := &CreateNodePoolOptions{
		NodeCount: defaultNodeCount,
	}

	// All the flags added here would be included in both NodePool and Cluster create commands
	// In order to include flag only in NodePool create command, add the flag in `cmd/nodepool/create.go`
	cmd.PersistentFlags().Int32Var(&opts.NodeCount, "node-pool-replicas", opts.NodeCount, "The number of nodes to create in the NodePool")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine autorepair with machine health checks")

	return opts
}

func (o *CreateNodePoolOptions) CreateExecFunc(platformOpts PlatformOptions) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := o.createNodePool(cmd.Context(), platformOpts); err != nil {
			log.Error(err, "Failed to create nodepool")
			return err
		}
		return nil
	}
}

func (o *CreateNodePoolOptions) createNodePool(ctx context.Context, platformOpts PlatformOptions) error {
	if o.NodeCount < 0 {
		return fmt.Errorf("node-pool-replicas must not be smaller than 0 (node-pool-replicas=%d)", o.NodeCount)
	}
	if err := platformOpts.Validate(); err != nil {
		return err
	}

	client := util.GetClientOrDie()

	hcluster := &hyperv1.HostedCluster{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.ClusterName}, hcluster); err != nil {
		return fmt.Errorf("failed to get HostedCluster %s/%s: %w", o.Namespace, o.ClusterName, err)
	}

	nodePool, err := o.GenerateNodePoolObject(ctx, platformOpts, hcluster, client)
	if err != nil {
		return err
	}

	return util.ApplyObjects(ctx, []crclient.Object{nodePool}, o.Render, hcluster.Spec.InfraID)
}

func (o *CreateNodePoolOptions) GenerateNodePoolObject(ctx context.Context, platformOpts PlatformOptions, hcluster *hyperv1.HostedCluster, client crclient.Client) (*hyperv1.NodePool, error) {
	if o.NodeCount < 0 {
		return nil, nil
	}
	if platformOpts.Type() != hcluster.Spec.Platform.Type {
		return nil, fmt.Errorf("NodePool platform type %s must be HostedCluster type %s", platformOpts.Type(), hcluster.Spec.Platform.Type)
	}

	nodePoolName := o.Name
	if nodePoolName == "" {
		nodePoolName = hcluster.Name
	}
	nodePoolNamespace := hcluster.Namespace

	nodePool := &hyperv1.NodePool{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: nodePoolNamespace, Name: nodePoolName}, nodePool); err == nil && !o.Render {
		return nil, fmt.Errorf("NodePool %s/%s already exists", nodePoolNamespace, nodePoolName)
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
			Namespace: nodePoolNamespace,
			Name:      nodePoolName,
		},
		Spec: hyperv1.NodePoolSpec{
			Management: hyperv1.NodePoolManagement{
				AutoRepair:  o.AutoRepair,
				UpgradeType: hyperv1.UpgradeTypeReplace,
			},
			ClusterName: hcluster.Name,
			NodeCount:   &o.NodeCount,
			Release: hyperv1.Release{
				Image: releaseImage,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: platformOpts.Type(),
			},
		},
	}

	if err := platformOpts.UpdateNodePool(ctx, nodePool, hcluster, client); err != nil {
		return nil, err
	}

	return nodePool, nil
}
