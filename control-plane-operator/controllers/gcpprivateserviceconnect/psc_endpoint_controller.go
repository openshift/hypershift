package gcpprivateserviceconnect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	pscEndpointFinalizer               = "hypershift.openshift.io/gcp-psc-customer"
	pscEndpointDeletionRequeueDuration = 5 * time.Second // Match AWS pattern

	// gcpAPITimeout is the timeout for individual GCP API calls to prevent hung reconcilers.
	// GCP SDK has connection-level timeouts (dial: 30s, TLS: 10s) but no overall request timeout.
	// This matches the timeout used in the management-side PSC controller.
	gcpAPITimeout = 30 * time.Second

	// wifTokenPath is the path where the token minter writes the projected service account token for GCP WIF.
	wifTokenPath = "/var/run/secrets/openshift/serviceaccount/token"

	// driftDetectionRequeueInterval is the interval for periodic reconciliation to detect
	// out-of-band changes to GCP resources. Matches the AWS private link controller pattern.
	driftDetectionRequeueInterval = 5 * time.Minute
)

// isWIFTokenAccessible checks if the WIF service account token file exists and is accessible.
// The token is written by the token minter after the credential secret is created.
// This is used to determine if GCP WIF credentials are ready before adding a finalizer.
//
// NOTE: We cannot use the ValidGCPWorkloadIdentity condition from HostedCluster or HCP because:
//  1. The HCP may already be deleted during the deletion flow, making it unavailable to check
//  2. The ValidGCPCredentials condition indicates the credential secret exists, but the token
//     minter runs asynchronously and may not have written the token file yet
//  3. Checking the token file directly is the most accurate way to verify WIF is ready
//     within the control-plane-operator pod
func isWIFTokenAccessible() bool {
	_, err := os.Stat(wifTokenPath)
	return err == nil
}

// gcpClientBuilder manages GCP client creation with HCP configuration
type gcpClientBuilder struct {
	initialized     bool
	customerProject string
	region          string
}

func (b *gcpClientBuilder) initializeWithHCP(hcp *hyperv1.HostedControlPlane) {
	if !b.initialized {
		b.setFromHCP(hcp)
		b.initialized = true
	}
}

func (b *gcpClientBuilder) setFromHCP(hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.GCP != nil {
		b.customerProject = hcp.Spec.Platform.GCP.Project
		b.region = hcp.Spec.Platform.GCP.Region
	} else {
		b.customerProject = ""
		b.region = ""
	}
}

func (b *gcpClientBuilder) getClient(ctx context.Context) (*compute.Service, error) {
	if !b.initialized {
		return nil, errors.New("client not initialized")
	}

	return InitCustomerGCPClient(ctx)
}

// GCPPrivateServiceConnectReconciler manages PSC endpoints in customer projects
type GCPPrivateServiceConnectReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	gcpClientBuilder gcpClientBuilder
}

