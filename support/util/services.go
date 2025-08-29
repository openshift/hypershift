package util

import (
	"fmt"
	"strings"
	"time"

	"github.com/openshift/hypershift/support/events"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/duration"
)

func CollectLBMessageIfNotProvisioned(svc *corev1.Service, messageCollector events.MessageCollector) (string, error) {
	if len(svc.Status.LoadBalancer.Ingress) > 0 {
		return "", nil
	}
	message := fmt.Sprintf("%s load balancer is not provisioned; %v since creation.", svc.Name, duration.ShortHumanDuration(time.Since(svc.CreationTimestamp.Time)))
	var eventMessages []string
	eventMessages, err := messageCollector.ErrorMessages(svc)
	if err != nil {
		return message, fmt.Errorf("failed to get events for service %s/%s: %w", svc.Namespace, svc.Name, err)
	}
	if len(eventMessages) > 0 {
		message = fmt.Sprintf("%s load balancer is not provisioned: %s", svc.Name, strings.Join(eventMessages, "; "))
	}

	return message, nil
}
