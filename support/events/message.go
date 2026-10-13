package events

import (
	"context"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	EventInvolvedObjectUIDField = "involvedObject.uid"
)

type MessageCollector interface {
	ErrorMessages(resource crclient.Object) ([]string, error)
}

type messageCollector struct {
	ctx    context.Context
	client crclient.Client
}

func NewMessageCollector(ctx context.Context, client crclient.Client) MessageCollector {
	return &messageCollector{
		ctx:    ctx,
		client: client,
	}
}

func (c *messageCollector) ErrorMessages(resource crclient.Object) ([]string, error) {
	events := &corev1.EventList{}
	if err := c.client.List(c.ctx, events, &crclient.ListOptions{
		Namespace:     resource.GetNamespace(),
		FieldSelector: fields.OneTermEqualSelector(EventInvolvedObjectUIDField, string(resource.GetUID())),
	}); err != nil {
		return nil, err
	}
	sort.Slice(events.Items, func(i, j int) bool {
		// comparison is reversed to result in most recent first
		return events.Items[j].CreationTimestamp.Time.Before(events.Items[i].CreationTimestamp.Time)
	})
	messageMap := map[string]string{}
	for _, event := range events.Items {
		if event.Type == "Normal" {
			continue
		}
		// Only keep one message per event reason
		if _, hasMessage := messageMap[event.Reason]; !hasMessage {
			messageMap[event.Reason] = event.Message
		}
	}
	messages := make([]string, 0, len(messageMap))
	for _, msg := range messageMap {
		messages = append(messages, msg)
	}
	return messages, nil
}
