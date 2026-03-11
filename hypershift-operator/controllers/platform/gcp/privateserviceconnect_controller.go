package gcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	finalizer = "hypershift.openshift.io/gcp-private-service-connect"

	// gcpAPITimeout is the timeout for individual GCP API calls to prevent hung reconcilers.
	// GCP SDK has connection-level timeouts (dial: 30s, TLS: 10s) but no overall request timeout.
	gcpAPITimeout = 30 * time.Second
)

// RBAC permissions for GCPPrivateServiceConnect controller (documentation only - not used for code generation)
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=gcpprivateserviceconnects,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=gcpprivateserviceconnects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch

// GCPPrivateServiceConnectReconciler reconciles GCPPrivateServiceConnect resources
type GCPPrivateServiceConnectReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	GcpClient *compute.Service // GCP Compute API client for ForwardingRules, ServiceAttachments, and Subnets
	ProjectID string
	Region    string
	Log       logr.Logger
}

// SetupWithManager sets up the controller with the Manager.
func (r *GCPPrivateServiceConnectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize GCP Compute Service client
	gcpComputeService, err := InitGCPComputeService(context.Background())
	if err != nil {
		return fmt.Errorf("failed to initialize GCP Compute service: %w", err)
	}
	r.GcpClient = gcpComputeService

	// Extract GCP project ID from GCP_PROJECT environment variable
	projectID, err := r.extractGCPProjectFromEnv()
	if err != nil {
		return fmt.Errorf("failed to extract GCP project ID from environment: %w", err)
	}
	r.ProjectID = projectID

	// Extract GCP region from GCP_REGION environment variable
	region, err := r.extractGCPRegionFromEnv()
	if err != nil {
		return fmt.Errorf("failed to extract GCP region from environment: %w", err)
	}
	r.Region = region

	r.Log.Info("Initialized GCP platform information", "projectID", r.ProjectID, "region", r.Region)

	return ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.GCPPrivateServiceConnect{}).
		// Note: Add HostedCluster watching if needed for network configuration changes
		// Watches(&source.Kind{Type: &hyperv1.HostedCluster{}},
		//        handler.EnqueueRequestsFromMapFunc(r.mapHostedClusterToGCPPSC)).
		Complete(r)
}

