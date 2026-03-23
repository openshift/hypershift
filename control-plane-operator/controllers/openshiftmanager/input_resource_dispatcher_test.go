package openshiftmanager

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/stretchr/testify/require"
)

func TestInputResourceDispatcherHandle(t *testing.T) {
	wellKnownGVK := schema.GroupVersionKind{Group: "example.io", Version: "v1", Kind: "Widget"}
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "widget-a",
			Namespace: "default",
		},
	}

	scenarios := []struct {
		name           string
		filters        map[schema.GroupVersionKind][]inputResourceEventFilter
		inputGVK       schema.GroupVersionKind
		inputObj       client.Object
		expectedEvents []event.GenericEvent
	}{
		{
			name: "dispatches matching filter",
			filters: map[schema.GroupVersionKind][]inputResourceEventFilter{
				wellKnownGVK: {
					func(cObj client.Object) bool {
						return cObj.GetName() == "widget-a"
					},
				},
			},
			inputGVK: wellKnownGVK,
			inputObj: obj,
			expectedEvents: []event.GenericEvent{
				{Object: obj},
			},
		},
		{
			name: "does not dispatch when filters do not match",
			filters: map[schema.GroupVersionKind][]inputResourceEventFilter{
				wellKnownGVK: {
					func(cObj client.Object) bool {
						return cObj.GetName() == "widget-b"
					},
				},
			},
			inputGVK: wellKnownGVK,
			inputObj: obj,
		},
		{
			name:     "does not dispatch when gvk has no filters",
			filters:  map[schema.GroupVersionKind][]inputResourceEventFilter{},
			inputGVK: wellKnownGVK,
			inputObj: obj,
		},
		{
			name: "dispatches when any filter matches",
			filters: map[schema.GroupVersionKind][]inputResourceEventFilter{
				wellKnownGVK: {
					func(cObj client.Object) bool {
						return cObj.GetName() == "widget-b"
					},
					func(cObj client.Object) bool {
						return cObj.GetNamespace() == "default"
					},
				},
			},
			inputGVK: wellKnownGVK,
			inputObj: obj,
			expectedEvents: []event.GenericEvent{
				{Object: obj},
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			dispatcher := newInputResourceDispatcher(scenario.filters)
			// dispatch in a goroutine for simplicity with an unbuffered channel
			go dispatcher.Handle(scenario.inputGVK, scenario.inputObj)

			events := readEvents(t, dispatcher.ResultChan(), len(scenario.expectedEvents))
			require.Equal(t, scenario.expectedEvents, events)
			ensureNoMoreEvents(t, dispatcher.ResultChan())
		})
	}
}

func readEvents(t *testing.T, ch <-chan event.GenericEvent, expected int) []event.GenericEvent {
	if expected == 0 {
		return nil
	}

	events := make([]event.GenericEvent, 0, expected)
	for i := 0; i < expected; i++ {
		select {
		case evt := <-ch:
			events = append(events, evt)
		case <-time.After(100 * time.Millisecond):
			require.Failf(t, "expected event not received", "received %d/%d events", len(events), expected)
		}
	}

	return events
}

func ensureNoMoreEvents(t *testing.T, ch <-chan event.GenericEvent) {
	select {
	case ev := <-ch:
		require.Failf(t, "unexpected event received", "got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
