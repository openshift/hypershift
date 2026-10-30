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
	"github.com/openshift/hypershift/support/reconcilerpolicy"
	"github.com/openshift/hypershift/support/statuspatching"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
)

const (
	routerServiceName        = "router"
	privateRouterServiceName = "private-router"

	backendServiceAnnotation    = "service.kubernetes.io/backend-service"
	tcpForwardingRuleAnnotation = "service.kubernetes.io/tcp-forwarding-rule"

	loadBalancerDiscoveryRetry = 30 * time.Second

	// labelOperationPollingInterval and labelOperationTimeout bound polling of
	// regional Compute operations. RegionOperations.Wait is best-effort and can
	// return before an operation completes, so retry the same operation instead
	// of issuing another setLabels request with a stale label fingerprint.
	labelOperationPollingInterval = 2 * time.Second
	labelOperationTimeout         = 5 * time.Minute

	managedLoadBalancerResourceLabelKeysAnnotation = "hypershift.openshift.io/gcp-lb-managed-resource-label-keys"

	gcpResourceLabelsAppliedCondition = "GCPResourceLabelsApplied"
	forwardingRulePendingReason       = "ForwardingRulePending"
	reconciliationFailedReason        = "ReconciliationFailed"
	reconciliationSucceededReason     = "ReconciliationSucceeded"
)

// RBAC permissions for GCPLoadBalancerLabelsReconciler.
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch;patch

// LoadBalancerLabelsComputeClient abstracts the Compute API calls used to label
// management-project forwarding rules.
type LoadBalancerLabelsComputeClient interface {
	ListForwardingRules(ctx context.Context, project, region, filter string) ([]*compute.ForwardingRule, error)
	GetForwardingRule(ctx context.Context, project, region, name string) (*compute.ForwardingRule, error)
	SetForwardingRuleLabels(ctx context.Context, project, region, name string, labels *compute.RegionSetLabelsRequest) (*compute.Operation, error)
	WaitForRegionalOperation(ctx context.Context, project, region, operation string) (*compute.Operation, error)
}

type loadBalancerLabelsComputeServiceAdapter struct {
	svc *compute.Service
}

