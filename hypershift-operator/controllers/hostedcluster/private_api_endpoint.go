package hostedcluster

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/config"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/netutil"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *HostedClusterReconciler) reconcileHostedControlPlane(ctx context.Context, hcp *hyperv1.HostedControlPlane, hc *hyperv1.HostedCluster, autoscaling, terminationHandler bool, certRenewalAnnotations func() (map[string]string, error)) error {
	if err := reconcileHostedControlPlane(hcp, hc, autoscaling, terminationHandler, certRenewalAnnotations); err != nil {
		return err
	}
	return r.reconcilePrivateAPIServerCleanup(ctx, hcp, hc)
}

func (r *HostedClusterReconciler) reconcilePrivateAPIServerCleanup(ctx context.Context, hcp *hyperv1.HostedControlPlane, hc *hyperv1.HostedCluster) error {
	delete(hcp.Annotations, config.PrivateAPIServerCleanupAnnotation)
	strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.APIServer)
	if strategy == nil || strategy.Type != hyperv1.LoadBalancer || netutil.IsPrivateHC(hc) {
		return nil
	}
	// These workers do not report NodePool rollout completion, so an empty NodePool list is not proof of cutover.
	if karpenterutil.IsKarpenterEnabled(hc.Spec.AutoNode) || controllerutil.ContainsFinalizer(hcp, karpenterutil.KarpenterFinalizer) {
		return nil
	}
	if _, unmanaged := hc.Annotations[hyperv1.DisableMachineManagement]; unmanaged {
		return nil
	}
	privateService := cpomanifests.KubeAPIServerPrivateService(hcp.Namespace)
	if err := r.Get(ctx, client.ObjectKeyFromObject(privateService), privateService); err != nil {
		if !apierrors.IsNotFound(err) || hc.Spec.Platform.Type != hyperv1.AWSPlatform {
			return client.IgnoreNotFound(err)
		}
		// Resume an interrupted cleanup even if the Service disappeared before its AWS dependency.
		endpointService := &hyperv1.AWSEndpointService{}
		endpointService.Name = privateService.Name
		endpointService.Namespace = hcp.Namespace
		if err := r.Get(ctx, client.ObjectKeyFromObject(endpointService), endpointService); err != nil {
			return client.IgnoreNotFound(err)
		}
	}
	if hc.Status.KubeConfig == nil {
		return nil
	}
	endpoint, err := nodepool.EndpointConfigString(ctx, r.Client, hc)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to resolve public API endpoint for private endpoint cleanup: %w", err)
	}
	// The kubeconfig can still reference the old private endpoint while CPO provisions the public LB.
	if strings.Contains(endpoint, ".hypershift.local") {
		return nil
	}
	nodePools := &hyperv1.NodePoolList{}
	if err := r.List(ctx, nodePools, client.InNamespace(hc.Namespace)); err != nil {
		return fmt.Errorf("failed to list NodePools for private API endpoint cleanup: %w", err)
	}
	for i := range nodePools.Items {
		np := &nodePools.Items[i]
		if np.Spec.ClusterName == hc.Name && !nodepool.IsAPIEndpointApplied(np, endpoint) {
			return nil
		}
	}
	if hcp.Annotations == nil {
		hcp.Annotations = map[string]string{}
	}
	hcp.Annotations[config.PrivateAPIServerCleanupAnnotation] = endpoint
	return nil
}