// SetupWithManager sets up the controller with the Manager.
func (r *GCPPrivateServiceConnectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.GCPPrivateServiceConnect{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	r.Client = mgr.GetClient()

	return nil
}

// Reconcile implements the main reconciliation logic for PSC endpoints
func (r *GCPPrivateServiceConnectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("gcp-psc-endpoint-controller").WithValues("gcpprivateserviceconnect", req.NamespacedName)

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
		if !controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
			// If we previously removed our finalizer, don't delete again and return early
			return ctrl.Result{}, nil
		}

		// Try to initialize client builder for deletion if not already done.
		// This handles the case where the controller restarts during deletion -
		// the in-memory client builder state is lost, so we need to reinitialize from HCP.
		if !r.gcpClientBuilder.initialized {
			if hcp, err := r.getHostedControlPlane(ctx, gcpPSC); err == nil {
				r.gcpClientBuilder.initializeWithHCP(hcp)
			} else {
				log.V(1).Info("Could not initialize client builder during deletion, HCP may be deleted", "error", err)
			}
		}

		// Attempt cleanup using client builder (following AWS pattern)
		customerGCPClient, err := r.gcpClientBuilder.getClient(ctx)
		if err != nil {
			log.Error(err, "failed to get GCP client, skipping PSC endpoint cleanup")
		} else {
			completed, err := r.reconcileDelete(ctx, gcpPSC, customerGCPClient, log)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
			}
			if !completed {
				return ctrl.Result{RequeueAfter: pscEndpointDeletionRequeueDuration}, nil
			}
		}

		// Always remove finalizer regardless of cleanup success (following AWS pattern)
		if controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
			controllerutil.RemoveFinalizer(gcpPSC, pscEndpointFinalizer)
			if err := r.Update(ctx, gcpPSC); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// 3. Add finalizer only if WIF token is accessible
	// The token is created by the token minter after credential secrets are ready.
	// If we add the finalizer before the token exists, cleanup on deletion will fail.
	if !controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
		if !isWIFTokenAccessible() {
			log.Info("WIF token not yet accessible, waiting before adding finalizer")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		controllerutil.AddFinalizer(gcpPSC, pscEndpointFinalizer)
		return ctrl.Result{}, r.Update(ctx, gcpPSC)
	}

	// 4. Find the hosted control plane (for normal reconciliation)
	hcp, err := r.getHostedControlPlane(ctx, gcpPSC)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// 5. Check if reconciliation is paused (following AWS pattern)
	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// 6. Initialize client builder with HCP configuration
	r.gcpClientBuilder.initializeWithHCP(hcp)

	// 7. Wait for Service Attachment to be ready
	if !r.isServiceAttachmentReady(gcpPSC) {
		log.Info("Waiting for Service Attachment to be ready")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// 8. Get customer GCP client using client builder
	customerGCPClient, err := r.gcpClientBuilder.getClient(ctx)
	if err != nil {
		log.Error(err, "failed to create customer GCP client")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Extract project and region from client builder
	customerProject := r.gcpClientBuilder.customerProject
	region := r.gcpClientBuilder.region

	// 9. Ensure IP address is reserved
	if result, err := r.ensureIPAddress(ctx, gcpPSC, hcp, customerGCPClient, customerProject, region, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 10. Reconcile PSC Endpoint
	return r.reconcilePSCEndpoint(ctx, gcpPSC, hcp, customerGCPClient, customerProject, region, log)
}

// isServiceAttachmentReady checks if the management-side Service Attachment is ready
func (r *GCPPrivateServiceConnectReconciler) isServiceAttachmentReady(gcpPSC *hyperv1.GCPPrivateServiceConnect) bool {
	// Check if management-side has populated both ServiceAttachmentURI and ServiceAttachmentName.
	// Both are required: URI for the PSC endpoint target, Name for constructing GCP resource names.
	if gcpPSC.Status.ServiceAttachmentURI == "" || gcpPSC.Status.ServiceAttachmentName == "" {
		return false
	}

	// Check if GCPServiceAttachmentAvailable condition is True
	for _, condition := range gcpPSC.Status.Conditions {
		if condition.Type == string(hyperv1.GCPServiceAttachmentAvailable) {
			return condition.Status == metav1.ConditionTrue
		}
	}

	return false
}

// ensureIPAddress reserves a static IP address for the PSC endpoint
func (r *GCPPrivateServiceConnectReconciler) ensureIPAddress(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, customerGCPClient *compute.Service, customerProject, region string, log logr.Logger) (ctrl.Result, error) {
	// Check if IP already allocated and recorded in status
	if gcpPSC.Status.EndpointIP != "" {
		// Verify IP still exists in GCP
		if exists, err := r.verifyIPExists(ctx, gcpPSC, customerGCPClient, customerProject, region); err != nil {
			return ctrl.Result{}, err
		} else if exists {
			return ctrl.Result{}, nil // IP ready
		}
		// IP was deleted, need to allocate new one
		log.Info("Previously allocated IP no longer exists, allocating new one")
	}

	pscSubnet := hcp.Spec.Platform.GCP.NetworkConfig.PrivateServiceConnectSubnet.Name
	if pscSubnet == "" {
		return ctrl.Result{}, fmt.Errorf("PrivateServiceConnectSubnet not specified in HostedControlPlane")
	}

	// Reserve static internal IP
	ipName := r.constructIPAddressName(gcpPSC)

	// First check if IP address already exists in GCP (make operation idempotent)
	getCtx, getCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer getCancel()
	existingAddress, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(getCtx).Do()
	if err != nil && !isNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to check existing IP address: %w", err)
	}

	if existingAddress != nil {
		// IP already exists, update status and continue
		log.Info("IP address already exists, updating status", "name", ipName, "ip", existingAddress.Address)
		patch := client.MergeFrom(gcpPSC.DeepCopy())
		gcpPSC.Status.EndpointIP = existingAddress.Address
		if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update EndpointIP with existing address: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// IP doesn't exist, create it
	ipAddress := &compute.Address{
		Name:        ipName,
		Description: fmt.Sprintf("PSC endpoint IP for HyperShift cluster %s", gcpPSC.Name),
		AddressType: "INTERNAL",
		Subnetwork:  r.constructSubnetURL(pscSubnet, customerProject, region),
		// Purpose not set for subnetwork addresses - PSC purpose is implicit when used with ForwardingRule
	}

	log.Info("Reserving new IP address for PSC endpoint", "name", ipName, "subnet", pscSubnet)
	insertCtx, insertCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer insertCancel()
	op, err := customerGCPClient.Addresses.Insert(customerProject, region, ipAddress).Context(insertCtx).Do()
	if err != nil {
		return r.handleGCPError(ctx, gcpPSC, "IPReservationFailed", err)
	}

	// GCP operations can be PENDING, RUNNING, or DONE
	if op.Status != "DONE" {
		log.Info("IP reservation in progress", "operation", op.Name, "status", op.Status)
		return ctrl.Result{RequeueAfter: time.Second * 15}, nil
	}
	// DONE doesn't guarantee success - check for operation errors
	if op.Error != nil {
		return ctrl.Result{}, fmt.Errorf("IP reservation failed: %v", op.Error.Errors)
	}

	// Get the allocated IP address
	fetchCtx, fetchCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer fetchCancel()
	allocatedAddress, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(fetchCtx).Do()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get allocated IP: %w", err)
	}

	// Update status with allocated IP
	patch := client.MergeFrom(gcpPSC.DeepCopy())
	gcpPSC.Status.EndpointIP = allocatedAddress.Address
	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update EndpointIP: %w", err)
	}

	log.Info("Successfully reserved IP address", "ip", allocatedAddress.Address)
	return ctrl.Result{}, nil
}

