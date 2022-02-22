package hostedapicache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// HostedAPICacheReconciler is used to drive a hostedAPICache to keep the cache
// in sync with a hosted apiserver based on a kubeconfig published for the cluster.
//
// The reconciler will watch for changes to the given kubeconfig secret and make
// the right calls to keep the hostedAPICache updated. Other controllers can use
// the GetCache function to get the actively managed HostedAPICache for accessing
// the hosted apiserver.
type HostedAPICacheReconciler struct {
	client.Client

	hostedAPICache *hostedAPICache
	scope          manifests.KubeconfigScope
}

// RegisterHostedAPICacheReconciler builds a new HostedAPICacheReconciler for a
// cluster kubeconfig with the given scope.
func RegisterHostedAPICacheReconciler(mgr ctrl.Manager, cacheCtx context.Context, cacheLog logr.Logger, scope manifests.KubeconfigScope) (*HostedAPICacheReconciler, error) {
	r := &HostedAPICacheReconciler{
		Client:         mgr.GetClient(),
		hostedAPICache: newHostedAPICache(cacheCtx, cacheLog, mgr.GetScheme()),
		scope:          scope,
	}

	labelSelectorPredicate, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchLabels: map[string]string{manifests.KubeconfigScopeLabel: string(scope)},
	})
	if err != nil {
		return nil, err
	}
	_, err = ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}, builder.WithPredicates(labelSelectorPredicate)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 10*time.Second),
		}).
		Build(r)
	if err != nil {
		return nil, fmt.Errorf("failed setting up with a controller manager %w", err)
	}

	return r, nil
}

// GetCache provides a functional HostedAPICache which will stay in sync with
// the underlying kubeconfig secret used to make the cache.
func (r *HostedAPICacheReconciler) GetCache() HostedAPICache {
	return r.hostedAPICache
}

func (r *HostedAPICacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Determine which secret to reconcile based on the configured scope
	var secret *corev1.Secret
	switch r.scope {
	case manifests.KubeconfigScopeExternal:
		secret = manifests.KASExternalKubeconfigSecret(req.Namespace, nil)
	case manifests.KubeconfigScopeLocal:
		secret = manifests.KASServiceKubeconfigSecret(req.Namespace)
	default:
		return ctrl.Result{}, fmt.Errorf("invalid kubeconfig scope: %s", r.scope)
	}
	// If the secret doesn't exist, treat it as a delete and clear the cache
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("destroying hosted API cache because kubeconfig secret is missing")
			r.hostedAPICache.destroy()
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get secret: %v", err)
	}
	// If the secret is marked for deletion, go ahead and clear the cache
	if !secret.DeletionTimestamp.IsZero() {
		log.Info("destroying hosted API cache because kubeconfig secret is marked for deletion")
		r.hostedAPICache.destroy()
		return ctrl.Result{}, nil
	}

	// At this point, treat the secret as live and update the cache
	kubeConfig, hasKubeConfig := secret.Data[kas.KubeconfigKey]
	if !hasKubeConfig {
		return ctrl.Result{}, fmt.Errorf("kubeconfig secret is missing %q key", kas.KubeconfigKey)
	}

	log.Info("updating hosted API cache")
	cacheUpdateCtx, cancelCacheUpdate := context.WithTimeout(ctx, 5*time.Second)
	defer cancelCacheUpdate()
	err := r.hostedAPICache.update(cacheUpdateCtx, secret, kubeConfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update api cache: %w", err)
	}

	return ctrl.Result{}, nil
}
