// Package azure implements the management-plane side of Azure Private Link Service (PLS)
// lifecycle for self-managed HyperShift hosted clusters.
//
// Architecture: Azure private topology uses a split HO/CPO controller pattern
// (similar to AWS Private Link and GCP Private Service Connect):
//
//	KAS Service (internal LB) → CPO Observer detects ILB IP →
//	  creates AzurePrivateLinkService CR → HO controller (this package) creates PLS in Azure →
//	  CPO controller creates Private Endpoint + DNS in guest VNet
//
// The HO controller runs in the management cluster with broad Azure credentials
// (Network Contributor on the management resource group). It watches AzurePrivateLinkService
// CRs across all namespaces and creates/deletes the Azure PLS resource that fronts each
// hosted cluster's internal load balancer.
//
// Azure SDK client interfaces (PrivateLinkServicesAPI, LoadBalancersAPI) are used instead
// of concrete types to enable unit testing with mocks.
//
// Azure Credentials:
//
// The HO controller runs in the management cluster and uses the management cluster's
// Azure workload identity (typically Network Contributor on the management resource group).
// Required Azure RBAC permissions:
//   - Microsoft.Network/privateLinkServices/read, write, delete (PLS lifecycle)
//   - Microsoft.Network/loadBalancers/read (ILB frontend IP lookup)
package azure

import (
	"context"
	"fmt"
	"net"
	"slices"
	"sort"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	supportutil "github.com/openshift/hypershift/support/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// azurePLSFinalizer is the HO-side finalizer for AzurePrivateLinkService resources.
	// It is responsible for deleting the management-side Azure Private Link Service (PLS)
	// that fronts the hosted cluster's internal load balancer.
	//
	// Deletion ordering contract with the CPO controller:
	// The CPO controller manages a separate finalizer ("hypershift.openshift.io/azure-private-link-service")
	// that deletes the guest-side Private Endpoint (PE), Private DNS Zone, VNet link, and
	// A records. Azure enforces that a PLS cannot be deleted while active PE connections
	// exist. Therefore, the CPO finalizer MUST complete (deleting the PE) before this HO
	// finalizer can successfully delete the PLS.
	//
	// As a defensive measure, the HO delete() function proactively rejects any active PE
	// connections (Approved or Pending) before attempting PLS deletion. This mirrors the
	// AWS pattern (rejectVpcEndpointConnections) and ensures cleanup proceeds even if the
	// CPO controller is slow to remove its PE. After rejection, the controller requeues to
	// allow Azure time to process the disconnections before retrying PLS deletion.
	azurePLSFinalizer = "hypershift.openshift.io/azure-pls-cleanup"

	// azureAPITimeout is the timeout for individual Azure API calls to prevent hung reconcilers.
	// HO uses 2m (120s) because it manages the complex Private Link Service resource which requires:
	//   - Load balancer frontend IP configuration lookup and mapping
	//   - Private Link Service creation with network configuration
	//   - Visibility and auto-approval settings for allowed subscriptions
	//   - NAT IP allocation from dedicated subnet
	// These operations are more involved than CPO's smaller resources and can take longer,
	// justifying the longer timeout. For comparison, CPO uses 30s (see control-plane-operator/)
	// for simpler resources like Private Endpoints, DNS zones, and A records.
	azureAPITimeout = 2 * time.Minute
)

// PrivateLinkServicesAPI abstracts the Azure Private Link Services client
type PrivateLinkServicesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, serviceName string, parameters armnetwork.PrivateLinkService, options *armnetwork.PrivateLinkServicesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateLinkServicesClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, serviceName string, options *armnetwork.PrivateLinkServicesClientBeginDeleteOptions) (*azruntime.Poller[armnetwork.PrivateLinkServicesClientDeleteResponse], error)
	Get(ctx context.Context, resourceGroupName string, serviceName string, options *armnetwork.PrivateLinkServicesClientGetOptions) (armnetwork.PrivateLinkServicesClientGetResponse, error)
	UpdatePrivateEndpointConnection(ctx context.Context, resourceGroupName string, serviceName string, peConnectionName string, parameters armnetwork.PrivateEndpointConnection, options *armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionOptions) (armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionResponse, error)
}

// LoadBalancersAPI abstracts the Azure Load Balancers client
type LoadBalancersAPI interface {
	NewListPager(resourceGroupName string, options *armnetwork.LoadBalancersClientListOptions) *azruntime.Pager[armnetwork.LoadBalancersClientListResponse]
}

