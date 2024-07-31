package controlplanecomponent

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Predicatefn func(cpContext ControlPlaneContext) bool

type GenericReconciler struct {
	ReconcileFn func(cpContext ControlPlaneContext, resource client.Object) error
	Manifestfn  func(hcpNamespace string) client.Object
	Predicatefn Predicatefn
}

type genericReconcilerBuilder[T client.Object] struct {
	name        string
	forInput    T
	reconcileFn func(cpContext ControlPlaneContext, resource T) error
	predicatefn Predicatefn
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

func (b *genericReconcilerBuilder[T]) WithPredicate(predicatefn Predicatefn) *genericReconcilerBuilder[T] {
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
		Manifestfn: func(hcpNamespace string) client.Object {
			object := b.forInput
			object.SetNamespace(hcpNamespace)
			object.SetName(b.name)

			return object
		},
		Predicatefn: b.predicatefn,
	}
}

// DisableIfAnnotationExist is a helper predicte for the common use case of disabling a resource when an annotation exists.
func DisableIfAnnotationExist(annotation string) Predicatefn {
	return func(cpContext ControlPlaneContext) bool {
		if _, exists := cpContext.Hcp.Annotations[annotation]; exists {
			return false
		}
		return true
	}
}
