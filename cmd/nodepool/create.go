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
	Name        string
	Namespace   string
	ClusterName string
	NodeCount   int32
	Render      bool
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := CreateNodePoolOptions{
		Name:        "example",
		Namespace:   "clusters",
		ClusterName: "example",
		NodeCount:   2,
	}

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool")
	cmd.Flags().Int32Var(&opts.NodeCount, "node-count", opts.NodeCount, "The number of nodes to create in the NodePool")
	cmd.Flags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join")

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
			IgnitionService: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
			ClusterName: o.ClusterName,
			NodeCount:   &o.NodeCount,
			Platform: hyperv1.NodePoolPlatform{
				Type: hcluster.Spec.Platform.Type,
			},
		},
	}

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		nodePool.Spec.Platform.AWS = hcluster.Spec.Platform.AWS.NodePoolDefaults
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