// SubnetsAPI abstracts the Azure Subnets client for auto-creating NAT subnets
type SubnetsAPI interface {
	Get(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, options *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error)
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, virtualNetworkName string, subnetName string, subnetParameters armnetwork.Subnet, options *armnetwork.SubnetsClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.SubnetsClientCreateOrUpdateResponse], error)
	NewListPager(resourceGroupName string, virtualNetworkName string, options *armnetwork.SubnetsClientListOptions) *azruntime.Pager[armnetwork.SubnetsClientListResponse]
}

// AzurePrivateLinkServiceController reconciles AzurePrivateLinkService resources.
// It watches AzurePrivateLinkService CRDs across all namespaces and manages
// the lifecycle of Azure Private Link Service resources.
type AzurePrivateLinkServiceController struct {
	client.Client
	PrivateLinkServices     PrivateLinkServicesAPI
	LoadBalancers           LoadBalancersAPI
	Subnets                 SubnetsAPI
	ManagementResourceGroup string
}

// SetupWithManager sets up the controller with the Manager.
func (r *AzurePrivateLinkServiceController) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AzurePrivateLinkService{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 1,
		}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	return nil
}

// Reconcile manages the Azure Private Link Service for a hosted cluster.
// Steps: fetch CR → handle deletion → add finalizer → find HostedCluster →
// check paused → wait for LoadBalancerIP → look up ILB → create PLS.
func (r *AzurePrivateLinkServiceController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("azureprivatelinkservice", req.NamespacedName)

	// 1. Fetch AzurePrivateLinkService CR
	obj := &hyperv1.AzurePrivateLinkService{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get AzurePrivateLinkService: %w", err)
	}

	// Don't change the cached object
	azPLS := obj.DeepCopy()

	// 2. Handle deletion
	if !azPLS.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(azPLS, azurePLSFinalizer) {
			return ctrl.Result{}, nil
		}
		completed, err := r.delete(ctx, azPLS)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
		}
		if !completed {
			return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
		}
		controllerutil.RemoveFinalizer(azPLS, azurePLSFinalizer)
		if err := r.Update(ctx, azPLS); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(azPLS, azurePLSFinalizer) {
		controllerutil.AddFinalizer(azPLS, azurePLSFinalizer)
		if err := r.Update(ctx, azPLS); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		// Return immediately after finalizer addition to avoid using a stale
		// ResourceVersion for subsequent Status().Patch() calls. The next
		// reconcile will re-fetch the object with the updated ResourceVersion.
		return ctrl.Result{}, nil
	}

	// 4. Find the hosted cluster using annotation (set by CPO-side observer)
	hc, err := supportutil.HostedClusterFromAnnotation(ctx, r.Client, azPLS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted cluster: %w", err)
	}

	// 5. Check if reconciliation is paused
	if isPaused, duration := supportutil.IsReconciliationPaused(log, hc.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hc.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// 6. If loadBalancerIP is not yet set, wait for CPO to populate it
	if azPLS.Spec.LoadBalancerIP == "" {
		log.Info("LoadBalancerIP not yet set, waiting for CPO observer")
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	// 7. Reconcile PLS
	return r.reconcilePrivateLinkService(ctx, azPLS, hc)
}

// reconcilePrivateLinkService manages Private Link Service lifecycle.
// It looks up the Azure ILB by matching the frontend IP to spec.loadBalancerIP,
// then creates a PLS attached to that ILB's frontend IP configuration.
// The PLS NAT IP is allocated from the dedicated NAT subnet specified in the CR.
func (r *AzurePrivateLinkServiceController) reconcilePrivateLinkService(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, hc *hyperv1.HostedCluster) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("azureprivatelinkservice", azPLS.Name, "namespace", azPLS.Namespace)
	plsName := constructPLSName(hc.Spec.ClusterID, azPLS.Name)
	if err := azureutil.ValidateAzureResourceName(plsName, "Private Link Service"); err != nil {
		return ctrl.Result{}, err
	}
	resourceGroup := r.ManagementResourceGroup

	// 1. Check if PLS already exists
	getCtx, getCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer getCancel()

	existingPLS, err := r.PrivateLinkServices.Get(getCtx, resourceGroup, plsName, nil)
	if err != nil && !azureutil.IsAzureNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get Private Link Service: %w", err)
	}

	if existingPLS.ID != nil {
		// PLS already exists. Check if visibility/auto-approval needs updating.
		desiredSubs, err := buildAllowedSubscriptions(azPLS)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to build allowed subscriptions: %w", err)
		}
		if r.plsSubscriptionsDrifted(existingPLS.PrivateLinkService, desiredSubs) {
			log.Info("AllowedSubscriptions changed, updating PLS visibility/auto-approval", "name", plsName)
			return r.updatePLSSubscriptions(ctx, azPLS, plsName, resourceGroup, existingPLS.PrivateLinkService)
		}
		return r.updateStatusFromPLS(ctx, azPLS, &existingPLS.PrivateLinkService)
	}

	// 2. If status already has a PLS ID, it might have been deleted externally.
	//    Clear stale status fields so CPO observer won't create a PE pointing to a non-existent PLS.
	if azPLS.Status.PrivateLinkServiceID != "" {
		log.Info("PLS not found in Azure but status has PLS ID, clearing stale status and recreating",
			"previousID", azPLS.Status.PrivateLinkServiceID,
			"previousAlias", azPLS.Status.PrivateLinkServiceAlias)
		patch := client.MergeFrom(azPLS.DeepCopy())
		azPLS.Status.PrivateLinkServiceID = ""
		azPLS.Status.PrivateLinkServiceAlias = ""
		meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.AzurePLSCreated),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.AzurePLSErrorReason,
			Message:            "Private Link Service was deleted externally, recreating",
			ObservedGeneration: azPLS.Generation,
		})
		if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clear stale PLS status: %w", err)
		}
	}

	// 3. Look up the ILB by frontend IP
	ilbID, frontendIPConfigID, ilbSubnetID, err := r.lookupILBByFrontendIP(ctx, azPLS)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, "ILBLookupFailed", err)
	}
	if ilbID == "" {
		log.Info("Internal Load Balancer not yet found for LoadBalancer IP, will retry", "loadBalancerIP", azPLS.Spec.LoadBalancerIP)
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	// 4. Update status with ILB resource ID
	patch := client.MergeFrom(azPLS.DeepCopy())
	azPLS.Status.InternalLoadBalancerID = ilbID
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.AzureInternalLoadBalancerAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.AzurePLSSuccessReason,
		Message:            "Internal Load Balancer found",
		ObservedGeneration: azPLS.Generation,
	})
	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ILB status: %w", err)
	}

	// 5. Build allowed subscriptions for visibility/auto-approval
	desiredSubs, err := buildAllowedSubscriptions(azPLS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build allowed subscriptions: %w", err)
	}
	allowedSubs := make([]*string, len(desiredSubs))
	for i, sub := range desiredSubs {
		allowedSubs[i] = ptr.To(sub)
	}

	// 6. Resolve NAT subnet ID — use the one from the spec, or auto-create in the ILB's VNet.
	// The NAT subnet must be in the same VNet as the ILB (Azure requirement).
	// The ILB is created by the management cluster's cloud controller in the management VNet,
	// so the NAT subnet must also be in the management VNet (not the HC's VNet).
	natSubnetID := string(azPLS.Spec.NATSubnetID)
	if natSubnetID == "" {
		if ilbSubnetID == "" {
			return r.handleAzureError(ctx, azPLS, "NATSubnetCreationFailed",
				fmt.Errorf("ILB frontend IP configuration does not have a subnet reference, cannot auto-create NAT subnet"))
		}
		var resolveErr error
		natSubnetID, resolveErr = r.ensureNATSubnet(ctx, hc, ilbSubnetID)
		if resolveErr != nil {
			return r.handleAzureError(ctx, azPLS, "NATSubnetCreationFailed", resolveErr)
		}
		log.Info("Auto-created NAT subnet for Private Link Service", "natSubnetID", natSubnetID)
	}

	// 7. Create PLS
	plsParams := armnetwork.PrivateLinkService{
		Location: ptr.To(azPLS.Spec.Location),
		Tags: map[string]*string{
			"kubernetes.io_cluster_" + hc.Name:      ptr.To("owned"),
			"hypershift.openshift.io_resource-type": ptr.To("private-link-service"),
		},
		Properties: &armnetwork.PrivateLinkServiceProperties{
			LoadBalancerFrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
				{
					ID: ptr.To(frontendIPConfigID),
				},
			},
			IPConfigurations: []*armnetwork.PrivateLinkServiceIPConfiguration{
				{
					Name: ptr.To("pls-nat-ip"),
					Properties: &armnetwork.PrivateLinkServiceIPConfigurationProperties{
						PrivateIPAllocationMethod: ptr.To(armnetwork.IPAllocationMethodDynamic),
						Subnet: &armnetwork.Subnet{
							ID: ptr.To(natSubnetID),
						},
						Primary: ptr.To(true),
					},
				},
			},
			Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
				Subscriptions: allowedSubs,
			},
			AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
				Subscriptions: allowedSubs,
			},
			EnableProxyProtocol: ptr.To(false),
		},
	}

	log.Info("Creating Private Link Service", "name", plsName, "resourceGroup", resourceGroup)
	createCtx, createCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer createCancel()

	poller, err := r.PrivateLinkServices.BeginCreateOrUpdate(createCtx, resourceGroup, plsName, plsParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, "PLSCreationFailed", err)
	}

	if poller == nil {
		log.Info("Private Link Service creation accepted, will check status on next reconcile", "name", plsName)
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	pollCtx, pollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
	defer pollCancel()

	resp, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, "PLSCreationFailed", err)
	}

	log.Info("Private Link Service created successfully", "name", plsName)
	return r.updateStatusFromPLS(ctx, azPLS, &resp.PrivateLinkService)
}

