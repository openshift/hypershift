package usercabundle

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type reconciler struct {
	client   client.Client
	cpClient client.Reader
	upsert.CreateOrUpdateProvider
	hcpName      string
	hcpNamespace string
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	if req.Namespace != r.hcpNamespace || req.Name != r.hcpName {
		return ctrl.Result{}, nil
	}

	hcp := &hyperv1.HostedControlPlane{}
	if err := r.cpClient.Get(ctx, req.NamespacedName, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get hosted control plane %s: %w", req.NamespacedName, err)
	}

	if err := r.reconcileUserCertCABundle(ctx, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile user cert CA bundle: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *reconciler) reconcileUserCertCABundle(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	log := ctrl.LoggerFrom(ctx)
	userCAConfigMap := manifests.UserCABundle()

	if hcp.Spec.AdditionalTrustBundle != nil {
		cpUserCAConfigMap := cpomanifests.UserCAConfigMap(hcp.Namespace)
		if err := r.cpClient.Get(ctx, client.ObjectKeyFromObject(cpUserCAConfigMap), cpUserCAConfigMap); err != nil {
			return fmt.Errorf("cannot get AdditionalTrustBundle ConfigMap: %w", err)
		}
		if _, err := r.CreateOrUpdate(ctx, r.client, userCAConfigMap, func() error {
			userCAConfigMap.Data = cpUserCAConfigMap.Data
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile the %s ConfigMap: %w", client.ObjectKeyFromObject(userCAConfigMap), err)
		}
	} else {
		// If the HostedControlPlane has no additional trust bundle, delete the user-ca-bundle ConfigMap if it exists.
		if deleted, err := k8sutil.DeleteIfNeeded(ctx, r.client, userCAConfigMap); err != nil {
			return fmt.Errorf("failed to delete unused user-ca-bundle ConfigMap: %w", err)
		} else if deleted {
			log.Info("deleted unused user-ca-bundle ConfigMap", "name", userCAConfigMap.Name, "namespace", userCAConfigMap.Namespace)
		}
	}
	return nil
}

func additionalTrustBundleChangedPredicate(namespace, name string) predicate.TypedFuncs[*hyperv1.HostedControlPlane] {
	isTarget := func(hcp *hyperv1.HostedControlPlane) bool {
		return hcp.Namespace == namespace && hcp.Name == name
	}
	return predicate.TypedFuncs[*hyperv1.HostedControlPlane]{
		CreateFunc: func(e event.TypedCreateEvent[*hyperv1.HostedControlPlane]) bool {
			return isTarget(e.Object)
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*hyperv1.HostedControlPlane]) bool {
			return isTarget(e.Object)
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*hyperv1.HostedControlPlane]) bool {
			return isTarget(e.ObjectNew) && !equality.Semantic.DeepEqual(
				e.ObjectOld.Spec.AdditionalTrustBundle,
				e.ObjectNew.Spec.AdditionalTrustBundle,
			)
		},
		GenericFunc: func(e event.TypedGenericEvent[*hyperv1.HostedControlPlane]) bool {
			return isTarget(e.Object)
		},
	}
}
