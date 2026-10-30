package haproxy

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolveSharedIngressWorkerAPIEndpoint reads the worker API address and port
// from HCP status, preferring IP over hostname.
func ResolveSharedIngressWorkerAPIEndpoint(ctx context.Context, c client.Reader, key client.ObjectKey) (string, int32, error) {
	hcp := &hyperv1.HostedControlPlane{}
	if err := c.Get(ctx, key, hcp); err != nil {
		return "", 0, fmt.Errorf("failed to get hosted control plane: %w", err)
	}
	endpoint := hcp.Status.SharedIngressWorkerAPIEndpoint
	if endpoint.Port == 0 {
		return "", 0, fmt.Errorf("waiting on hosted control plane shared ingress worker API port")
	}
	address := endpoint.IP
	if address == "" {
		address = endpoint.Hostname
	}
	if address == "" {
		return "", 0, fmt.Errorf("waiting on hosted control plane shared ingress worker API endpoint")
	}
	return address, endpoint.Port, nil
}