// lookupILBByFrontendIP finds the ILB whose frontend IP matches spec.loadBalancerIP.
// Returns the ILB resource ID, the matching frontend IP configuration ID, and the
// subnet ID from the frontend IP configuration. The subnet ID identifies the VNet
// where the NAT subnet must be created for Private Link Service.
func (r *AzurePrivateLinkServiceController) lookupILBByFrontendIP(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService) (string, string, string, error) {
	pager := r.LoadBalancers.NewListPager(r.ManagementResourceGroup, nil)

	for pager.More() {
		pageCtx, pageCancel := context.WithTimeout(ctx, azureAPITimeout)
		page, err := pager.NextPage(pageCtx)
		pageCancel()
		if err != nil {
			return "", "", "", fmt.Errorf("failed to list load balancers: %w", err)
		}

		for _, lb := range page.Value {
			if lb.Properties == nil || lb.ID == nil {
				continue
			}
			for _, fip := range lb.Properties.FrontendIPConfigurations {
				if fip.Properties == nil || fip.Properties.PrivateIPAddress == nil || fip.ID == nil {
					continue
				}
				if *fip.Properties.PrivateIPAddress == azPLS.Spec.LoadBalancerIP {
					var subnetID string
					if fip.Properties.Subnet != nil && fip.Properties.Subnet.ID != nil {
						subnetID = *fip.Properties.Subnet.ID
					}
					return *lb.ID, *fip.ID, subnetID, nil
				}
			}
		}
	}

	return "", "", "", nil
}

