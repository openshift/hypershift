package karpenter

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	appsv1 "k8s.io/api/apps/v1"
	certv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &Reconciler{
		Client:                 opts.CPCluster.GetClient(),
		GuestClusterClient:     opts.Manager.GetClient(),
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
		HCPNamespace:           opts.Namespace,
	}
	c, err := controller.New("featureGate-Karpenter", opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &corev1.Node{}, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch Nodes: %w", err)
	}
	if err := c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &certv1.CertificateSigningRequest{}, &handler.EnqueueRequestForObject{})); err != nil {
		return fmt.Errorf("failed to watch CertificateSigningRequests: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, c client.Object) []reconcile.Request {
			if c.GetNamespace() != r.HCPNamespace || c.GetName() != "karpenter" {
				return nil
			}
			return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(c)}}
		}))); err != nil {
		return fmt.Errorf("failed to watch Deployment: %w", err)
	}

	// Other feature gated controllers can be added here.
	return nil
}