// Reconcile reconciles GCPPrivateServiceConnect resources
func (r *GCPPrivateServiceConnectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", req.NamespacedName)

	// 1. Fetch GCPPrivateServiceConnect CR
	obj := &hyperv1.GCPPrivateServiceConnect{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get GCPPrivateServiceConnect: %w", err)
	}

	// Don't change the cached object
	gcpPSC := obj.DeepCopy()

	// 2. Handle deletion
	if !gcpPSC.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(gcpPSC, finalizer) {
			// If we previously removed our finalizer, don't delete again and return early
			return ctrl.Result{}, nil
		}
		completed, err := r.delete(ctx, gcpPSC)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
		}
		if !completed {
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		if controllerutil.ContainsFinalizer(gcpPSC, finalizer) {
			controllerutil.RemoveFinalizer(gcpPSC, finalizer)
			if err := r.Update(ctx, gcpPSC); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(gcpPSC, finalizer) {
		controllerutil.AddFinalizer(gcpPSC, finalizer)
		return ctrl.Result{}, r.Update(ctx, gcpPSC)
	}

	// 4. Find the hosted cluster using annotation (set by customer-side controller)
	hc, err := supportutil.HostedClusterFromAnnotation(ctx, r.Client, gcpPSC)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster: %w", err)
	}

	// 5. Check if reconciliation is paused
	if isPaused, duration := r.isReconciliationPaused(log, hc.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hc.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// 6. Reconcile the GCPPrivateServiceConnect Spec
	if _, err := r.CreateOrUpdate(ctx, r.Client, gcpPSC, func() error {
		return r.reconcileGCPPrivateServiceConnectSpec(ctx, gcpPSC, hc)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile GCPPrivateServiceConnect spec: %w", err)
	}

	// 7. Reconcile Service Attachment
	return r.reconcileServiceAttachment(ctx, gcpPSC, hc)
}

// reconcileGCPPrivateServiceConnectSpec reconciles the GCPPrivateServiceConnect spec fields
func (r *GCPPrivateServiceConnectReconciler) reconcileGCPPrivateServiceConnectSpec(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hc *hyperv1.HostedCluster) error {
	// Set ForwardingRuleName if not already populated
	if gcpPSC.Spec.ForwardingRuleName == "" {
		forwardingRuleName, err := r.lookupForwardingRuleName(ctx, gcpPSC)
		if err != nil {
			return fmt.Errorf("failed to lookup ForwardingRule: %w", err)
		}
		gcpPSC.Spec.ForwardingRuleName = forwardingRuleName
	}

	// Set NAT Subnet if not already populated
	if gcpPSC.Spec.NATSubnet == "" {
		natSubnet, err := r.discoverNATSubnet(ctx, gcpPSC)
		if err != nil {
			return fmt.Errorf("failed to discover NAT subnet: %w", err)
		}
		gcpPSC.Spec.NATSubnet = natSubnet
	}

	return nil
}

// lookupForwardingRuleName finds the ForwardingRule name by LoadBalancer IP
func (r *GCPPrivateServiceConnectReconciler) lookupForwardingRuleName(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) (string, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", gcpPSC.Name, "loadBalancerIP", gcpPSC.Spec.LoadBalancerIP)

	// Use AIP-160 filter syntax for exact string matching
	filter := fmt.Sprintf(`IPAddress = "%s"`, gcpPSC.Spec.LoadBalancerIP)

	apiCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer cancel()

	req := r.GcpClient.ForwardingRules.List(r.ProjectID, r.Region).Filter(filter)
	resp, err := req.Context(apiCtx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to list forwarding rules: %w", err)
	}

	if len(resp.Items) == 0 {
		log.V(1).Info("ForwardingRule not found for LoadBalancer IP, will retry later")
		// LoadBalancer might not be fully provisioned yet
		return "", nil // Return empty string, not an error
	}

	if len(resp.Items) > 1 {
		// This shouldn't happen, but handle gracefully
		log.Info("Multiple ForwardingRules found for LoadBalancer IP, using first one", "count", len(resp.Items))
	}

	forwardingRule := resp.Items[0]
	log.Info("Found ForwardingRule for LoadBalancer IP", "forwardingRule", forwardingRule.Name)

	return forwardingRule.Name, nil
}

// isSubnetInUse checks if a subnet is already being used by existing Service Attachments
func (r *GCPPrivateServiceConnectReconciler) isSubnetInUse(ctx context.Context, subnetName string) (bool, error) {
	apiCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer cancel()

	// List all Service Attachments in the region
	req := r.GcpClient.ServiceAttachments.List(r.ProjectID, r.Region)
	resp, err := req.Context(apiCtx).Do()
	if err != nil {
		return false, fmt.Errorf("failed to list service attachments: %w", err)
	}

	// Check if any Service Attachment is using this subnet
	for _, sa := range resp.Items {
		for _, natSubnet := range sa.NatSubnets {
			// NatSubnets contains full URLs like:
			// "projects/PROJECT_ID/regions/REGION/subnetworks/SUBNET_NAME"
			// Extract just the subnet name for comparison
			if strings.HasSuffix(natSubnet, "/"+subnetName) {
				return true, nil
			}
		}
	}

	return false, nil
}

// discoverNATSubnet discovers available PRIVATE_SERVICE_CONNECT subnets
func (r *GCPPrivateServiceConnectReconciler) discoverNATSubnet(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) (string, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", gcpPSC.Name)

	// 1. Check if NATSubnet already specified in CR
	if gcpPSC.Spec.NATSubnet != "" {
		log.Info("Using specified NAT subnet", "subnet", gcpPSC.Spec.NATSubnet)
		return gcpPSC.Spec.NATSubnet, nil
	}

	// 2. List subnets with PRIVATE_SERVICE_CONNECT purpose
	apiCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer cancel()

	req := r.GcpClient.Subnetworks.List(r.ProjectID, r.Region)
	resp, err := req.Context(apiCtx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to list subnets: %w", err)
	}

	// Find first available PRIVATE_SERVICE_CONNECT subnet not already in use.
	// Race conditions are not a concern since we use a single management cluster per GCP project.
	for _, subnet := range resp.Items {
		if subnet.Purpose == "PRIVATE_SERVICE_CONNECT" {
			// Check if this subnet is already in use by another Service Attachment
			inUse, err := r.isSubnetInUse(ctx, subnet.Name)
			if err != nil {
				log.Error(err, "Failed to check subnet usage", "subnet", subnet.Name)
				continue // Skip this subnet and try the next one
			}

			if !inUse {
				log.Info("Found available PSC subnet", "subnet", subnet.Name)
				return subnet.Name, nil
			}

			log.V(1).Info("Subnet already in use, trying next", "subnet", subnet.Name)
		}
	}

	return "", fmt.Errorf("no available PRIVATE_SERVICE_CONNECT subnet found in region %s", r.Region)
}

// reconcileServiceAttachment manages Service Attachment lifecycle
func (r *GCPPrivateServiceConnectReconciler) reconcileServiceAttachment(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hc *hyperv1.HostedCluster) (ctrl.Result, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", gcpPSC.Name)

	// 1. Construct unique Service Attachment name using cluster ID
	serviceAttachmentName := r.constructServiceAttachmentName(hc)

	// 2. Check if Service Attachment already exists
	getCtx, getCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer getCancel()

	existingServiceAttachment, err := r.GcpClient.ServiceAttachments.Get(r.ProjectID, r.Region, serviceAttachmentName).Context(getCtx).Do()
	if err != nil && !isNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get Service Attachment: %w", err)
	}

	if existingServiceAttachment != nil {
		// Update status if Service Attachment exist
		return r.updateStatusFromServiceAttachment(ctx, gcpPSC, existingServiceAttachment)
	}

	// 3. Validate required spec fields before creating Service Attachment
	if gcpPSC.Spec.ForwardingRuleName == "" || gcpPSC.Spec.NATSubnet == "" {
		log.Info("Required spec fields not yet populated, waiting for next reconciliation",
			"forwardingRuleName", gcpPSC.Spec.ForwardingRuleName,
			"natSubnet", gcpPSC.Spec.NATSubnet)
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// 4. Create new Service Attachment
	serviceAttachment := &compute.ServiceAttachment{
		Name:                 serviceAttachmentName,
		Description:          fmt.Sprintf("Service Attachment for HyperShift cluster %s", gcpPSC.Name),
		TargetService:        r.constructForwardingRuleURL(gcpPSC.Spec.ForwardingRuleName),
		ConnectionPreference: "ACCEPT_MANUAL",
		ConsumerAcceptLists:  r.buildConsumerAcceptLists(gcpPSC.Spec.ConsumerAcceptList),
		NatSubnets:           []string{r.constructSubnetURL(gcpPSC.Spec.NATSubnet)},
		EnableProxyProtocol:  false,
	}

	log.Info("Creating Service Attachment", "name", serviceAttachmentName)
	insertCtx, insertCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer insertCancel()

	op, err := r.GcpClient.ServiceAttachments.Insert(r.ProjectID, r.Region, serviceAttachment).Context(insertCtx).Do()
	if err != nil {
		return r.handleGCPError(ctx, gcpPSC, "ServiceAttachmentCreationFailed", err)
	}

	// 5. Check operation status - check for errors first, then check if still running
	if op.Error != nil && len(op.Error.Errors) > 0 {
		opErr := fmt.Errorf("operation failed: %s", op.Error.Errors[0].Message)
		return r.handleGCPError(ctx, gcpPSC, "ServiceAttachmentCreationFailed", opErr)
	}

	if op.Status != "DONE" {
		log.Info("Service Attachment creation in progress", "operation", op.Name, "status", op.Status)
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// 6. Operation completed - fetch the created Service Attachment and update status
	fetchCtx, fetchCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer fetchCancel()

	createdServiceAttachment, err := r.GcpClient.ServiceAttachments.Get(r.ProjectID, r.Region, serviceAttachmentName).Context(fetchCtx).Do()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get newly created Service Attachment: %w", err)
	}

	log.Info("Service Attachment created successfully", "name", serviceAttachmentName)
	return r.updateStatusFromServiceAttachment(ctx, gcpPSC, createdServiceAttachment)
}

// constructForwardingRuleURL builds the full GCP ForwardingRule URL
func (r *GCPPrivateServiceConnectReconciler) constructForwardingRuleURL(forwardingRuleName string) string {
	return fmt.Sprintf("projects/%s/regions/%s/forwardingRules/%s",
		r.ProjectID, r.Region, forwardingRuleName)
}

// constructSubnetURL builds the full GCP Subnet URL
func (r *GCPPrivateServiceConnectReconciler) constructSubnetURL(subnetName string) string {
	return fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s",
		r.ProjectID, r.Region, subnetName)
}

// constructServiceAttachmentURI builds the Service Attachment URI in the format expected by the CRD
func (r *GCPPrivateServiceConnectReconciler) constructServiceAttachmentURI(serviceAttachmentName string) string {
	return fmt.Sprintf("projects/%s/regions/%s/serviceAttachments/%s",
		r.ProjectID, r.Region, serviceAttachmentName)
}

// constructServiceAttachmentName builds a unique Service Attachment name using the cluster ID
// Format: psc-{clusterID} (prefix ensures name starts with a letter per GCP naming requirements)
func (r *GCPPrivateServiceConnectReconciler) constructServiceAttachmentName(hc *hyperv1.HostedCluster) string {
	return fmt.Sprintf("psc-%s", hc.Spec.ClusterID)
}

// buildConsumerAcceptLists builds the consumer accept list for Service Attachment
func (r *GCPPrivateServiceConnectReconciler) buildConsumerAcceptLists(acceptList []string) []*compute.ServiceAttachmentConsumerProjectLimit {
	var result []*compute.ServiceAttachmentConsumerProjectLimit
	for _, projectID := range acceptList {
		result = append(result, &compute.ServiceAttachmentConsumerProjectLimit{
			ProjectIdOrNum:  projectID,
			ConnectionLimit: 10, // Default connection limit per project
		})
	}
	return result
}

// updateStatusFromServiceAttachment updates the CR status based on Service Attachment state
func (r *GCPPrivateServiceConnectReconciler) updateStatusFromServiceAttachment(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, serviceAttachment *compute.ServiceAttachment) (ctrl.Result, error) {
	patch := client.MergeFrom(gcpPSC.DeepCopy())

	// Update status fields
	gcpPSC.Status.ServiceAttachmentName = serviceAttachment.Name
	gcpPSC.Status.ServiceAttachmentURI = r.constructServiceAttachmentURI(serviceAttachment.Name)

	// Update conditions based on Service Attachment state
	now := metav1.Now()

	// Service Attachment is considered ready if it has been successfully created
	// and has the expected configuration. Since GCP ServiceAttachment doesn't
	// have a Status field, we check if it exists and has proper configuration.
	isReady := serviceAttachment.Name != "" &&
		serviceAttachment.TargetService != "" &&
		len(serviceAttachment.NatSubnets) > 0

	if isReady {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPServiceAttachmentAvailable),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.GCPSuccessReason,
			Message:            "GCP Service Attachment is ready and accepting connections",
			LastTransitionTime: now,
		})
	} else {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPServiceAttachmentAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.GCPErrorReason,
			Message:            "GCP Service Attachment is not properly configured",
			LastTransitionTime: now,
		})
	}

	return ctrl.Result{}, r.Status().Patch(ctx, gcpPSC, patch)
}

