package controlplanecomponent

import (
	"github.com/openshift/hypershift/support/util"

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
		_, err := util.DeleteIfNeeded(cpContext, cpContext.Client, obj)
		return err
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
