package openshiftmanager

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type inputResourceEventFilter func(obj client.Object) bool

// inputResourceDispatcher is a simple dispatcher that applies GVK scoped filters
// and forwards matching operator.
//
// Each GVK has its own set of filters. Today these
// may include name/namespace checks, and in the future label selectors.
//
// Longer term, this dispatcher is expected to track which input resources are
// associated with which operator.
type inputResourceDispatcher struct {
	// eventsCh channel on which an operator name
	// to reconcile will be sent
	eventsCh chan event.TypedGenericEvent[string]
	filters  map[schema.GroupVersionKind][]inputResourceEventFilter
}

func newInputResourceDispatcher(filters map[schema.GroupVersionKind][]inputResourceEventFilter) *inputResourceDispatcher {
	return &inputResourceDispatcher{
		eventsCh: make(chan event.TypedGenericEvent[string]),
		filters:  filters,
	}
}

func (d *inputResourceDispatcher) Handle(gvk schema.GroupVersionKind, cObj client.Object) {
	filters := d.filters[gvk]
	if len(filters) == 0 {
		// for the POC we always return cao
		// TODO: implement proper operator discovery
		d.eventsCh <- event.TypedGenericEvent[string]{Object: "cluster-authentication-operator"}
		return
	}

	for _, filter := range filters {
		if filter(cObj) {
			// for the POC we always return cao
			// TODO: implement proper operator discovery
			d.eventsCh <- event.TypedGenericEvent[string]{Object: "cluster-authentication-operator"}
			return
		}
	}
}

// ResultChan returns a channel on which
// an operator name to reconcile will be sent
func (d *inputResourceDispatcher) ResultChan() <-chan event.TypedGenericEvent[string] {
	return d.eventsCh
}