// delete handles deletion of GCPPrivateServiceConnect resources and returns completion status
func (r *GCPPrivateServiceConnectReconciler) delete(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) (bool, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", gcpPSC.Name)

	// Use Service Attachment name from status (set during creation)
	serviceAttachmentName := gcpPSC.Status.ServiceAttachmentName
	if serviceAttachmentName == "" {
		log.Info("No Service Attachment name in status, nothing to delete")
		return true, nil // Consider deletion completed if no Service Attachment was created
	}

	log.Info("Deleting Service Attachment", "name", serviceAttachmentName)
	deleteCtx, deleteCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer deleteCancel()

	op, err := r.GcpClient.ServiceAttachments.Delete(r.ProjectID, r.Region, serviceAttachmentName).Context(deleteCtx).Do()
	if err != nil {
		if isNotFoundError(err) {
			// Service Attachment already deleted, consider it completed
			log.Info("Service Attachment not found, deletion already completed", "name", serviceAttachmentName)
			return true, nil
		}
		return false, fmt.Errorf("failed to delete Service Attachment: %w", err)
	}

	if op != nil {
		if op.Error != nil && len(op.Error.Errors) > 0 {
			return false, fmt.Errorf("delete Service Attachment operation failed: %s", op.Error.Errors[0].Message)
		}
		if op.Status != "DONE" {
			log.Info("Service Attachment deletion in progress", "operation", op.Name, "status", op.Status)
			return false, nil // Not completed yet
		}
	}

	log.Info("Service Attachment deletion completed", "name", serviceAttachmentName)
	return true, nil // Deletion completed
}

