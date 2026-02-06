package controlplanecomponent

import (
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"

	capiutil "sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Predicate func(cpContext WorkloadContext) bool

type genericAdapter struct {
	adapt             func(cpContext WorkloadContext, resource client.Object) error
	predicate         Predicate
	platformPredicate Predicate // if false, completely skip this resource (no API calls)
	reconcileExisting bool      // if true, causes the existing resource to be fetched before adapting
}

type option func(*genericAdapter)

func WithAdaptFunction[T client.Object](adapt func(cpContext WorkloadContext, resource T) error) option {
	return func(ga *genericAdapter) {
		ga.adapt = func(cpContext WorkloadContext, resource client.Object) error {
			return adapt(cpContext, resource.(T))
		}
	}
}

func WithPredicate(predicate Predicate) option {
	return func(ga *genericAdapter) {
		ga.predicate = predicate
	}
}

// WithPlatformPredicate sets a predicate that determines if this resource is applicable to the current platform.
// If the predicate returns false, the resource is completely skipped (no API calls, no informer creation).
// Use this for platform-specific resources (e.g., Azure components on AWS).
// For configuration-based enabling/disabling (where cleanup is needed), use WithPredicate instead.
func WithPlatformPredicate(predicate Predicate) option {
	return func(ga *genericAdapter) {
		ga.platformPredicate = predicate
	}
}

// ReconcileExisting can be used as an option when the existing resource should be fetched
// and passed to the adapt function. This is necessary for resources such as certificates that
// can result in a change every time we reconcile if we don't load the existing one first.
func ReconcileExisting() option {
	return func(ga *genericAdapter) {
		ga.reconcileExisting = true
	}
}

func (ga *genericAdapter) reconcile(cpContext ControlPlaneContext, obj client.Object) error {
	workloadContext := cpContext.workloadContext()

	// Check platformPredicate first - if false, completely skip this resource
	// This prevents any API calls for platform-specific resources on the wrong platform
	if ga.platformPredicate != nil && !ga.platformPredicate(workloadContext) {
		return nil
	}

	// Check regular predicate - if false, attempt cleanup
	// This handles configuration-based enabling/disabling where resources should be removed when disabled
	if ga.predicate != nil && !ga.predicate(workloadContext) {
		// get the existing object to read its ownerRefs
		existing := obj.DeepCopyObject().(client.Object)
		err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(obj), existing)

		if err == nil {
			objOwnerRefs := existing.GetOwnerReferences()
			ownerRefHCP := config.OwnerRefFrom(cpContext.HCP)
			if capiutil.HasOwnerRef(objOwnerRefs, *ownerRefHCP.Reference) {
				// delete the object only if it has HCP ownerRef
				_, err := util.DeleteIfNeeded(cpContext, cpContext.Client, obj)
				return err
			}
			return nil
		} else if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return err
		}
		return nil
	}

	if ga.reconcileExisting {
		existing := obj.DeepCopyObject().(client.Object)
		if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(existing), existing); err == nil {
			obj = existing
		}
	}

	if ga.adapt != nil {
		if err := ga.adapt(workloadContext, obj); err != nil {
			return err
		}
	}
	if _, err := cpContext.ApplyManifest(cpContext, cpContext.Client, obj); err != nil {
		return err
	}

	return nil
}
