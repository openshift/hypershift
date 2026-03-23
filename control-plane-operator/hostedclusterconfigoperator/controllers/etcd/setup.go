package etcd

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
)

const (
	ControllerName = "etcd-udn-endpointslices"
)

func Setup(ctx context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	// Primary UDN is currently only supported/used with the KubeVirt platform.
	if opts.PlatformType != hyperv1.KubevirtPlatform {
		return nil
	}

	r := &reconciler{
		client:                 opts.CPCluster.GetClient(),
		hcpKey:                 types.NamespacedName{Namespace: opts.Namespace, Name: opts.HCPName},
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
	}

	enqueueHCP := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: r.hcpKey}}
	})

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.Pod{}, enqueueHCP, isEtcdPodInNamespace(opts.Namespace))); err != nil {
		return fmt.Errorf("failed to watch etcd Pods: %w", err)
	}
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.Service{}, enqueueHCP, isEtcdServiceInNamespace(opts.Namespace))); err != nil {
		return fmt.Errorf("failed to watch etcd Services: %w", err)
	}
	if err := c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &discoveryv1.EndpointSlice{}, enqueueHCP, isEtcdEndpointSliceInNamespace(opts.Namespace))); err != nil {
		return fmt.Errorf("failed to watch etcd EndpointSlices: %w", err)
	}

	logger := ctrl.LoggerFrom(ctx)
	logger.Info("Setup", "controller", ControllerName)
	return nil
}
