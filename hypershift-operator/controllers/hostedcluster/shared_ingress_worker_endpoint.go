package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/reconcilerpolicy"
	"github.com/openshift/hypershift/support/statuspatching"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *HostedClusterReconciler) reconcileSharedIngressWorkerEndpoint(ctx context.Context, hc *hyperv1.HostedCluster) error {
	hcp := controlplaneoperator.HostedControlPlane(manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), hc.Name)
	if err := r.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get hosted control plane for shared ingress endpoint: %w", err)
	}
	return statuspatching.PatchStatus(ctx, r.Client, hcp, func() error {
		endpoint := hyperv1.SharedIngressWorkerAPIEndpoint{}
		if netutil.UseSharedIngressHC(hc) && !netutil.IsPrivateHC(hc) {
			router := sharedingress.RouterPublicService()
			if err := r.Get(ctx, client.ObjectKeyFromObject(router), router); err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to get shared ingress router service: %w", err)
				}
			} else if len(router.Status.LoadBalancer.Ingress) > 0 {
				first := router.Status.LoadBalancer.Ingress[0]
				if first.IP != "" {
					endpoint.IP = first.IP
				} else if first.Hostname != "" {
					endpoint.Hostname = first.Hostname
				}
			}
		}
		if endpoint.IP != "" || endpoint.Hostname != "" {
			endpoint.Port = sharedingress.KASSVCLBPort
		}
		hcp.Status.SharedIngressWorkerAPIEndpoint = endpoint
		return nil
	})
}

// enqueueHostedClustersForSharedIngressRouter maps a shared router event to the
// in-scope HostedClusters that publish its address.
func enqueueHostedClustersForSharedIngressRouter(c client.Client) handler.MapFunc {
	return func(ctx context.Context, _ client.Object) []reconcile.Request {
		hcList := &hyperv1.HostedClusterList{}
		if err := c.List(ctx, hcList); err != nil {
			ctrllog.Log.Error(err, "failed to list hosted clusters for shared ingress router event")
			return nil
		}

		scope := reconcilerpolicy.PredicatesForHostedClusterAnnotationScoping(c)
		var requests []reconcile.Request
		for i := range hcList.Items {
			hc := &hcList.Items[i]
			if netutil.UseSharedIngressHC(hc) && !netutil.IsPrivateHC(hc) && scope.Create(event.CreateEvent{Object: hc}) {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			}
		}
		return requests
	}
}