// verifyIPExists checks if the IP address still exists in GCP
func (r *GCPPrivateServiceConnectReconciler) verifyIPExists(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, customerGCPClient *compute.Service, customerProject, region string) (bool, error) {
	ipName := r.constructIPAddressName(gcpPSC)
	apiCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer cancel()
	_, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(apiCtx).Do()
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// reconcilePSCEndpoint creates or updates the PSC endpoint
func (r *GCPPrivateServiceConnectReconciler) reconcilePSCEndpoint(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, customerGCPClient *compute.Service, customerProject, region string, log logr.Logger) (ctrl.Result, error) {
	endpointName := r.constructEndpointName(gcpPSC)

	// Check if endpoint already exists
	getCtx, getCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer getCancel()
	existingEndpoint, err := customerGCPClient.ForwardingRules.Get(customerProject, region, endpointName).Context(getCtx).Do()
	if err != nil && !isNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to check existing PSC endpoint: %w", err)
	}

	if existingEndpoint != nil {
		// Update status from existing endpoint
		return r.updateStatusFromEndpoint(ctx, gcpPSC, existingEndpoint)
	}

	// Create new PSC endpoint
	ipName := r.constructIPAddressName(gcpPSC)
	endpoint := &compute.ForwardingRule{
		Name:        endpointName,
		Description: fmt.Sprintf("PSC endpoint for HyperShift cluster %s", gcpPSC.Name),
		Network:     r.constructNetworkURL(hcp.Spec.Platform.GCP.NetworkConfig.Network.Name, customerProject),
		Subnetwork:  r.constructSubnetURL(hcp.Spec.Platform.GCP.NetworkConfig.PrivateServiceConnectSubnet.Name, customerProject, region),
		Target:      gcpPSC.Status.ServiceAttachmentURI,                     // From management-side
		IPAddress:   r.constructAddressURL(ipName, customerProject, region), // Reserved IP resource URL
		// LoadBalancingScheme not set for PSC endpoints - it's implicit and setting it causes API errors
	}

	log.Info("Creating PSC endpoint", "name", endpointName, "serviceAttachment", gcpPSC.Status.ServiceAttachmentURI)
	insertCtx, insertCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer insertCancel()
	op, err := customerGCPClient.ForwardingRules.Insert(customerProject, region, endpoint).Context(insertCtx).Do()
	if err != nil {
		return r.handleGCPError(ctx, gcpPSC, "PSCEndpointCreationFailed", err)
	}

	// GCP operations can be PENDING, RUNNING, or DONE
	if op.Status != "DONE" {
		log.Info("PSC endpoint creation in progress", "operation", op.Name, "status", op.Status)
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}
	// DONE doesn't guarantee success - check for operation errors
	if op.Error != nil {
		return r.handleGCPError(ctx, gcpPSC, "PSCEndpointCreationFailed", fmt.Errorf("operation failed: %v", op.Error.Errors))
	}

	// Fetch the newly created endpoint to update status
	log.Info("PSC endpoint created, fetching to update status", "name", endpointName)
	fetchCtx, fetchCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer fetchCancel()
	createdEndpoint, err := customerGCPClient.ForwardingRules.Get(customerProject, region, endpointName).Context(fetchCtx).Do()
	if err != nil {
		// Endpoint was created but we couldn't fetch it - requeue to retry
		log.Error(err, "failed to fetch newly created endpoint, will retry")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	return r.updateStatusFromEndpoint(ctx, gcpPSC, createdEndpoint)
}

// updateStatusFromEndpoint updates the CR status based on the PSC endpoint state
func (r *GCPPrivateServiceConnectReconciler) updateStatusFromEndpoint(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, endpoint *compute.ForwardingRule) (ctrl.Result, error) {
	patch := client.MergeFrom(gcpPSC.DeepCopy())

	// Update condition based on endpoint status
	now := metav1.Now()

	// Check if endpoint is ready (has IP and target)
	if endpoint.IPAddress != "" && endpoint.Target != "" {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPEndpointAvailable),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.GCPSuccessReason,
			Message:            "PSC endpoint is ready and accepting connections",
			LastTransitionTime: now,
		})
	} else {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPEndpointAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.GCPErrorReason,
			Message:            fmt.Sprintf("PSC endpoint not ready: IP=%s, Target=%s", endpoint.IPAddress, endpoint.Target),
			LastTransitionTime: now,
		})
	}

	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		return ctrl.Result{}, err
	}

	// Always requeue to catch and report out-of-band changes in GCP (drift detection)
	return ctrl.Result{RequeueAfter: driftDetectionRequeueInterval}, nil
}

