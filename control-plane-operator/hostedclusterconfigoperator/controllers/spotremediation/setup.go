package spotremediation

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "spot-remediation"

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	if opts.PlatformType != hyperv1.AWSPlatform {
		return nil
	}

	r := &reconciler{
		client:             opts.CPCluster.GetClient(),
		guestClusterClient: opts.Manager.GetClient(),
	}
	c, err := controller.New(ControllerName, opts.Manager, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 1,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &corev1.Node{}, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch Nodes: %w", err)
	}

	return nil
}
