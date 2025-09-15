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
	reconcileExisting bool // if true, causes the existing resource to be fetched before adapting
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
