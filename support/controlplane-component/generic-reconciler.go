package controlplanecomponent

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Predicate func(cpContext ControlPlaneContext) bool

type GenericReconciler struct {
	ReconcileFn func(cpContext ControlPlaneContext, resource client.Object) error
	ManifestFn  func(hcpNamespace string) client.Object
	PredicateFn Predicate
}

type genericReconcilerBuilder[T client.Object] struct {
	name        string
	forInput    T
	reconcileFn func(cpContext ControlPlaneContext, resource T) error
	predicatefn Predicate
}

func NewReconcilerFor[T client.Object](object T) *genericReconcilerBuilder[T] {
	return &genericReconcilerBuilder[T]{
		forInput: object,
	}
}

func (b *genericReconcilerBuilder[T]) WithReconcileFunction(reconcileFn func(cpContext ControlPlaneContext, resource T) error) *genericReconcilerBuilder[T] {
	b.reconcileFn = reconcileFn
	return b
}

func (b *genericReconcilerBuilder[T]) WithPredicate(predicatefn Predicate) *genericReconcilerBuilder[T] {
	b.predicatefn = predicatefn
	return b
}

func (b *genericReconcilerBuilder[T]) WithName(name string) *genericReconcilerBuilder[T] {
	b.name = name
	return b
}

func (b *genericReconcilerBuilder[T]) Build() GenericReconciler {
	return GenericReconciler{
		ReconcileFn: func(cpContext ControlPlaneContext, resource client.Object) error {
			obj := resource.(T)
			return b.reconcileFn(cpContext, obj)
		},
		ManifestFn: func(hcpNamespace string) client.Object {
			object := b.forInput
			object.SetNamespace(hcpNamespace)
			object.SetName(b.name)

			return object
		},
		PredicateFn: b.predicatefn,
	}
}

// DisableIfAnnotationExist is a helper predicte for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) Predicate {
	return func(cpContext ControlPlaneContext) bool {
		if _, exists := cpContext.HCP.Annotations[annotation]; exists {
			return false
		}
		return true
	}
}
