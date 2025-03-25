package controlplanecomponent

import (
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"
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

func (ga *genericAdapter) reconcile(cpContext ControlPlaneContext, componentName string, manifestName string) error {
	workloadContext := cpContext.workloadContext()
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	obj, _, err := assets.LoadManifest(componentName, manifestName)
	if err != nil {
		return err
	}
	obj.SetNamespace(cpContext.HCP.Namespace)

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

	ownerRef.ApplyTo(obj)
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
