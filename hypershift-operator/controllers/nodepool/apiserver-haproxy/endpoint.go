package haproxy

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolveSharedIngressWorkerAPIEndpoint returns the shared ingress address and
// port workers use to reach the API, from HostedControlPlane status.
//
// allowSharedIngressServiceFallback reads the address from the shared ingress
// router Service while HostedControlPlane status does not carry it: on upgrade
// and while the HostedCluster is paused.
func ResolveSharedIngressWorkerAPIEndpoint(ctx context.Context, c client.Reader, key client.ObjectKey, allowSharedIngressServiceFallback bool) (string, int32, error) {
	hcp := &hyperv1.HostedControlPlane{}
	if err := c.Get(ctx, key, hcp); err != nil {
		return "", 0, fmt.Errorf("failed to get hosted control plane: %w", err)
	}
	endpoint := hcp.Status.SharedIngressWorkerAPIEndpoint
	if endpoint != nil && endpoint.Port != 0 && endpoint.Host != "" {
		return endpoint.Host, endpoint.Port, nil
	}
	if !allowSharedIngressServiceFallback {
		return "", 0, fmt.Errorf("waiting on hosted control plane shared ingress worker API endpoint")
	}
	return resolveSharedIngressRouterServiceEndpoint(ctx, c)
}

// resolveSharedIngressRouterServiceEndpoint returns the first address the shared
// ingress router Service reports.
func resolveSharedIngressRouterServiceEndpoint(ctx context.Context, c client.Reader) (string, int32, error) {
	router := sharedingress.RouterPublicService()
	if err := c.Get(ctx, client.ObjectKeyFromObject(router), router); err != nil {
		return "", 0, fmt.Errorf("failed to get shared ingress router service: %w", err)
	}
	if len(router.Status.LoadBalancer.Ingress) == 0 {
		return "", 0, fmt.Errorf("waiting on sharedIngress route service to report .Status.LoadBalancer.Ingress")
	}
	address := router.Status.LoadBalancer.Ingress[0].IP
	if address == "" {
		address = router.Status.LoadBalancer.Ingress[0].Hostname
	}
	if address == "" {
		return "", 0, fmt.Errorf("waiting on sharedIngress route service ingress to report an IP or hostname")
	}
	return address, sharedingress.KASSVCLBPort, nil
}
