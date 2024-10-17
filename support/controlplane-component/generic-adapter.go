package controlplanecomponent

import (
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/config"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Predicate func(cpContext ControlPlaneContext) bool

type genericAdapter struct {
	adapt     func(cpContext ControlPlaneContext, resource client.Object) error
	predicate Predicate
}

type option func(*genericAdapter)

func WithAdaptFunction[T client.Object](adapt func(cpContext ControlPlaneContext, resource T) error) option {
	return func(ga *genericAdapter) {
		ga.adapt = func(cpContext ControlPlaneContext, resource client.Object) error {
			return adapt(cpContext, resource.(T))
		}
	}
}

func WithPredicate(predicate Predicate) option {
	return func(ga *genericAdapter) {
		ga.predicate = predicate
	}
}

func (ga *genericAdapter) reconcile(cpContext ControlPlaneContext, componentName string, manifestName string) error {
	hcp := cpContext.HCP
	ownerRef := config.OwnerRefFrom(hcp)

	if ga.predicate != nil && !ga.predicate(cpContext) {
		return nil
	}

	obj, _, err := assets.LoadManifest(componentName, manifestName)
	if err != nil {
		return err
	}
	obj.SetNamespace(cpContext.HCP.Namespace)
	ownerRef.ApplyTo(obj)

	if ga.adapt != nil {
		if err := ga.adapt(cpContext, obj); err != nil {
			return err
		}
	}
	if _, err := cpContext.CreateOrUpdateV2(cpContext, cpContext.Client, obj); err != nil {
		return err
	}

	return nil
}
