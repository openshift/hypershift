package usercabundle

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const ControllerName = "user-ca-bundle"

func Setup(_ context.Context, opts *operator.HostedClusterConfigOperatorConfig) error {
	r := &reconciler{
		client:                 opts.Manager.GetClient(),
		cpClient:               opts.CPCluster.GetClient(),
		CreateOrUpdateProvider: opts.TargetCreateOrUpdateProvider,
		hcpName:                opts.HCPName,
		hcpNamespace:           opts.Namespace,
	}

	c, err := controller.New(ControllerName, opts.Manager, controller.Options{Reconciler: r})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err = c.Watch(source.Kind(opts.CPCluster.GetCache(), &hyperv1.HostedControlPlane{},
		&handler.TypedEnqueueRequestForObject[*hyperv1.HostedControlPlane]{},
		additionalTrustBundleChangedPredicate(opts.Namespace, opts.HCPName))); err != nil {
		return fmt.Errorf("failed to watch HostedControlPlane: %w", err)
	}

	hcpRequest := []reconcile.Request{{NamespacedName: types.NamespacedName{
		Namespace: opts.Namespace,
		Name:      opts.HCPName,
	}}}
	userCAConfigMap := manifests.UserCABundle()
	userCAConfigMapPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == userCAConfigMap.Namespace && obj.GetName() == userCAConfigMap.Name
	})
	if err = c.Watch(source.Kind[client.Object](opts.Manager.GetCache(), &corev1.ConfigMap{},
		handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
			return hcpRequest
		}), userCAConfigMapPredicate)); err != nil {
		return fmt.Errorf("failed to watch guest user CA ConfigMap: %w", err)
	}

	cpUserCAConfigMap := cpomanifests.UserCAConfigMap(opts.Namespace)
	cpUserCAConfigMapPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == cpUserCAConfigMap.Namespace && obj.GetName() == cpUserCAConfigMap.Name
	})
	if err = c.Watch(source.Kind[client.Object](opts.CPCluster.GetCache(), &corev1.ConfigMap{},
		handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
			return hcpRequest
		}), cpUserCAConfigMapPredicate)); err != nil {
		return fmt.Errorf("failed to watch control plane user CA ConfigMap: %w", err)
	}

	return nil
}
