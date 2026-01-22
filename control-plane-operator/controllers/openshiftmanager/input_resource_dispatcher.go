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
	// as of today, the initial list of resources
	// always contains only "exact resources".
	// Therefore, if there are no filters defined,
	// we are not interested in that object.
	//
	// see: https://github.com/openshift/cluster-authentication-operator/blob/7f4a59434336c25a05c821fd0e9d94e6a30a8644/pkg/cmd/mom/input_resources_command.go#L18
	for _, filter := range d.filters[gvk] {
		if filter(cObj) {
			d.eventsCh <- event.GenericEvent{Object: cObj}
			return
		}
	}
}

func (d *inputResourceDispatcher) ResultChan() <-chan event.GenericEvent {
	return d.eventsCh
}
