package openshiftmanager

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type inputResourceEventFilter func(obj client.Object) bool

// inputResourceDispatcher is a simple dispatcher that applies GVK scoped filters
// and forwards matching events.
//
// Each GVK has its own set of filters. Today these
// may include name/namespace checks, and in the future label selectors.
//
// Longer term, this dispatcher is expected to track which input resources are
// associated with which operator.
type inputResourceDispatcher struct {
	eventsCh chan event.GenericEvent
	filters  map[schema.GroupVersionKind][]inputResourceEventFilter
}

func newInputResourceDispatcher(filters map[schema.GroupVersionKind][]inputResourceEventFilter) *inputResourceDispatcher {
	return &inputResourceDispatcher{
		eventsCh: make(chan event.GenericEvent),
		filters:  filters,
	}
}

func (d *inputResourceDispatcher) Handle(gvk schema.GroupVersionKind, cObj client.Object) {
	filters := d.filters[gvk]
	if len(filters) == 0 {
		d.eventsCh <- event.GenericEvent{Object: cObj}
		return
	}

	for _, filter := range filters {
		if filter(cObj) {
			d.eventsCh <- event.GenericEvent{Object: cObj}
			return
		}
	}
}

func (d *inputResourceDispatcher) ResultChan() <-chan event.GenericEvent {
	return d.eventsCh
}