// reconcileDelete handles cleanup when the CR is being deleted.
// For each resource: delete, check operation status/error, then verify resource is gone.
func (r *GCPPrivateServiceConnectReconciler) reconcileDelete(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, customerGCPClient *compute.Service, log logr.Logger) (bool, error) {
	customerProject := r.gcpClientBuilder.customerProject
	region := r.gcpClientBuilder.region

	// Step 1: Delete PSC endpoint (ForwardingRule)
	endpointName := r.constructEndpointName(gcpPSC)

	deleteCtx, deleteCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer deleteCancel()
	endpointOp, err := customerGCPClient.ForwardingRules.Delete(customerProject, region, endpointName).Context(deleteCtx).Do()
	if err != nil {
		if !isNotFoundError(err) {
			return false, fmt.Errorf("failed to delete PSC endpoint: %w", err)
		}
		// Already deleted, continue to IP deletion
		log.Info("PSC endpoint already deleted", "name", endpointName)
	} else {
		// Check operation status - DONE doesn't guarantee success, must check Error
		if endpointOp.Status != "DONE" {
			log.Info("PSC endpoint deletion in progress", "name", endpointName, "operation", endpointOp.Name, "status", endpointOp.Status)
			return false, nil
		}
		if endpointOp.Error != nil {
			return false, fmt.Errorf("PSC endpoint deletion failed: %v", endpointOp.Error.Errors)
		}

		// Verify endpoint is actually gone
		getCtx, getCancel := context.WithTimeout(ctx, gcpAPITimeout)
		defer getCancel()
		_, err = customerGCPClient.ForwardingRules.Get(customerProject, region, endpointName).Context(getCtx).Do()
		if err == nil {
			log.Info("PSC endpoint still exists after DONE, waiting", "name", endpointName)
			return false, nil
		}
		if !isNotFoundError(err) {
			return false, fmt.Errorf("failed to verify PSC endpoint deletion: %w", err)
		}
		log.Info("PSC endpoint deleted", "name", endpointName)
	}

	// Step 2: Delete reserved IP address
	ipName := r.constructIPAddressName(gcpPSC)

	ipDeleteCtx, ipDeleteCancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer ipDeleteCancel()
	ipOp, err := customerGCPClient.Addresses.Delete(customerProject, region, ipName).Context(ipDeleteCtx).Do()
	if err != nil {
		if !isNotFoundError(err) {
			return false, fmt.Errorf("failed to delete reserved IP: %w", err)
		}
		// Already deleted
		log.Info("Reserved IP address already deleted", "name", ipName)
	} else {
		// Check operation status - DONE doesn't guarantee success, must check Error
		if ipOp.Status != "DONE" {
			log.Info("IP address deletion in progress", "name", ipName, "operation", ipOp.Name, "status", ipOp.Status)
			return false, nil
		}
		if ipOp.Error != nil {
			return false, fmt.Errorf("IP address deletion failed: %v", ipOp.Error.Errors)
		}

		// Verify IP is actually gone
		ipGetCtx, ipGetCancel := context.WithTimeout(ctx, gcpAPITimeout)
		defer ipGetCancel()
		_, err = customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(ipGetCtx).Do()
		if err == nil {
			log.Info("Reserved IP address still exists after DONE, waiting", "name", ipName)
			return false, nil
		}
		if !isNotFoundError(err) {
			return false, fmt.Errorf("failed to verify IP address deletion: %w", err)
		}
		log.Info("Reserved IP address deleted", "name", ipName)
	}

	return true, nil // Both resources confirmed deleted
}

