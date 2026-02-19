package metricsproxy

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ScrapeTarget struct {
	PodName string
	PodIP   string
	Port    int32
}

type EndpointSliceDiscoverer struct {
	client    kubernetes.Interface
	namespace string
}

func NewEndpointSliceDiscoverer(client kubernetes.Interface, namespace string) *EndpointSliceDiscoverer {
	return &EndpointSliceDiscoverer{
		client:    client,
		namespace: namespace,
	}
}

func (d *EndpointSliceDiscoverer) Discover(ctx context.Context, serviceName string, port int32) ([]ScrapeTarget, error) {
	labelSelector := fmt.Sprintf("kubernetes.io/service-name=%s", serviceName)
	endpointSlices, err := d.client.DiscoveryV1().EndpointSlices(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list endpoint slices for service %s: %w", serviceName, err)
	}

	var targets []ScrapeTarget
	for _, es := range endpointSlices.Items {
		for _, endpoint := range es.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			podName := ""
			if endpoint.TargetRef != nil {
				podName = endpoint.TargetRef.Name
			}
			for _, addr := range endpoint.Addresses {
				targets = append(targets, ScrapeTarget{
					PodName: podName,
					PodIP:   addr,
					Port:    port,
				})
			}
		}
	}

	return targets, nil
}