// handleGCPError handles GCP API errors with appropriate retry logic
func (r *GCPPrivateServiceConnectReconciler) handleGCPError(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, reason string, err error) (ctrl.Result, error) {
	log := r.Log.WithValues("gcpprivateserviceconnect", gcpPSC.Name)

	// Extract GCP error details
	var requeueAfter time.Duration
	var message string

	if googleErr, ok := err.(*googleapi.Error); ok {
		switch googleErr.Code {
		case 429: // Rate limit
			requeueAfter = time.Minute * 5
			message = "GCP API rate limit exceeded, retrying"
		case 403: // Permission denied
			requeueAfter = time.Minute * 10
			message = "GCP API permission denied, check service account permissions"
		case 409: // Conflict
			requeueAfter = time.Second * 30
			message = "GCP resource conflict, retrying"
		default:
			requeueAfter = time.Minute * 2
			message = fmt.Sprintf("GCP API error: %s", googleErr.Message)
		}
	} else {
		requeueAfter = time.Minute * 2
		message = fmt.Sprintf("Unexpected error: %s", err.Error())
	}

	log.Error(err, message)

	// Update condition
	patch := client.MergeFrom(gcpPSC.DeepCopy())
	meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.GCPServiceAttachmentAvailable),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		log.Error(err, "failed to update status")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// Helper functions