// Helper functions for resource naming and URL construction

func (r *GCPPrivateServiceConnectReconciler) constructEndpointName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Use service attachment name as base - it's unique and within GCP naming limits
	return fmt.Sprintf("%s-endpoint", gcpPSC.Status.ServiceAttachmentName)
}

func (r *GCPPrivateServiceConnectReconciler) constructIPAddressName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Use service attachment name as base - it's unique and within GCP naming limits
	return fmt.Sprintf("%s-ip", gcpPSC.Status.ServiceAttachmentName)
}

func (r *GCPPrivateServiceConnectReconciler) constructNetworkURL(networkName, customerProject string) string {
	return fmt.Sprintf("projects/%s/global/networks/%s", customerProject, networkName)
}

func (r *GCPPrivateServiceConnectReconciler) constructSubnetURL(subnetName, customerProject, region string) string {
	return fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", customerProject, region, subnetName)
}

func (r *GCPPrivateServiceConnectReconciler) constructAddressURL(addressName, customerProject, region string) string {
	return fmt.Sprintf("projects/%s/regions/%s/addresses/%s", customerProject, region, addressName)
}

// getHostedControlPlane retrieves the HostedControlPlane from the CR's owner reference
func (r *GCPPrivateServiceConnectReconciler) getHostedControlPlane(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) (*hyperv1.HostedControlPlane, error) {
	// Find HCP from owner reference
	for _, ownerRef := range gcpPSC.OwnerReferences {
		if ownerRef.Kind == "HostedControlPlane" && ownerRef.APIVersion == hyperv1.GroupVersion.String() {
			hcp := &hyperv1.HostedControlPlane{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: gcpPSC.Namespace, Name: ownerRef.Name}, hcp); err != nil {
				return nil, fmt.Errorf("failed to get HostedControlPlane %s: %w", ownerRef.Name, err)
			}
			return hcp, nil
		}
	}

	return nil, fmt.Errorf("no HostedControlPlane owner reference found")
}