// updateStatusFromPLS updates the CR status based on PLS state and sets the
// HostedCluster annotation back to the hosted cluster (same pattern as GCP).
func (r *AzurePrivateLinkServiceController) updateStatusFromPLS(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, pls *armnetwork.PrivateLinkService) (ctrl.Result, error) {
	patch := client.MergeFrom(azPLS.DeepCopy())

	if pls.ID != nil {
		azPLS.Status.PrivateLinkServiceID = *pls.ID
	}
	if pls.Properties != nil && pls.Properties.Alias != nil {
		azPLS.Status.PrivateLinkServiceAlias = *pls.Properties.Alias
	}

	isReady := pls.ID != nil &&
		pls.Properties != nil &&
		pls.Properties.Alias != nil &&
		len(pls.Properties.LoadBalancerFrontendIPConfigurations) > 0

	if isReady {
		for _, t := range []hyperv1.ConditionType{hyperv1.AzurePLSCreated, hyperv1.AzurePrivateLinkServiceAvailable} {
			meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
				Type:               string(t),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AzurePLSSuccessReason,
				Message:            "Azure Private Link Service is ready",
				ObservedGeneration: azPLS.Generation,
			})
		}
	} else {
		for _, t := range []hyperv1.ConditionType{hyperv1.AzurePLSCreated, hyperv1.AzurePrivateLinkServiceAvailable} {
			meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
				Type:               string(t),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AzurePLSErrorReason,
				Message:            "Azure Private Link Service is not properly configured",
				ObservedGeneration: azPLS.Generation,
			})
		}
	}

	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: azureutil.DriftDetectionRequeueInterval}, nil
}

