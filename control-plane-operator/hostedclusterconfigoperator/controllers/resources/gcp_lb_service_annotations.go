package resources

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	legacyRegionalInternalLoadBalancerClass = "networking.gke.io/l4-regional-internal-legacy"
	legacyRegionalExternalLoadBalancerClass = "networking.gke.io/l4-regional-external-legacy"
	regionalExternalLoadBalancerClass       = "networking.gke.io/l4-regional-external"
	l4RBSAnnotation                         = "cloud.google.com/l4-rbs"
	l4RBSEnabled                            = "enabled"
	netLBFinalizerV2                        = "gke.networking.io/l4-netlb-v2"
	netLBFinalizerV3                        = "gke.networking.io/l4-netlb-v3"
)

// reconcileGCPLoadBalancerServiceAnnotations merges HCP resource labels into
// Services managed by the legacy GCP CCM. CCM reconciles the native annotation
// to the existing forwarding rule, so this also converges Services created
// before an HCP resource-label change.
func (r *reconciler) reconcileGCPLoadBalancerServiceAnnotations(ctx context.Context, hcp *hyperv1.HostedControlPlane) []error {
	services := &corev1.ServiceList{}
	if err := r.client.List(ctx, services); err != nil {
		return []error{fmt.Errorf("list LoadBalancer Services: %w", err)}
	}

	desiredLabels := gcputil.ResourceLabels(hcp)
	var errs []error
	for i := range services.Items {
		service := &services.Items[i]
		if !isLegacyGCPLoadBalancerService(service) {
			continue
		}

		annotations, changed, err := gcputil.ReconcileLBResourceLabelAnnotations(service.Annotations, desiredLabels)
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile GCP resource labels for Service %s/%s: %w", service.Namespace, service.Name, err))
			continue
		}
		if !changed {
			continue
		}

		before := service.DeepCopy()
		service.Annotations = annotations
		if err := r.client.Patch(ctx, service, client.MergeFrom(before)); err != nil {
			errs = append(errs, fmt.Errorf("patch GCP resource labels for Service %s/%s: %w", service.Namespace, service.Name, err))
		}
	}
	return errs
}

// isLegacyGCPLoadBalancerService selects Services handled by the legacy GCP
// cloud-controller-manager implementation that supports
// cloud.google.com/load-balancer-resource-labels. Services claimed by the newer
// regional-backend-service controllers, or by an unrelated LoadBalancerClass,
// must be left to their owning controller.
func isLegacyGCPLoadBalancerService(service *corev1.Service) bool {
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return false
	}

	if service.Spec.LoadBalancerClass != nil {
		class := *service.Spec.LoadBalancerClass
		return class == legacyRegionalInternalLoadBalancerClass || class == legacyRegionalExternalLoadBalancerClass
	}

	if service.Annotations[l4RBSAnnotation] == l4RBSEnabled {
		return false
	}
	for _, finalizer := range service.Finalizers {
		if finalizer == netLBFinalizerV2 || finalizer == netLBFinalizerV3 {
			return false
		}
	}
	return true
}