// handleGCPError handles GCP API errors with appropriate retry logic
func (r *GCPPrivateServiceConnectReconciler) handleGCPError(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, reason string, err error) (ctrl.Result, error) {
	log, logErr := logr.FromContext(ctx)
	if logErr != nil {
		return ctrl.Result{}, fmt.Errorf("logger not found: %w", logErr)
	}

	var requeueAfter time.Duration
	var message string

	if googleErr, ok := err.(*googleapi.Error); ok {
		switch googleErr.Code {
		case 429: // Rate limit
			requeueAfter = time.Minute * 5
			message = "GCP API rate limit exceeded, retrying"
		case 403: // Permission denied
			requeueAfter = time.Minute * 10
			message = "GCP API permission denied, check customer project permissions"
		case 409: // Conflict (IP already allocated, etc.)
			requeueAfter = time.Second * 30
			message = "GCP resource conflict, retrying"
		case 400: // Bad request (subnet full, invalid config, etc.)
			requeueAfter = time.Minute * 5
			message = fmt.Sprintf("GCP configuration error: %s", googleErr.Message)
		default:
			requeueAfter = time.Minute * 2
			message = fmt.Sprintf("GCP API error: %s", googleErr.Message)
		}
	} else {
		requeueAfter = time.Minute * 2
		message = fmt.Sprintf("Unexpected error: %s", err.Error())
	}

	log.Error(err, message)

	// Update condition with error
	patch := client.MergeFrom(gcpPSC.DeepCopy())
	meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.GCPEndpointAvailable),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		log.Error(err, "failed to update status condition")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// isNotFoundError checks if the error is a GCP "not found" error
func isNotFoundError(err error) bool {
	if googleErr, ok := err.(*googleapi.Error); ok {
		return googleErr.Code == 404
	}
	return false
}

// InitCustomerGCPClient initializes the GCP client for customer project operations.
// This controller requires Workload Identity Federation (WIF) credentials, which are
// set up by the token minter in the control-plane-operator. Service account keys are
// not supported - the finalizer gate checks for WIF token availability before proceeding.
func InitCustomerGCPClient(ctx context.Context) (*compute.Service, error) {
	credentialsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credentialsFile == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS not set")
	}

	// Verify credentials file exists and is readable
	if _, err := os.Stat(credentialsFile); err != nil {
		return nil, fmt.Errorf("credentials file not accessible at %s: %w", credentialsFile, err)
	}

	// Create Google Cloud client using the WIF credentials file from environment
	// google.DefaultClient() automatically reads GOOGLE_APPLICATION_CREDENTIALS
	client, err := google.DefaultClient(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Cloud client using %s: %w", credentialsFile, err)
	}

	// Create the Compute Engine service
	computeService, err := compute.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Compute Engine service: %w", err)
	}

	return computeService, nil
}
