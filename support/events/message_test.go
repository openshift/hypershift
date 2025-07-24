package events

import (
	"sort"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const fakeObjUID = "1234567890"

func TestErrorMessages(t *testing.T) {
	ev := func(msg, etype, reason string) corev1.Event {
		return corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: msg,
			},
			Type: etype,
			InvolvedObject: corev1.ObjectReference{
				UID: fakeObjUID,
			},
			Message: msg,
			Reason:  reason,
		}
	}
	evl := func(events ...corev1.Event) []corev1.Event {
		start := time.Now()
		// ensure events have distinct times
		for i := range events {
			events[i].CreationTimestamp = metav1.NewTime(start.Add(time.Duration(i) * 10 * time.Second))
		}
		return events
	}
	tests := []struct {
		name     string
		events   []corev1.Event
		expected []string
	}{
		{
			name:     "single event",
			events:   evl(ev("msg1", corev1.EventTypeWarning, "r1")),
			expected: []string{"msg1"},
		},
		{
			name:     "no warning events",
			events:   evl(ev("msg1", corev1.EventTypeNormal, "r1")),
			expected: []string{},
		},
		{
			name: "warning and info events",
			events: evl(
				ev("msg1", corev1.EventTypeNormal, "r1"),
				ev("msg2", corev1.EventTypeWarning, "r2"),
			),
			expected: []string{"msg2"},
		},
		{
			name: "multiple events with same reason",
			events: evl(
				ev("msg1", corev1.EventTypeWarning, "rr"),
				ev("msg2", corev1.EventTypeWarning, "rr"),
				ev("msg3", corev1.EventTypeWarning, "rr"),
			),
			// Expect only the most recent message to be returned
			expected: []string{"msg3"},
		},
		{
			name: "multiple events with different reasons",
			events: evl(
				ev("msg1", corev1.EventTypeWarning, "r1"),
				ev("msg2", corev1.EventTypeWarning, "r1"),
				ev("msg3", corev1.EventTypeWarning, "r2"),
			),
			expected: []string{"msg3", "msg2"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			eventList := &corev1.EventList{
				Items: test.events,
			}
			fakeObj := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: fakeObjUID,
				},
			}
			client := fake.NewClientBuilder().WithIndex(&corev1.Event{}, "involvedObject.uid", func(object client.Object) []string {
				return []string{string(object.(*corev1.Event).InvolvedObject.UID)}
			}).WithLists(eventList).Build()
			collector := NewMessageCollector(t.Context(), client)
			result, err := collector.ErrorMessages(fakeObj)
			g.Expect(err).ToNot(HaveOccurred())
			sort.Strings(result)
			sort.Strings(test.expected)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}