// buildAllowedSubscriptions constructs the complete list of Azure subscription IDs
// permitted to create Private Endpoints. It always includes the guest cluster's own
// subscription (derived from guestSubnetID) and appends any additional allowed subscriptions.
func buildAllowedSubscriptions(azPLS *hyperv1.AzurePrivateLinkService) ([]string, error) {
	guestSubnet, err := arm.ParseResourceID(string(azPLS.Spec.GuestSubnetID))
	if err != nil {
		return nil, fmt.Errorf("failed to parse guestSubnetID to extract subscription: %w", err)
	}
	guestSubscriptionID := guestSubnet.SubscriptionID

	// Start with the guest subscription, then add any additional ones (deduped).
	subs := []string{guestSubscriptionID}
	for _, sub := range azPLS.Spec.AdditionalAllowedSubscriptions {
		if string(sub) != guestSubscriptionID {
			subs = append(subs, string(sub))
		}
	}
	return subs, nil
}

// plsSubscriptionsDrifted checks whether the existing PLS visibility/auto-approval
// subscriptions differ from the desired spec. Returns true if an update is needed.
func (r *AzurePrivateLinkServiceController) plsSubscriptionsDrifted(pls armnetwork.PrivateLinkService, desired []string) bool {
	if pls.Properties == nil {
		return len(desired) > 0
	}

	var visSubs []*string
	if pls.Properties.Visibility != nil {
		visSubs = pls.Properties.Visibility.Subscriptions
	}
	existingVisibility := derefPtrSlice(visSubs)
	sort.Strings(existingVisibility)

	var autoSubs []*string
	if pls.Properties.AutoApproval != nil {
		autoSubs = pls.Properties.AutoApproval.Subscriptions
	}
	existingAutoApproval := derefPtrSlice(autoSubs)
	sort.Strings(existingAutoApproval)

	sorted := slices.Clone(desired)
	sort.Strings(sorted)

	// Normalize empty slices to nil for consistent comparison.
	// derefPtrSlice returns nil for empty/nil input, and slices.Equal
	// treats nil and []string{} as different, which would cause unnecessary updates.
	if len(sorted) == 0 {
		sorted = nil
	}

	return !slices.Equal(existingVisibility, sorted) || !slices.Equal(existingAutoApproval, sorted)
}

// derefPtrSlice extracts non-nil strings from a slice of string pointers.
func derefPtrSlice(ptrs []*string) []string {
	if len(ptrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(ptrs))
	for _, s := range ptrs {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// updatePLSSubscriptions patches the Azure PLS visibility and auto-approval settings
// to match the CR spec when allowed subscriptions have changed.
func (r *AzurePrivateLinkServiceController) updatePLSSubscriptions(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, plsName, resourceGroup string, existing armnetwork.PrivateLinkService) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("azureprivatelinkservice", azPLS.Name, "namespace", azPLS.Namespace)

	desiredSubs, err := buildAllowedSubscriptions(azPLS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build allowed subscriptions: %w", err)
	}
	allowedSubs := make([]*string, len(desiredSubs))
	for i, sub := range desiredSubs {
		allowedSubs[i] = ptr.To(sub)
	}

	// Preserve the existing PLS configuration and only update visibility/auto-approval.
	if existing.Properties == nil {
		existing.Properties = &armnetwork.PrivateLinkServiceProperties{}
	}
	existing.Properties.Visibility = &armnetwork.PrivateLinkServicePropertiesVisibility{
		Subscriptions: allowedSubs,
	}
	existing.Properties.AutoApproval = &armnetwork.PrivateLinkServicePropertiesAutoApproval{
		Subscriptions: allowedSubs,
	}

	updateCtx, updateCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer updateCancel()

	poller, err := r.PrivateLinkServices.BeginCreateOrUpdate(updateCtx, resourceGroup, plsName, existing, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, "PLSUpdateFailed", err)
	}

	if poller == nil {
		log.Info("Private Link Service update accepted, will check status on next reconcile", "name", plsName)
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	pollCtx, pollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
	defer pollCancel()

	resp, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, "PLSUpdateFailed", err)
	}

	log.Info("Private Link Service visibility/auto-approval updated successfully", "name", plsName)
	return r.updateStatusFromPLS(ctx, azPLS, &resp.PrivateLinkService)
}

