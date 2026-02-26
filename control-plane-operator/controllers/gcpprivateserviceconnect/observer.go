package gcpprivateserviceconnect

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	gcpInternalLoadBalancerType   = "Internal"
	gcpLoadBalancerTypeAnnotation = "networking.gke.io/load-balancer-type"
)

// GCPPrivateServiceObserver watches a router Service with Internal Load Balancer
// and reconciles a GCPPrivateServiceConnect CR representation for it.
type GCPPrivateServiceObserver struct {
	client.Client

	log logr.Logger

	ControllerName   string
	ServiceNamespace string
	ServiceName      string
	HCPNamespace     string
	upsert.CreateOrUpdateProvider
}

func ControllerName(name string) string {
	return fmt.Sprintf("%s-observer", name)
}

func (r *GCPPrivateServiceObserver) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.log = ctrl.Log.WithName(r.ControllerName).WithValues("name", r.ServiceName, "namespace", r.ServiceNamespace)

	return ctrl.NewControllerManagedBy(mgr).
		Named(r.ControllerName).
		For(&corev1.Service{}).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
		}).
		Complete(r)
}

func (r *GCPPrivateServiceObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != r.ServiceName {
		return ctrl.Result{}, nil
	}

	r.log.Info("reconciling")

	// Fetch the Service
	svc := &corev1.Service{}
	if err := r.Get(ctx, req.NamespacedName, svc); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Info("service not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Verify this is an Internal Load Balancer
	if !isInternalLoadBalancer(svc) {
		r.log.Info("service is not Internal LoadBalancer type, skipping", "loadBalancerType", svc.Annotations[gcpLoadBalancerTypeAnnotation])
		return ctrl.Result{}, nil
	}

	// Extract LoadBalancer IP and validate it's ready
	loadBalancerIP, hasValidIP := supportutil.ExtractLoadBalancerIP(svc)
	if !hasValidIP {
		r.log.Info("LoadBalancer IP not ready yet")
		return ctrl.Result{}, nil
	}

	// Find HostedControlPlane from service OwnerReference
	hcpName := supportutil.ExtractHostedControlPlaneOwnerName(svc.OwnerReferences)
	if hcpName == "" {
		return ctrl.Result{}, fmt.Errorf("service does not have HostedControlPlane owner reference")
	}

	// Fetch HostedControlPlane for ConsumerAcceptList
	hcp := &hyperv1.HostedControlPlane{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.HCPNamespace, Name: hcpName}, hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// Return early if HostedControlPlane is deleted
	if !hcp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Extract ConsumerAcceptList from HostedControlPlane
	consumerAcceptList := getConsumerAcceptList(hcp)

	// Create/Update GCPPrivateServiceConnect CR
	if err := r.reconcileGCPPrivateServiceConnect(ctx, hcp, loadBalancerIP, consumerAcceptList); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile GCPPrivateServiceConnect: %w", err)
	}

	r.log.Info("reconcile complete", "request", req, "loadBalancerIP", loadBalancerIP)
	return ctrl.Result{}, nil
}

func (r *GCPPrivateServiceObserver) reconcileGCPPrivateServiceConnect(ctx context.Context, hcp *hyperv1.HostedControlPlane, loadBalancerIP string, consumerAcceptList []string) error {
	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.ServiceName,
			Namespace: r.HCPNamespace,
		},
	}

	_, err := r.CreateOrUpdate(ctx, r.Client, gcpPSC, func() error {
		// Set OwnerReference to HostedControlPlane for lifecycle management
		gcpPSC.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: hyperv1.GroupVersion.String(),
			Kind:       "HostedControlPlane",
			Name:       hcp.Name,
			UID:        hcp.UID,
			Controller: ptr.To(true),
		}}

		// Copy HostedCluster annotation from HCP for direct lookup by management-side controller
		if gcpPSC.Annotations == nil {
			gcpPSC.Annotations = make(map[string]string)
		}
		if hcAnnotation, exists := hcp.Annotations[supportutil.HostedClusterAnnotation]; exists {
			gcpPSC.Annotations[supportutil.HostedClusterAnnotation] = hcAnnotation
		}

		// Set spec fields
		gcpPSC.Spec.LoadBalancerIP = loadBalancerIP
		gcpPSC.Spec.ConsumerAcceptList = consumerAcceptList
		// ForwardingRuleName is left empty - populated by hypershift-operator reconciler

		return nil
	})

	return err
}

// getConsumerAcceptList extracts the consumer accept list from HostedControlPlane
func getConsumerAcceptList(hcp *hyperv1.HostedControlPlane) []string {
	if hcp.Spec.Platform.GCP == nil {
		return nil
	}

	// Use the GCP project as the consumer accept list entry
	// This allows the service attachment to be accessed by the same project
	return []string{hcp.Spec.Platform.GCP.Project}
}

// isInternalLoadBalancer checks if the service is configured as an Internal Load Balancer
func isInternalLoadBalancer(svc *corev1.Service) bool {
	return svc.Annotations[gcpLoadBalancerTypeAnnotation] == gcpInternalLoadBalancerType
}
