// Package azureprivatelinkservice implements the control-plane side of Azure Private Link
// Service lifecycle for self-managed HyperShift hosted clusters.
//
// This package contains two controllers:
//
//   - Observer: Watches the kube-apiserver-private Service for an Azure Internal Load Balancer
//     IP and creates/updates an AzurePrivateLinkService CR with the discovered IP and platform
//     configuration from the HostedControlPlane spec.
//
//   - Reconciler: Watches AzurePrivateLinkService CRs and creates Azure Private Endpoint,
//     Private DNS Zone, VNet Link, and A record resources in the guest VNet to enable private
//     connectivity from the guest network to the hosted cluster API server.
//
// The observer bridges the gap between the KAS Service (created by the CPO infra reconciler)
// and the management-plane HO controller that creates the Azure PLS. The reconciler completes
// the private connectivity chain after the HO controller populates the PLS alias in the CR status.
//
// Condition progression on AzurePrivateLinkService status:
//
//	InternalLoadBalancerAvailable (HO) → PLSCreated (HO) →
//	  PrivateEndpointAvailable (CPO) → PrivateDNSAvailable (CPO) →
//	  AzurePrivateLinkServiceAvailable (CPO, overall)
package azureprivatelinkservice

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AzurePrivateLinkServiceObserver watches a KAS Service with an Azure Internal Load Balancer
// and reconciles an AzurePrivateLinkService CR representation for it.
type AzurePrivateLinkServiceObserver struct {
	client.Client

	ControllerName   string
	ServiceNamespace string
	ServiceName      string
	HCPNamespace     string
	upsert.CreateOrUpdateProvider
}

func ControllerName(name string) string {
	return fmt.Sprintf("%s-observer", name)
}

func (r *AzurePrivateLinkServiceObserver) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.ControllerName).
		For(&corev1.Service{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == r.ServiceName
		}))).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
		}).
		Complete(r)
}

func (r *AzurePrivateLinkServiceObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("name", r.ServiceName, "namespace", r.ServiceNamespace)
	logger.Info("reconciling")

	// Fetch the Service
	svc := &corev1.Service{}
	if err := r.Get(ctx, req.NamespacedName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("service not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get service: %w", err)
	}

	// Verify this is an Azure Internal Load Balancer
	if !isAzureInternalLoadBalancer(svc) {
		logger.Info("service does not have Azure internal load balancer annotation, skipping")
		return ctrl.Result{}, nil
	}

	// Extract LoadBalancer IP and validate it's ready
	loadBalancerIP, hasValidIP := supportutil.ExtractLoadBalancerIP(svc)
	if !hasValidIP {
		logger.Info("LoadBalancer IP not ready yet, will retry")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Find HostedControlPlane from service OwnerReference
	hcpName := supportutil.ExtractHostedControlPlaneOwnerName(svc.OwnerReferences)
	if hcpName == "" {
		return ctrl.Result{}, fmt.Errorf("service does not have HostedControlPlane owner reference")
	}

	// Fetch HostedControlPlane
	hcp := &hyperv1.HostedControlPlane{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.HCPNamespace, Name: hcpName}, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// Return early if HostedControlPlane is being deleted
	if !hcp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Create/Update AzurePrivateLinkService CR
	if err := r.reconcileAzurePrivateLinkService(ctx, hcp, loadBalancerIP); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile AzurePrivateLinkService: %w", err)
	}

	logger.Info("reconcile complete", "request", req, "loadBalancerIP", loadBalancerIP)
	return ctrl.Result{}, nil
}

func (r *AzurePrivateLinkServiceObserver) reconcileAzurePrivateLinkService(ctx context.Context, hcp *hyperv1.HostedControlPlane, loadBalancerIP string) error {
	azurePLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.ServiceName,
			Namespace: r.HCPNamespace,
		},
	}

	azurePlatform := hcp.Spec.Platform.Azure
	if azurePlatform == nil {
		return fmt.Errorf("HostedControlPlane %s/%s does not have Azure platform spec", hcp.Namespace, hcp.Name)
	}

	if azurePlatform.PrivateConnectivity == nil {
		return fmt.Errorf("HostedControlPlane %s/%s does not have Azure private connectivity config", hcp.Namespace, hcp.Name)
	}

	_, err := r.CreateOrUpdate(ctx, r.Client, azurePLS, func() error {
		// Set OwnerReference to HostedControlPlane for lifecycle management
		if err := controllerutil.SetControllerReference(hcp, azurePLS, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Copy HostedCluster annotation from HCP for direct lookup by management-side controller
		if azurePLS.Annotations == nil {
			azurePLS.Annotations = make(map[string]string)
		}
		if hcAnnotation, exists := hcp.Annotations[supportutil.HostedClusterAnnotation]; exists {
			azurePLS.Annotations[supportutil.HostedClusterAnnotation] = hcAnnotation
		}

		// Set spec fields from HCP Azure platform configuration
		azurePLS.Spec.LoadBalancerIP = loadBalancerIP
		azurePLS.Spec.SubscriptionID = azurePlatform.SubscriptionID
		azurePLS.Spec.ResourceGroupName = azurePlatform.ResourceGroupName
		azurePLS.Spec.Location = azurePlatform.Location
		azurePLS.Spec.NATSubnetID = azurePlatform.PrivateConnectivity.NATSubnetID
		azurePLS.Spec.AllowedSubscriptions = azurePlatform.PrivateConnectivity.AllowedSubscriptions
		azurePLS.Spec.GuestSubnetID = azurePlatform.SubnetID
		azurePLS.Spec.GuestVNetID = azurePlatform.VnetID

		return nil
	})

	return err
}

// isAzureInternalLoadBalancer checks if the service has the Azure internal load balancer annotation set to "true".
func isAzureInternalLoadBalancer(svc *corev1.Service) bool {
	return svc.Annotations[azureutil.InternalLoadBalancerAnnotation] == azureutil.InternalLoadBalancerValue
}