// delete handles deletion of the management-side Azure Private Link Service (PLS).
//
// This function is called by the HO finalizer during AzurePrivateLinkService CR deletion.
// It first retrieves the PLS to check for active Private Endpoint (PE) connections. If any
// PE connections are in an active state (Approved or Pending), it rejects them by updating
// their connection state to "Rejected" before attempting PLS deletion. This mirrors the
// AWS pattern of rejecting VPC endpoint connections before deleting the endpoint service.
//
// If PE rejection succeeds but some connections have not yet fully disconnected, the
// controller requeues to allow Azure time to process the rejections before retrying
// PLS deletion.
func (r *AzurePrivateLinkServiceController) delete(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService) (bool, error) {
	log := log.FromContext(ctx).WithValues("azureprivatelinkservice", azPLS.Name, "namespace", azPLS.Namespace)

	plsID := azPLS.Status.PrivateLinkServiceID
	if plsID == "" {
		log.Info("No Private Link Service ID in status, nothing to delete")
		return true, nil
	}

	// Extract the PLS name from the Azure resource ID rather than recomputing it.
	// This avoids a dependency on the HostedCluster (which may be deleted) during cleanup.
	parsedID, err := arm.ParseResourceID(plsID)
	if err != nil {
		return false, fmt.Errorf("failed to parse Private Link Service resource ID %q: %w", plsID, err)
	}
	plsName := parsedID.Name
	resourceGroup := parsedID.ResourceGroupName

	// Reject any active PE connections before attempting PLS deletion.
	// Azure does not allow deleting a PLS with active PE connections,
	// so we proactively reject them to unblock deletion.
	rejected, err := r.rejectPrivateEndpointConnections(ctx, resourceGroup, plsName)
	if err != nil {
		return false, fmt.Errorf("failed to reject private endpoint connections: %w", err)
	}
	if rejected > 0 {
		log.Info("Rejected active private endpoint connections, will retry PLS deletion after connections are fully disconnected",
			"name", plsName, "rejectedCount", rejected)
		return false, nil
	}

	log.Info("Deleting Private Link Service", "name", plsName, "resourceGroup", resourceGroup)
	deleteCtx, deleteCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer deleteCancel()

	poller, err := r.PrivateLinkServices.BeginDelete(deleteCtx, resourceGroup, plsName, nil)
	if err != nil {
		if azureutil.IsAzureNotFoundError(err) {
			log.Info("Private Link Service not found, deletion already completed", "name", plsName)
			return true, nil
		}
		return false, fmt.Errorf("failed to delete Private Link Service: %w", err)
	}

	if poller == nil {
		log.Info("Private Link Service deletion accepted", "name", plsName)
		return true, nil
	}

	deletePollCtx, deletePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
	defer deletePollCancel()

	_, err = poller.PollUntilDone(deletePollCtx, nil)
	if err != nil {
		if azureutil.IsAzureNotFoundError(err) {
			log.Info("Private Link Service already deleted during polling", "name", plsName)
			return true, nil
		}
		return false, fmt.Errorf("private link service deletion failed: %w", err)
	}

	log.Info("Private Link Service deletion completed", "name", plsName)
	return true, nil
}

