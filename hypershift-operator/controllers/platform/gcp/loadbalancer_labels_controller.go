package gcp

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/gcputil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
)

const (
	routerServiceName        = "router"
	privateRouterServiceName = "private-router"

	backendServiceAnnotation = "service.kubernetes.io/backend-service"

	loadBalancerDiscoveryRetry = 30 * time.Second
	labelOperationRetry        = 5 * time.Second
)

// RBAC permissions for GCPLoadBalancerLabelsReconciler.
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch

// LoadBalancerLabelsComputeClient abstracts the Compute API calls used to label
// management-project forwarding rules.
type LoadBalancerLabelsComputeClient interface {
	ListForwardingRules(ctx context.Context, project, region, filter string) ([]*compute.ForwardingRule, error)
	SetForwardingRuleLabels(ctx context.Context, project, region, name string, labels *compute.RegionSetLabelsRequest) (*compute.Operation, error)
}

type loadBalancerLabelsComputeServiceAdapter struct {
	svc *compute.Service
}

func (a *loadBalancerLabelsComputeServiceAdapter) ListForwardingRules(ctx context.Context, project, region, filter string) ([]*compute.ForwardingRule, error) {
	response, err := a.svc.ForwardingRules.List(project, region).Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (a *loadBalancerLabelsComputeServiceAdapter) SetForwardingRuleLabels(ctx context.Context, project, region, name string, labels *compute.RegionSetLabelsRequest) (*compute.Operation, error) {
	return a.svc.ForwardingRules.SetLabels(project, region, name, labels).Context(ctx).Do()
}

// GCPLoadBalancerLabelsReconciler applies HostedControlPlane resource labels to
// regional forwarding rules created for the public and private router Services.
//
// The Compute API does not provide a label operation for BackendService or
// NetworkEndpointGroup resources. Forwarding rules are therefore the only
// management-project load-balancer resources reconciled here.
type GCPLoadBalancerLabelsReconciler struct {
	client.Client
	GcpClient LoadBalancerLabelsComputeClient
	ProjectID string
	Region    string
	Log       logr.Logger
}

func (r *GCPLoadBalancerLabelsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.GcpClient == nil {
		gcpComputeService, err := InitGCPComputeService(context.Background())
		if err != nil {
			return fmt.Errorf("initialize GCP Compute service: %w", err)
		}
		r.GcpClient = &loadBalancerLabelsComputeServiceAdapter{svc: gcpComputeService}
	}
	if r.ProjectID == "" {
		r.ProjectID = os.Getenv("GCP_PROJECT")
	}
	if r.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT environment variable is required")
	}
	if r.Region == "" {
		r.Region = os.Getenv("GCP_REGION")
	}
	if r.Region == "" {
		return fmt.Errorf("GCP_REGION environment variable is required")
	}

	r.Log.Info("Initialized GCP load-balancer label controller", "projectID", r.ProjectID, "region", r.Region)
	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.HostedControlPlane{}).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.mapRouterServiceToHostedControlPlane),
			builder.WithPredicates(predicate.NewPredicateFuncs(isRouterService)),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Complete(r)
}

func (r *GCPLoadBalancerLabelsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("hostedControlPlane", req.NamespacedName)
	hcp := &hyperv1.HostedControlPlane{}
	if err := r.Get(ctx, req.NamespacedName, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get HostedControlPlane: %w", err)
	}
	if !hcp.DeletionTimestamp.IsZero() || hcp.Spec.Platform.Type != hyperv1.GCPPlatform {
		return ctrl.Result{}, nil
	}

	desiredLabels := gcputil.ResourceLabels(hcp)
	if len(desiredLabels) == 0 {
		return ctrl.Result{}, nil
	}

	pending, updated, err := r.reconcileRouterServices(ctx, hcp, desiredLabels)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pending {
		log.Info("Waiting for router load-balancer forwarding rules to be created")
		return ctrl.Result{RequeueAfter: loadBalancerDiscoveryRetry}, nil
	}
	if updated {
		// SetLabels is asynchronous. Re-read the forwarding rules shortly to verify
		// the operation has completed before relying on the normal event stream.
		return ctrl.Result{RequeueAfter: labelOperationRetry}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GCPLoadBalancerLabelsReconciler) reconcileRouterServices(ctx context.Context, hcp *hyperv1.HostedControlPlane, desiredLabels map[string]string) (pending, updated bool, _ error) {
	for _, serviceName := range []string{routerServiceName, privateRouterServiceName} {
		service := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: hcp.Namespace, Name: serviceName}, service); err != nil {
			if apierrors.IsNotFound(err) {
				// The private router is optional, and router Services can be created after
				// the HostedControlPlane. A Service event will trigger another reconcile.
				continue
			}
			return false, false, fmt.Errorf("get %s Service: %w", serviceName, err)
		}

		backendServiceName := service.Annotations[backendServiceAnnotation]
		if backendServiceName == "" {
			pending = true
			continue
		}

		callCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
		// The regional ForwardingRules REST endpoint rejects filters on
		// backendService. The management project has a small rule inventory, so
		// list it and match the backend service name exactly below.
		forwardingRules, err := r.GcpClient.ListForwardingRules(callCtx, r.ProjectID, r.Region, "")
		cancel()
		if err != nil {
			return false, false, fmt.Errorf("list forwarding rules for Service %s: %w", serviceName, err)
		}

		found := false
		for _, forwardingRule := range forwardingRules {
			// Backend service names can overlap, so do not use substring matching.
			if resourceName(forwardingRule.BackendService) != backendServiceName {
				continue
			}
			found = true
			labels := mergeResourceLabels(forwardingRule.Labels, desiredLabels)
			if maps.Equal(forwardingRule.Labels, labels) {
				continue
			}

			callCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
			operation, err := r.GcpClient.SetForwardingRuleLabels(callCtx, r.ProjectID, r.Region, forwardingRule.Name, &compute.RegionSetLabelsRequest{
				LabelFingerprint: forwardingRule.LabelFingerprint,
				Labels:           labels,
			})
			cancel()
			if err != nil {
				return false, false, fmt.Errorf("set labels on forwarding rule %s: %w", forwardingRule.Name, err)
			}
			if operation == nil {
				return false, false, fmt.Errorf("set labels operation for forwarding rule %s returned no operation", forwardingRule.Name)
			}
			if operation.Error != nil {
				return false, false, fmt.Errorf("set labels operation for forwarding rule %s failed: %v", forwardingRule.Name, operation.Error.Errors)
			}
			updated = true
		}
		if !found {
			pending = true
		}
	}
	return pending, updated, nil
}

func (r *GCPLoadBalancerLabelsReconciler) mapRouterServiceToHostedControlPlane(ctx context.Context, obj client.Object) []reconcile.Request {
	hcps := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcps, client.InNamespace(obj.GetNamespace())); err != nil {
		r.Log.Error(err, "Unable to list HostedControlPlanes for router Service", "service", client.ObjectKeyFromObject(obj))
		return nil
	}
	requests := make([]reconcile.Request, 0, len(hcps.Items))
	for i := range hcps.Items {
		hcp := &hcps.Items[i]
		if hcp.Spec.Platform.Type == hyperv1.GCPPlatform {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
		}
	}
	return requests
}

func isRouterService(obj client.Object) bool {
	return obj.GetName() == routerServiceName || obj.GetName() == privateRouterServiceName
}

func mergeResourceLabels(existing, desired map[string]string) map[string]string {
	merged := maps.Clone(existing)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range desired {
		merged[key] = value
	}
	return merged
}

func resourceName(resourceURL string) string {
	return path.Base(resourceURL)
}
