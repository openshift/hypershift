package drainer

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	kubeclient "k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	targetKubeClient, err := kubeclient.NewForConfig(opts.TargetConfig)
	if err != nil {
		return err
	}
	r := &Reconciler{
		client:                 opts.CPCluster.GetClient(),
		guestClusterClient:     opts.Manager.GetClient(),
		guestClusterKubeClient: targetKubeClient,
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}
	c, err := controller.New("drainer", opts.Manager, controller.Options{Reconciler: r, MaxConcurrentReconciles: 10})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[crclient.Object](opts.Manager.GetCache(), &corev1.Node{}, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch Nodes: %w", err)
	}

	return nil
}