// rejectPrivateEndpointConnections retrieves the PLS, enumerates its PE connections,
// and rejects any that are in an active state ("Approved" or "Pending"). This mirrors
// the AWS pattern of calling RejectVpcEndpointConnections before deleting an endpoint
// service.
//
// Returns the number of connections that were rejected, and an error if the Get or
// UpdatePrivateEndpointConnection call fails. If the PLS is not found (already deleted),
// returns (0, nil).
func (r *AzurePrivateLinkServiceController) rejectPrivateEndpointConnections(ctx context.Context, resourceGroup, plsName string) (int, error) {
	log := log.FromContext(ctx).WithValues("plsName", plsName, "resourceGroup", resourceGroup)

	getCtx, getCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer getCancel()

	plsResp, err := r.PrivateLinkServices.Get(getCtx, resourceGroup, plsName, nil)
	if err != nil {
		if azureutil.IsAzureNotFoundError(err) {
			// PLS already deleted, nothing to reject
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get Private Link Service %q: %w", plsName, err)
	}

	if plsResp.Properties == nil || len(plsResp.Properties.PrivateEndpointConnections) == 0 {
		return 0, nil
	}

	rejected := 0
	for _, conn := range plsResp.Properties.PrivateEndpointConnections {
		if conn.Name == nil || conn.Properties == nil || conn.Properties.PrivateLinkServiceConnectionState == nil {
			continue
		}

		status := ""
		if conn.Properties.PrivateLinkServiceConnectionState.Status != nil {
			status = *conn.Properties.PrivateLinkServiceConnectionState.Status
		}

		// Only reject connections that are in an active state
		switch status {
		case azureutil.PEConnectionStateApproved, azureutil.PEConnectionStatePending:
			log.Info("Rejecting private endpoint connection",
				"connectionName", *conn.Name,
				"currentStatus", status)

			updateCtx, updateCancel := context.WithTimeout(ctx, azureAPITimeout)
			_, updateErr := r.PrivateLinkServices.UpdatePrivateEndpointConnection(
				updateCtx,
				resourceGroup,
				plsName,
				*conn.Name,
				armnetwork.PrivateEndpointConnection{
					Name: conn.Name,
					Properties: &armnetwork.PrivateEndpointConnectionProperties{
						PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
							Status:      ptr.To(azureutil.PEConnectionStateRejected),
							Description: ptr.To("Rejected during Private Link Service deletion"),
						},
					},
				},
				nil,
			)
			updateCancel()

			if updateErr != nil {
				return rejected, fmt.Errorf("failed to reject private endpoint connection %q: %w", *conn.Name, updateErr)
			}

			rejected++
			log.Info("Private endpoint connection rejected successfully", "connectionName", *conn.Name)
		default:
			log.V(1).Info("Skipping private endpoint connection in non-active state",
				"connectionName", *conn.Name,
				"status", status)
		}
	}

	return rejected, nil
}

// handleAzureError handles Azure API errors with appropriate status updates
func (r *AzurePrivateLinkServiceController) handleAzureError(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, reason string, err error) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("azureprivatelinkservice", azPLS.Name, "namespace", azPLS.Namespace)

	requeueAfter, message := azureutil.ClassifyAzureError(err)
	log.Error(err, message)

	patch := client.MergeFrom(azPLS.DeepCopy())
	for _, t := range []hyperv1.ConditionType{hyperv1.AzurePLSCreated, hyperv1.AzurePrivateLinkServiceAvailable} {
		meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
			Type:               string(t),
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            fmt.Sprintf("%s: %v", message, err),
			ObservedGeneration: azPLS.Generation,
		})
	}

	if patchErr := r.Status().Patch(ctx, azPLS, patch); patchErr != nil {
		log.Error(patchErr, "failed to update status")
		return ctrl.Result{}, patchErr
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// ensureNATSubnet creates or gets a NAT subnet in the ILB's VNet for Private Link Service.
// The ilbSubnetID identifies the subnet (and therefore VNet) where the ILB frontend IP lives.
// The NAT subnet must be in the same VNet as the ILB (Azure requirement).
// The subnet is named {infraID}-pls-nat and has privateLinkServiceNetworkPolicies disabled.
// The CIDR is dynamically allocated by finding the next available /24 block in the VNet's
// 10.0.0.0/16 address space that doesn't overlap with any existing subnet.
func (r *AzurePrivateLinkServiceController) ensureNATSubnet(ctx context.Context, hc *hyperv1.HostedCluster, ilbSubnetID string) (string, error) {
	// Parse the VNet name and resource group from the ILB's subnet ID.
	// The subnet ID has the form: /subscriptions/.../resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet}
	parsedID, err := arm.ParseResourceID(ilbSubnetID)
	if err != nil {
		return "", fmt.Errorf("failed to parse ILB subnet ID %q: %w", ilbSubnetID, err)
	}
	// The parent resource of the subnet is the VNet
	vnetName := parsedID.Parent.Name
	vnetRG := parsedID.ResourceGroupName

	subnetName := hc.Spec.InfraID + "-pls-nat"

	// Check if subnet already exists
	getCtx, getCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer getCancel()
	existing, err := r.Subnets.Get(getCtx, vnetRG, vnetName, subnetName, nil)
	if err == nil && existing.ID != nil {
		return *existing.ID, nil
	}
	if !azureutil.IsAzureNotFoundError(err) {
		return "", fmt.Errorf("failed to check for existing NAT subnet: %w", err)
	}

	// Find the next available /24 CIDR by listing existing subnets
	addressPrefix, err := r.findAvailableCIDR(ctx, vnetRG, vnetName)
	if err != nil {
		return "", fmt.Errorf("failed to find available CIDR for NAT subnet: %w", err)
	}

	// Create the NAT subnet with privateLinkServiceNetworkPolicies disabled
	disabled := armnetwork.VirtualNetworkPrivateLinkServiceNetworkPoliciesDisabled
	subnetParams := armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:                     ptr.To(addressPrefix),
			PrivateLinkServiceNetworkPolicies: &disabled,
		},
	}

	createCtx, createCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer createCancel()
	poller, err := r.Subnets.BeginCreateOrUpdate(createCtx, vnetRG, vnetName, subnetName, subnetParams, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create NAT subnet %s: %w", subnetName, err)
	}

	pollCtx, pollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
	defer pollCancel()
	result, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		return "", fmt.Errorf("failed waiting for NAT subnet creation: %w", err)
	}

	if result.ID == nil {
		return "", fmt.Errorf("created NAT subnet has no ID")
	}

	return *result.ID, nil
}