func (a *loadBalancerLabelsComputeServiceAdapter) ListForwardingRules(ctx context.Context, project, region, filter string) ([]*compute.ForwardingRule, error) {
	var forwardingRules []*compute.ForwardingRule
	err := a.svc.ForwardingRules.List(project, region).Filter(filter).Context(ctx).Pages(ctx, func(page *compute.ForwardingRuleList) error {
		forwardingRules = append(forwardingRules, page.Items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return forwardingRules, nil
}

func (a *loadBalancerLabelsComputeServiceAdapter) GetForwardingRule(ctx context.Context, project, region, name string) (*compute.ForwardingRule, error) {
	return a.svc.ForwardingRules.Get(project, region, name).Context(ctx).Do()
}

func (a *loadBalancerLabelsComputeServiceAdapter) SetForwardingRuleLabels(ctx context.Context, project, region, name string, labels *compute.RegionSetLabelsRequest) (*compute.Operation, error) {
	return a.svc.ForwardingRules.SetLabels(project, region, name, labels).Context(ctx).Do()
}

func (a *loadBalancerLabelsComputeServiceAdapter) WaitForRegionalOperation(ctx context.Context, project, region, operation string) (*compute.Operation, error) {
	return a.svc.RegionOperations.Wait(project, region, operation).Context(ctx).Do()
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
		For(&hyperv1.HostedControlPlane{}, builder.WithPredicates(hostedControlPlaneResourceLabelsPredicate())).
		Watches(
			&corev1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.mapRouterServiceToHostedControlPlane),
			builder.WithPredicates(predicate.NewPredicateFuncs(isRouterService)),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Complete(r)
}

func hostedControlPlaneResourceLabelsPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			hcp, ok := e.Object.(*hyperv1.HostedControlPlane)
			return ok && hcp.Spec.Platform.GCP != nil
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHCP, ok := e.ObjectOld.(*hyperv1.HostedControlPlane)
			if !ok || oldHCP.Spec.Platform.GCP == nil {
				return false
			}
			newHCP, ok := e.ObjectNew.(*hyperv1.HostedControlPlane)
			if !ok || newHCP.Spec.Platform.GCP == nil {
				return false
			}
			return !equality.Semantic.DeepEqual(oldHCP.Spec.Platform.GCP.ResourceLabels, newHCP.Spec.Platform.GCP.ResourceLabels) ||
				oldHCP.Annotations[managedLoadBalancerResourceLabelKeysAnnotation] != newHCP.Annotations[managedLoadBalancerResourceLabelKeysAnnotation]
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
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
	if isPaused, duration := reconcilerpolicy.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	desiredLabels := gcputil.ResourceLabels(hcp)
	previouslyManagedLabelKeys := gcputil.ManagedResourceLabelKeys(hcp.Annotations, managedLoadBalancerResourceLabelKeysAnnotation)

	pending, err := r.reconcileRouterServices(ctx, hcp, desiredLabels, previouslyManagedLabelKeys)
	if err != nil {
		if conditionErr := r.patchResourceLabelsCondition(ctx, hcp, metav1.ConditionFalse, reconciliationFailedReason, err.Error()); conditionErr != nil {
			return ctrl.Result{}, fmt.Errorf("patch GCP resource labels condition: %w", conditionErr)
		}
		return ctrl.Result{}, err
	}
	if pending {
		message := "Waiting for router load-balancer forwarding rules to be created"
		if err := r.patchResourceLabelsCondition(ctx, hcp, metav1.ConditionFalse, forwardingRulePendingReason, message); err != nil {
			return ctrl.Result{}, fmt.Errorf("patch GCP resource labels condition: %w", err)
		}
		log.Info(message)
		return ctrl.Result{RequeueAfter: loadBalancerDiscoveryRetry}, nil
	}
	if err := r.patchResourceLabelsCondition(ctx, hcp, metav1.ConditionTrue, reconciliationSucceededReason, "GCP resource labels applied to router load-balancer forwarding rules"); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch GCP resource labels condition: %w", err)
	}
	if err := gcputil.UpdateManagedResourceLabelKeys(ctx, r.Client, hcp, managedLoadBalancerResourceLabelKeysAnnotation, desiredLabels); err != nil {
		return ctrl.Result{}, fmt.Errorf("update managed load-balancer resource label keys: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *GCPLoadBalancerLabelsReconciler) patchResourceLabelsCondition(ctx context.Context, hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) error {
	return statuspatching.PatchStatusCondition(ctx, r.Client, hcp, &hcp.Status.Conditions, metav1.Condition{
		Type:    gcpResourceLabelsAppliedCondition,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *GCPLoadBalancerLabelsReconciler) reconcileRouterServices(ctx context.Context, hcp *hyperv1.HostedControlPlane, desiredLabels map[string]string, previouslyManagedLabelKeys map[string]struct{}) (pending bool, _ error) {
	for _, serviceName := range []string{routerServiceName, privateRouterServiceName} {
		service := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: hcp.Namespace, Name: serviceName}, service); err != nil {
			if apierrors.IsNotFound(err) {
				// The private router is optional, and router Services can be created after
				// the HostedControlPlane. A Service event will trigger another reconcile.
				continue
			}
			return false, fmt.Errorf("get %s Service: %w", serviceName, err)
		}

		forwardingRuleName := ""
		if forwardingRuleAnnotation := service.Annotations[tcpForwardingRuleAnnotation]; forwardingRuleAnnotation != "" {
			forwardingRuleName = resourceName(forwardingRuleAnnotation)
		}
		var forwardingRules []*compute.ForwardingRule
		if forwardingRuleName != "" {
			callCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
			forwardingRule, err := r.GcpClient.GetForwardingRule(callCtx, r.ProjectID, r.Region, forwardingRuleName)
			cancel()
			if err != nil {
				if isNotFoundError(err) {
					pending = true
					continue
				}
				return false, fmt.Errorf("get forwarding rule %s for Service %s: %w", forwardingRuleName, serviceName, err)
			}
			if forwardingRule == nil {
				pending = true
				continue
			}
			forwardingRules = []*compute.ForwardingRule{forwardingRule}
		} else {
			backendServiceName := service.Annotations[backendServiceAnnotation]
			if backendServiceName == "" {
				pending = true
				continue
			}

			callCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
			// The regional ForwardingRules REST endpoint rejects filters on
			// backendService. Fall back to listing for Services that do not yet
			// expose the GKE-written forwarding-rule annotation.
			listedRules, err := r.GcpClient.ListForwardingRules(callCtx, r.ProjectID, r.Region, "")
			cancel()
			if err != nil {
				return false, fmt.Errorf("list forwarding rules for Service %s: %w", serviceName, err)
			}
			forwardingRules = filterForwardingRulesByBackendService(listedRules, backendServiceName)
		}

		found := len(forwardingRules) > 0
		for _, forwardingRule := range forwardingRules {
			labels, err := gcputil.MergeResourceLabels(forwardingRule.Labels, desiredLabels, previouslyManagedLabelKeys)
			if err != nil {
				return false, fmt.Errorf("merge labels for forwarding rule %s: %w", forwardingRule.Name, err)
			}
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
				return false, fmt.Errorf("set labels on forwarding rule %s: %w", forwardingRule.Name, err)
			}
			if err := waitForForwardingRuleLabelOperation(ctx, r.GcpClient, r.ProjectID, r.Region, forwardingRule.Name, operation); err != nil {
				return false, err
			}
		}
		if !found {
			pending = true
		}
	}
	return pending, nil
}

func filterForwardingRulesByBackendService(forwardingRules []*compute.ForwardingRule, backendServiceName string) []*compute.ForwardingRule {
	matched := make([]*compute.ForwardingRule, 0, len(forwardingRules))
	for _, forwardingRule := range forwardingRules {
		// Backend service names can overlap, so do not use substring matching.
		if resourceName(forwardingRule.BackendService) == backendServiceName {
			matched = append(matched, forwardingRule)
		}
	}
	return matched
}

// waitForForwardingRuleLabelOperation waits for a setLabels operation to complete
// and returns its terminal error, if any. RegionOperations.Wait is best-effort,
// so a non-terminal response is retried until the operation completes or times out.
func waitForForwardingRuleLabelOperation(ctx context.Context, gcpClient LoadBalancerLabelsComputeClient, project, region, forwardingRuleName string, operation *compute.Operation) error {
	if operation == nil {
		return fmt.Errorf("set labels operation for forwarding rule %s returned no operation", forwardingRuleName)
	}
	if operation.Error != nil {
		return fmt.Errorf("set labels operation for forwarding rule %s failed: %v", forwardingRuleName, operation.Error.Errors)
	}
	if operation.Status == "DONE" {
		return nil
	}
	if operation.Name == "" {
		return fmt.Errorf("set labels operation for forwarding rule %s is not complete and has no name", forwardingRuleName)
	}

	return wait.PollUntilContextTimeout(ctx, labelOperationPollingInterval, labelOperationTimeout, true, func(ctx context.Context) (bool, error) {
		completed, err := gcpClient.WaitForRegionalOperation(ctx, project, region, operation.Name)
		if err != nil {
			return false, fmt.Errorf("wait for set labels operation for forwarding rule %s: %w", forwardingRuleName, err)
		}
		if completed == nil {
			return false, fmt.Errorf("wait for set labels operation for forwarding rule %s returned no operation", forwardingRuleName)
		}
		if completed.Error != nil {
			return false, fmt.Errorf("set labels operation for forwarding rule %s failed: %v", forwardingRuleName, completed.Error.Errors)
		}
		return completed.Status == "DONE", nil
	})
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

func resourceName(resourceURL string) string {
	return path.Base(resourceURL)
}