// isNotFoundError checks if an error is a GCP 404 Not Found error
func isNotFoundError(err error) bool {
	if googleErr, ok := err.(*googleapi.Error); ok {
		return googleErr.Code == 404
	}
	return false
}

// extractGCPProjectFromEnv extracts GCP project ID from GCP_PROJECT environment variable
func (r *GCPPrivateServiceConnectReconciler) extractGCPProjectFromEnv() (string, error) {
	project := os.Getenv("GCP_PROJECT")
	if project == "" {
		return "", fmt.Errorf("GCP_PROJECT environment variable is required")
	}
	return project, nil
}

// extractGCPRegionFromEnv extracts GCP region from GCP_REGION environment variable
func (r *GCPPrivateServiceConnectReconciler) extractGCPRegionFromEnv() (string, error) {
	region := os.Getenv("GCP_REGION")
	if region == "" {
		return "", fmt.Errorf("GCP_REGION environment variable is required")
	}
	return region, nil
}

// isReconciliationPaused checks if reconciliation should be paused and returns the pause duration
func (r *GCPPrivateServiceConnectReconciler) isReconciliationPaused(log logr.Logger, pausedUntil *string) (bool, time.Duration) {
	if pausedUntil == nil || *pausedUntil == "" {
		return false, 0
	}

	pausedUntilTime, err := time.Parse(time.RFC3339, *pausedUntil)
	if err != nil {
		log.Error(err, "invalid pausedUntil time format", "pausedUntil", *pausedUntil)
		return false, 0
	}

	now := time.Now()
	if now.Before(pausedUntilTime) {
		duration := pausedUntilTime.Sub(now)
		return true, duration
	}

	return false, 0
}

// InitGCPComputeService initializes the GCP Compute Service client
func InitGCPComputeService(ctx context.Context) (*compute.Service, error) {
	// Use Application Default Credentials or service account key
	client, err := google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP client: %w", err)
	}

	service, err := compute.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Compute service: %w", err)
	}

	return service, nil
}