// findAvailableCIDR lists all existing subnets in the VNet and returns the next
// available /24 CIDR block that doesn't overlap with any of them. It scans
// candidate CIDRs starting from 10.0.1.0/24 within the 10.0.0.0/16 space
// (skipping 10.0.0.0/24 which is the default subnet).
func (r *AzurePrivateLinkServiceController) findAvailableCIDR(ctx context.Context, vnetRG, vnetName string) (string, error) {
	// Collect all existing subnet CIDRs
	var existingCIDRs []*net.IPNet
	pager := r.Subnets.NewListPager(vnetRG, vnetName, nil)
	for pager.More() {
		listCtx, listCancel := context.WithTimeout(ctx, azureAPITimeout)
		page, err := pager.NextPage(listCtx)
		listCancel()
		if err != nil {
			return "", fmt.Errorf("failed to list subnets in VNet %s/%s: %w", vnetRG, vnetName, err)
		}
		for _, subnet := range page.Value {
			if subnet.Properties != nil && subnet.Properties.AddressPrefix != nil {
				_, cidr, err := net.ParseCIDR(*subnet.Properties.AddressPrefix)
				if err != nil {
					continue
				}
				existingCIDRs = append(existingCIDRs, cidr)
			}
		}
	}

	// Try /24 blocks starting at 10.0.1.0/24 (skip .0 which is the default subnet)
	// through 10.0.255.0/24 (the last /24 in 10.0.0.0/16)
	for i := 1; i < 256; i++ {
		candidate := fmt.Sprintf("10.0.%d.0/24", i)
		_, candidateCIDR, _ := net.ParseCIDR(candidate)

		if !overlapsAny(candidateCIDR, existingCIDRs) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no available /24 CIDR block in 10.0.0.0/16 for NAT subnet")
}

// overlapsAny returns true if the candidate CIDR overlaps with any of the existing CIDRs.
// CIDR blocks are power-of-2 aligned, so two CIDRs either don't overlap at all or one
// fully contains the other — checking if either base IP falls within the other network
// is sufficient.
func overlapsAny(candidate *net.IPNet, existing []*net.IPNet) bool {
	for _, cidr := range existing {
		if candidate.Contains(cidr.IP) || cidr.Contains(candidate.IP) {
			return true
		}
	}
	return false
}

// constructPLSName builds a unique Private Link Service name using the cluster ID and service name.
// For backward compatibility, the original "private-router" service retains the legacy name format
// pls-{clusterID} (e.g., pls-12345678-abcd-1234-abcd-123456789012).
// All other services use the format pls-{serviceName}-{clusterID} (e.g., pls-oauth-openshift-{clusterID})
// to differentiate multiple PLS resources (KAS vs OAuth) within the same cluster.
// The cluster ID ensures uniqueness when multiple hosted clusters share a management resource group.
// This follows the same pattern as GCP Private Service Connect (psc-{clusterID}).
func constructPLSName(clusterID, serviceName string) string {
	// For backward compatibility, private-router uses the original name format
	if serviceName == "private-router" {
		return fmt.Sprintf("pls-%s", clusterID)
	}
	return fmt.Sprintf("pls-%s-%s", serviceName, clusterID)
}
