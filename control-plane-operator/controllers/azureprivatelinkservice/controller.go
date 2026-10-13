package azureprivatelinkservice

import (
	"context"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
)

const (
	// azurePrivateLinkServiceFinalizer is the CPO-side finalizer for AzurePrivateLinkService resources.
	// It is responsible for deleting all guest-side Azure resources created by the CPO controller:
	//   - Private Endpoint (PE) connecting the guest VNet to the management PLS
	//   - Private DNS Zone, VNet link, and A records for KAS hostname resolution
	//
	// Deletion ordering contract with the HO controller:
	// The HO controller manages a separate finalizer ("hypershift.openshift.io/azure-pls-cleanup")
	// that deletes the management-side Private Link Service (PLS). Azure enforces that a PLS
	// cannot be deleted while active Private Endpoint connections exist. Therefore, this CPO
	// finalizer MUST complete (deleting the PE) before the HO finalizer attempts PLS deletion.
	//
	// This ordering is naturally satisfied because both controllers independently reconcile on
	// deletion. If the HO attempts PLS deletion while the PE still exists, Azure returns an
	// error and the HO controller retries until the CPO has cleaned up the PE.
	azurePrivateLinkServiceFinalizer = "hypershift.openshift.io/azure-private-link-service"

	// azureAPITimeout is the timeout for individual Azure API calls to prevent hung reconcilers.
	// CPO uses 30s because it manages smaller, simpler resources:
	//   - Private Endpoint: connection point, typically fast to create
	//   - Private DNS Zone: simple DNS resource creation
	//   - VNet Link: lightweight link between zone and network
	//   - A Record: simple DNS record, synchronous operation
	// These operations are generally quick and reliable, so a shorter timeout is appropriate.
	// For comparison, HO controller uses 2m (see hypershift-operator/controllers/platform/azure/)
	// because it manages the more complex Private Link Service resource that requires
	// load balancer configuration, frontend IP mapping, and visibility/auto-approval settings.
	azureAPITimeout = 30 * time.Second

	// privateEndpointNameSuffix is appended to the CR name to form the Private Endpoint name.
	privateEndpointNameSuffix = "-pe"

	// dnsZoneLinkNameSuffix is appended to the CR name to form the VNet link name.
	dnsZoneLinkNameSuffix = "-vnet-link"

	// baseDomainLinkNameSuffix is appended to the CR name to form the base domain VNet link name.
	baseDomainLinkNameSuffix = "-basedomain-vnet-link"

	// privateRouterCRName is the expected CR name for the private-router PLS.
	// When the CR has this name, it represents the KAS/ingress service.
	// Any other CR name (e.g., "oauth-openshift") represents a service-specific PLS.
	privateRouterCRName = "private-router"

	// kasBaseDomainRecordPrefix is the prefix for the KAS A record in the base domain zone.
	// Combined with the cluster name, produces "api-<cluster-name>" as the record name.
	kasBaseDomainRecordPrefix = "api-"

	// oauthBaseDomainRecordPrefix is the prefix for the OAuth A record in the base domain zone.
	// Combined with the cluster name, produces "oauth-<cluster-name>" as the record name.
	oauthBaseDomainRecordPrefix = "oauth-"

	// privateDNSZoneSuffix is the suffix for the Private DNS Zone name.
	// The zone name is "<cluster-name>.hypershift.local", a synthetic internal
	// domain that only exists within the guest VNet via Azure Private DNS.
	privateDNSZoneSuffix = "hypershift.local"

	// kasARecordName is the record name for the KAS API endpoint within the Private DNS zone.
	// Combined with the zone name "<cluster-name>.hypershift.local", this produces the
	// FQDN "api.<cluster-name>.hypershift.local" for KAS resolution.
	kasARecordName = "api"

	// appsARecordName is the wildcard record name for all other services (OAuth, Konnectivity,
	// Ignition) within the Private DNS zone. All services share the single private router,
	// so both records resolve to the same Private Endpoint IP.
	// Example: *.apps.<cluster-name>.hypershift.local -> PE IP
	// This matches the AWS pattern which creates both api and *.apps records.
	appsARecordName = "*.apps"

	// aRecordTTL is the TTL for A records in seconds.
	aRecordTTL int64 = 300

	// hcpAzurePLSFinalizerName is a finalizer placed on the HostedControlPlane to ensure
	// Azure PE and DNS cleanup completes before the HCP and its credentials are torn down.
	//
	// Problem: During HCP deletion, credentials (workload identity tokens, service principal
	// secrets) may become invalid before the CPO reconciler gets a chance to clean up Azure
	// resources. Without this finalizer, the AzurePrivateLinkService CR's own finalizer
	// runs during CR deletion, but by that point the HCP credentials may already be gone,
	// causing Azure API calls to fail with authentication errors. This orphans PE and DNS
	// resources in the customer's Azure subscription.
	//
	// Solution: This HCP finalizer blocks HCP deletion until the CPO reconciler confirms
	// all Azure resources (PE, DNS zone, VNet link, A record) are deleted. Only then is
	// the finalizer removed, allowing HCP deletion to proceed with credentials still valid.
	//
	// Flow:
	//   1. During normal reconciliation, this finalizer is added to the HCP.
	//   2. When HCP.DeletionTimestamp is set, the reconciler detects it via the HCP watch,
	//      performs Azure resource cleanup, and removes this finalizer from the HCP.
	//   3. HCP deletion then proceeds, tearing down credentials and the namespace.
	hcpAzurePLSFinalizerName = "hypershift.openshift.io/azure-pls-endpoint-cleanup"
)

// PrivateEndpointsAPI abstracts the Azure Private Endpoints client.
type PrivateEndpointsAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, privateEndpointName string, parameters armnetwork.PrivateEndpoint, options *armnetwork.PrivateEndpointsClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, privateEndpointName string, options *armnetwork.PrivateEndpointsClientBeginDeleteOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientDeleteResponse], error)
	Get(ctx context.Context, resourceGroupName string, privateEndpointName string, options *armnetwork.PrivateEndpointsClientGetOptions) (armnetwork.PrivateEndpointsClientGetResponse, error)
}

// PrivateDNSZonesAPI abstracts the Azure Private DNS Zones client.
type PrivateDNSZonesAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, privateZoneName string, parameters armprivatedns.PrivateZone, options *armprivatedns.PrivateZonesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, privateZoneName string, options *armprivatedns.PrivateZonesClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientDeleteResponse], error)
}

// VirtualNetworkLinksAPI abstracts the Azure Virtual Network Links client.
type VirtualNetworkLinksAPI interface {
	BeginCreateOrUpdate(ctx context.Context, resourceGroupName string, privateZoneName string, virtualNetworkLinkName string, parameters armprivatedns.VirtualNetworkLink, options *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse], error)
	BeginDelete(ctx context.Context, resourceGroupName string, privateZoneName string, virtualNetworkLinkName string, options *armprivatedns.VirtualNetworkLinksClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientDeleteResponse], error)
}

// RecordSetsAPI abstracts the Azure Record Sets client.
type RecordSetsAPI interface {
	CreateOrUpdate(ctx context.Context, resourceGroupName string, privateZoneName string, recordType armprivatedns.RecordType, relativeRecordSetName string, parameters armprivatedns.RecordSet, options *armprivatedns.RecordSetsClientCreateOrUpdateOptions) (armprivatedns.RecordSetsClientCreateOrUpdateResponse, error)
	Delete(ctx context.Context, resourceGroupName string, privateZoneName string, recordType armprivatedns.RecordType, relativeRecordSetName string, options *armprivatedns.RecordSetsClientDeleteOptions) (armprivatedns.RecordSetsClientDeleteResponse, error)
}

// AzurePrivateLinkServiceReconciler reconciles AzurePrivateLinkService resources by
// creating Private Endpoints, Private DNS Zones, VNet links, and A records in Azure.
//
// It runs in the CPO (control-plane-operator) with scoped Azure credentials for the
// guest VNet. The reconciler waits for the HO platform controller to populate the PLS
// alias in the CR status before creating guest-side resources.
//
// Azure Credentials:
// The CPO uses a workload identity scoped to the guest resource group/VNet.
// Required Azure RBAC permissions:
//   - Microsoft.Network/privateEndpoints/read, write, delete (PE lifecycle)
//   - Microsoft.Network/privateDnsZones/read, write, delete (DNS zone lifecycle)
//   - Microsoft.Network/privateDnsZones/virtualNetworkLinks/read, write, delete (VNet link)
//   - Microsoft.Network/privateDnsZones/A/read, write, delete (A record management)
//
// Azure SDK client interfaces are used instead of concrete types to enable unit testing.
type AzurePrivateLinkServiceReconciler struct {
	client.Client
	PrivateEndpoints    PrivateEndpointsAPI
	PrivateDNSZones     PrivateDNSZonesAPI
	VirtualNetworkLinks VirtualNetworkLinksAPI
	RecordSets          RecordSetsAPI
}

// SetupWithManager sets up the controller with the Manager.
// It watches AzurePrivateLinkService CRs as the primary resource, and also watches
// HostedControlPlane objects so that deletion of the HCP triggers reconciliation of
// the associated AzurePrivateLinkService CR. This enables the HCP finalizer to block
// HCP deletion until Azure resource cleanup is complete.
func (r *AzurePrivateLinkServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AzurePrivateLinkService{}).
		Watches(&hyperv1.HostedControlPlane{}, handler.EnqueueRequestsFromMapFunc(
			r.mapHCPToAzurePLS(),
		)).
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

// mapHCPToAzurePLS maps HostedControlPlane events to the AzurePrivateLinkService CRs
// in the same namespace. This ensures the reconciler is triggered when an HCP is being
// deleted, allowing it to perform Azure resource cleanup and remove the HCP finalizer.
func (r *AzurePrivateLinkServiceReconciler) mapHCPToAzurePLS() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		hcp, ok := obj.(*hyperv1.HostedControlPlane)
		if !ok {
			return nil
		}

		// Only trigger reconciliation when the HCP has our finalizer; this avoids
		// unnecessary reconciliations for HCPs that are not related to Azure PLS.
		if !controllerutil.ContainsFinalizer(hcp, hcpAzurePLSFinalizerName) {
			return nil
		}

		azPLSList := &hyperv1.AzurePrivateLinkServiceList{}
		if err := r.List(ctx, azPLSList, client.InNamespace(hcp.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list AzurePrivateLinkService resources for HCP mapping", "namespace", hcp.Namespace)
			return nil
		}

		var requests []reconcile.Request
		for i := range azPLSList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      azPLSList.Items[i].Name,
					Namespace: azPLSList.Items[i].Namespace,
				},
			})
		}
		return requests
	}
}

// Reconcile implements the main reconciliation logic for Azure Private Link Service resources.
func (r *AzurePrivateLinkServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// 2. Handle AzurePrivateLinkService CR deletion
	if !azPLS.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(azPLS, azurePrivateLinkServiceFinalizer) {
			return ctrl.Result{}, nil
		}

		if err := r.reconcileDelete(ctx, azPLS, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete resources: %w", err)
		}

		controllerutil.RemoveFinalizer(azPLS, azurePrivateLinkServiceFinalizer)
		if err := r.Update(ctx, azPLS); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// 3. Add CR finalizer if not present
	if !controllerutil.ContainsFinalizer(azPLS, azurePrivateLinkServiceFinalizer) {
		controllerutil.AddFinalizer(azPLS, azurePrivateLinkServiceFinalizer)
		if err := r.Update(ctx, azPLS); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 4. Wait for PLS alias to be available (populated by HO platform controller)
	if azPLS.Status.PrivateLinkServiceAlias == "" {
		log.Info("PLS alias not yet available, waiting")
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	// 5. Look up the HostedControlPlane for the KAS hostname
	hcp, err := r.getHostedControlPlane(ctx, azPLS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// 6. Handle HCP deletion: clean up Azure resources and remove HCP finalizer
	if !hcp.DeletionTimestamp.IsZero() {
		return r.reconcileHCPDeletion(ctx, azPLS, hcp, log)
	}

	// 7. Add HCP finalizer to block HCP deletion until Azure cleanup is done.
	// This is done after the PLS alias is available, which means Azure resources
	// are about to be created or already exist. Adding the finalizer at this point
	// ensures we only block HCP deletion when there are actually resources to clean up.
	if result, err := r.ensureHCPFinalizer(ctx, hcp, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 8. Reconcile Private Endpoint
	if result, err := r.reconcilePrivateEndpoint(ctx, azPLS, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 9. Reconcile Private DNS Zone, VNet link, and A records.
	// The zone name uses the HCP name (cluster name) with a ".hypershift.local" suffix,
	// producing FQDNs like "api.<cluster-name>.hypershift.local" and
	// "*.apps.<cluster-name>.hypershift.local". This is a synthetic internal domain
	// that only exists within the guest VNet via Azure Private DNS.
	// Only the private-router CR creates these records; non-private-router CRs (e.g.,
	// oauth-openshift) skip this because they have their own PE IP that should only
	// be used for base domain records specific to that service.
	if azPLS.Name == privateRouterCRName {
		if result, err := r.reconcileDNS(ctx, azPLS, hcp.Name, log); err != nil || !result.IsZero() {
			return result, err
		}
	} else if azPLS.Status.DNSZoneName == "" {
		// For non-private-router CRs (e.g., oauth-openshift), persist the DNS zone name
		// in status so reconcileDelete can derive clusterName for base domain record cleanup.
		// These CRs don't create the internal zone, but need the zone name format for deletion.
		dnsZoneName := fmt.Sprintf("%s.%s", hcp.Name, privateDNSZoneSuffix)
		patch := client.MergeFrom(azPLS.DeepCopy())
		azPLS.Status.DNSZoneName = dnsZoneName
		if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to persist DNS zone name in status: %w", err)
		}
	}

	// 10. Reconcile base domain Private DNS Zone for external hostname resolution.
	// Workers need to resolve api-<name>.<basedomain> and oauth-<name>.<basedomain>
	// to the PE IP so that the console, OAuth, and other services work on private clusters.
	if azPLS.Spec.BaseDomain != "" {
		if result, err := r.reconcileBaseDomainDNS(ctx, azPLS, hcp.Name, log); err != nil || !result.IsZero() {
			return result, err
		}
	}

	// 11. Set overall Available condition
	patch := client.MergeFrom(azPLS.DeepCopy())
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.AzurePrivateLinkServiceAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.AzurePLSSuccessReason,
		Message:            "All Azure Private Link Service resources are available",
		ObservedGeneration: azPLS.Generation,
	})
	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Available condition: %w", err)
	}

	// 12. Requeue for drift detection
	return ctrl.Result{RequeueAfter: azureutil.DriftDetectionRequeueInterval}, nil
}

// ensureHCPFinalizer adds the HCP finalizer if not already present.
// Uses optimistic locking via MergeFromWithOptimisticLock to safely update the HCP.
func (r *AzurePrivateLinkServiceReconciler) ensureHCPFinalizer(ctx context.Context, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(hcp, hcpAzurePLSFinalizerName) {
		return ctrl.Result{}, nil
	}

	log.Info("Adding HCP finalizer for Azure PLS cleanup")
	originalHCP := hcp.DeepCopy()
	controllerutil.AddFinalizer(hcp, hcpAzurePLSFinalizerName)
	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to add HCP finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// reconcileHCPDeletion handles HCP deletion by cleaning up Azure resources and removing
// the HCP finalizer. This ensures credentials remain valid during the cleanup process.
//
// The flow is:
//  1. If the HCP does not have our finalizer, nothing to do.
//  2. Perform Azure resource cleanup (PE, DNS zone, VNet link, A record).
//  3. Remove the HCP finalizer to unblock HCP deletion.
func (r *AzurePrivateLinkServiceReconciler) reconcileHCPDeletion(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(hcp, hcpAzurePLSFinalizerName) {
		return ctrl.Result{}, nil
	}

	log.Info("HCP is being deleted, cleaning up Azure resources before removing HCP finalizer")

	// Perform Azure resource cleanup
	if err := r.reconcileDelete(ctx, azPLS, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clean up Azure resources during HCP deletion: %w", err)
	}

	// Remove the HCP finalizer to unblock HCP deletion
	log.Info("Azure resource cleanup complete, removing HCP finalizer")
	originalHCP := hcp.DeepCopy()
	controllerutil.RemoveFinalizer(hcp, hcpAzurePLSFinalizerName)
	if err := r.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to remove HCP finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcilePrivateEndpoint creates or updates the Private Endpoint in the guest VNet.
func (r *AzurePrivateLinkServiceReconciler) reconcilePrivateEndpoint(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, log logr.Logger) (ctrl.Result, error) {
	resourceGroup := azPLS.Spec.ResourceGroupName
	peName := privateEndpointName(azPLS.Name)
	if err := azureutil.ValidateAzureResourceName(peName, "Private Endpoint"); err != nil {
		return ctrl.Result{}, err
	}

	// Check if PE already exists. Expand networkInterfaces so the NIC IP
	// configurations are included in the response — without this, the PE
	// response only contains NIC IDs without their properties, and the
	// private IP address cannot be extracted.
	existingPE, err := r.PrivateEndpoints.Get(ctx, resourceGroup, peName, &armnetwork.PrivateEndpointsClientGetOptions{
		Expand: ptr.To("networkInterfaces"),
	})
	if err == nil {
		// PE exists, extract IP and update status
		peIP := extractPrivateEndpointIP(existingPE.PrivateEndpoint)
		return r.updatePrivateEndpointStatus(ctx, azPLS, existingPE.PrivateEndpoint, peIP, log)
	}
	if !azureutil.IsAzureNotFoundError(err) {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateEndpointAvailable), "PrivateEndpointGetFailed", fmt.Errorf("failed to get Private Endpoint: %w", err), log)
	}

	log.Info("Creating Private Endpoint", "name", peName, "resourceGroup", resourceGroup)

	peParams := armnetwork.PrivateEndpoint{
		Location: ptr.To(azPLS.Spec.Location),
		Tags: map[string]*string{
			"hypershift.openshift.io_resource-type": ptr.To("private-endpoint"),
		},
		Properties: &armnetwork.PrivateEndpointProperties{
			Subnet: &armnetwork.Subnet{
				ID: ptr.To(string(azPLS.Spec.GuestSubnetID)),
			},
			ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
				{
					Name: ptr.To(peName),
					Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
						PrivateLinkServiceID: ptr.To(azPLS.Status.PrivateLinkServiceAlias),
						RequestMessage:       ptr.To("HyperShift CPO auto-connect"),
					},
				},
			},
		},
	}

	apiCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
	defer cancel()

	poller, err := r.PrivateEndpoints.BeginCreateOrUpdate(apiCtx, resourceGroup, peName, peParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateEndpointAvailable), "PrivateEndpointCreationFailed", fmt.Errorf("failed to begin creating Private Endpoint: %w", err), log)
	}

	if poller != nil {
		pollCtx, pollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer pollCancel()

		peResp, err := poller.PollUntilDone(pollCtx, nil)
		if err != nil {
			return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateEndpointAvailable), "PrivateEndpointCreationFailed", fmt.Errorf("failed to create Private Endpoint: %w", err), log)
		}

		peIP := extractPrivateEndpointIP(peResp.PrivateEndpoint)
		log.Info("Private Endpoint created", "name", peName, "id", ptr.Deref(peResp.ID, ""), "ip", peIP)

		return r.updatePrivateEndpointStatus(ctx, azPLS, peResp.PrivateEndpoint, peIP, log)
	}

	return ctrl.Result{}, nil
}

// updatePrivateEndpointStatus updates the CR status with PE information and sets the PE condition.
// It also checks the Private Endpoint connection state; if the connection is not "Approved"
// (e.g., the guest subscription is not in the PLS auto-approval list), a warning condition
// is set and the controller requeues to wait for approval.
func (r *AzurePrivateLinkServiceReconciler) updatePrivateEndpointStatus(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, pe armnetwork.PrivateEndpoint, peIP string, log logr.Logger) (ctrl.Result, error) {
	patch := client.MergeFrom(azPLS.DeepCopy())

	azPLS.Status.PrivateEndpointID = ptr.Deref(pe.ID, "")
	azPLS.Status.PrivateEndpointIP = peIP

	// Check the PE connection state. When using ManualPrivateLinkServiceConnections
	// (as this controller does), the connection requires explicit approval on the PLS
	// side. If the guest subscription is not in the PLS auto-approval list, the
	// connection will remain in "Pending" state indefinitely until manually approved.
	// TODO(CNTRLPLANE-2172): Consider implementing automatic retry with timeout and
	// escalation to a more prominent error state if the connection is not approved
	// within a reasonable time period.
	connectionState := extractPrivateEndpointConnectionState(pe)
	log.Info("Private Endpoint connection state", "state", connectionState)

	if connectionState != "" && connectionState != azureutil.PEConnectionStateApproved {
		meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
			Type:   string(hyperv1.AzurePrivateEndpointAvailable),
			Status: metav1.ConditionFalse,
			Reason: "PrivateEndpointConnectionNotApproved",
			Message: fmt.Sprintf("Private Endpoint connection state is %q, not Approved. "+
				"Ensure the guest subscription is in the PLS auto-approval list, "+
				"or manually approve the connection on the Private Link Service.", connectionState),
			ObservedGeneration: azPLS.Generation,
		})

		if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
			log.Error(err, "failed to update status with Private Endpoint connection state")
			return ctrl.Result{}, err
		}

		// Requeue to check again; the connection may be approved out-of-band.
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.AzurePrivateEndpointAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.AzurePLSSuccessReason,
		Message:            "Private Endpoint is available",
		ObservedGeneration: azPLS.Generation,
	})

	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		log.Error(err, "failed to update status with Private Endpoint information")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// dnsZoneConfig captures the per-zone differences between reconcileDNS and
// reconcileBaseDomainDNS so that the shared reconcileDNSZone helper can handle both.
type dnsZoneConfig struct {
	// zoneName is the FQDN of the Private DNS Zone to create (e.g. "cluster.hypershift.local" or "example.com").
	zoneName string
	// linkName is the name of the VNet link to create in the zone.
	linkName string
	// linkDisplayName is used in validation error messages (e.g. "VNet Link" or "Base Domain VNet Link").
	linkDisplayName string
	// recordNames are the A record names to create in the zone.
	recordNames []string
	// logPrefix is a lowercase qualifier prepended to log messages so callers can
	// distinguish zone types (e.g. "" or "base domain").
	logPrefix string
	// logPrefixTitle is the title-case form of logPrefix, used at the start of log
	// messages (e.g. "" or "Base domain").
	logPrefixTitle string
	// errorReasonPrefix is prepended to error condition reasons (e.g. "" or "BaseDomain").
	errorReasonPrefix string
	// updateStatus is called after the zone, link, and records are created to persist
	// zone-specific status fields (zone ID, zone name, condition, etc.).
	updateStatus func(azPLS *hyperv1.AzurePrivateLinkService, zoneID string)
}

// errMsgQualifier returns the logPrefix followed by a space (e.g. "base domain ") for use
// in error messages, or an empty string when no prefix is set.
func (c dnsZoneConfig) errMsgQualifier() string {
	if c.logPrefix == "" {
		return ""
	}
	return c.logPrefix + " "
}

// reconcileDNSZone is the shared helper that creates or updates a Private DNS Zone, VNet link,
// and A records for a given zone configuration. Both reconcileDNS and reconcileBaseDomainDNS
// delegate to this function after constructing the appropriate dnsZoneConfig.
func (r *AzurePrivateLinkServiceReconciler) reconcileDNSZone(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, cfg dnsZoneConfig, log logr.Logger) (ctrl.Result, error) {
	if azPLS.Status.PrivateEndpointIP == "" {
		log.Info("Private Endpoint IP not available yet, skipping DNS reconciliation", "zone", cfg.zoneName)
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	resourceGroup := azPLS.Spec.ResourceGroupName

	// 1. Create Private DNS Zone
	logMsg := "Reconciling Private DNS Zone"
	if cfg.logPrefix != "" {
		logMsg = fmt.Sprintf("Reconciling %s Private DNS Zone", cfg.logPrefix)
	}
	log.Info(logMsg, "zone", cfg.zoneName)

	zoneParams := armprivatedns.PrivateZone{
		Location: ptr.To("global"),
		Tags: map[string]*string{
			"hypershift.openshift.io_resource-type": ptr.To("private-dns-zone"),
		},
	}

	apiCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
	defer cancel()

	zonePoller, err := r.PrivateDNSZones.BeginCreateOrUpdate(apiCtx, resourceGroup, cfg.zoneName, zoneParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), cfg.errorReasonPrefix+"DNSZoneCreationFailed",
			fmt.Errorf("failed to begin creating %sPrivate DNS Zone: %w", cfg.errMsgQualifier(), err), log)
	}

	var zoneResp armprivatedns.PrivateZonesClientCreateOrUpdateResponse
	if zonePoller != nil {
		zonePollCtx, zonePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer zonePollCancel()

		zoneResp, err = zonePoller.PollUntilDone(zonePollCtx, nil)
		if err != nil {
			return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), cfg.errorReasonPrefix+"DNSZoneCreationFailed",
				fmt.Errorf("failed to create %sPrivate DNS Zone: %w", cfg.errMsgQualifier(), err), log)
		}
	}

	zoneLogMsg := "Private DNS Zone reconciled"
	if cfg.logPrefix != "" {
		zoneLogMsg = fmt.Sprintf("%s Private DNS Zone reconciled", cfg.logPrefixTitle)
	}
	log.Info(zoneLogMsg, "id", ptr.Deref(zoneResp.ID, ""))

	// 2. Create VNet Link
	if err := azureutil.ValidateAzureResourceName(cfg.linkName, cfg.linkDisplayName); err != nil {
		return ctrl.Result{}, err
	}

	linkLogMsg := "Reconciling VNet Link"
	if cfg.logPrefix != "" {
		linkLogMsg = fmt.Sprintf("Reconciling %s VNet Link", cfg.logPrefix)
	}
	log.Info(linkLogMsg, "name", cfg.linkName)

	linkParams := armprivatedns.VirtualNetworkLink{
		Location: ptr.To("global"),
		Tags: map[string]*string{
			"hypershift.openshift.io_resource-type": ptr.To("vnet-link"),
		},
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork: &armprivatedns.SubResource{
				ID: ptr.To(string(azPLS.Spec.GuestVNetID)),
			},
			RegistrationEnabled: ptr.To(false),
		},
	}

	linkCtx, linkCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer linkCancel()

	linkPoller, err := r.VirtualNetworkLinks.BeginCreateOrUpdate(linkCtx, resourceGroup, cfg.zoneName, cfg.linkName, linkParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), cfg.errorReasonPrefix+"VNetLinkCreationFailed",
			fmt.Errorf("failed to begin creating %sVNet Link: %w", cfg.errMsgQualifier(), err), log)
	}

	if linkPoller != nil {
		linkPollCtx, linkPollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer linkPollCancel()

		if _, err := linkPoller.PollUntilDone(linkPollCtx, nil); err != nil {
			// Azure returns a Conflict error when the DNS zone is already linked to
			// the same VNet via a different link name (e.g., created by a sibling
			// AzurePrivateLinkService CR). This is expected in multi-CR PLS setups
			// where both oauth-openshift and private-router share the base domain zone.
			if strings.Contains(err.Error(), "already linked to the virtual network") {
				log.Info("VNet is already linked to DNS zone (likely by sibling CR), continuing", "zone", cfg.zoneName, "linkName", cfg.linkName)
			} else {
				return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), cfg.errorReasonPrefix+"VNetLinkCreationFailed",
					fmt.Errorf("failed to create %sVNet Link: %w", cfg.errMsgQualifier(), err), log)
			}
		}
	}

	linkDoneMsg := "VNet Link reconciled"
	if cfg.logPrefix != "" {
		linkDoneMsg = fmt.Sprintf("%s VNet Link reconciled", cfg.logPrefixTitle)
	}
	log.Info(linkDoneMsg, "name", cfg.linkName)

	// 3. Create A Records
	ttl := aRecordTTL
	recordParams := armprivatedns.RecordSet{
		Properties: &armprivatedns.RecordSetProperties{
			TTL: &ttl,
			ARecords: []*armprivatedns.ARecord{
				{
					IPv4Address: ptr.To(azPLS.Status.PrivateEndpointIP),
				},
			},
		},
	}

	for _, recordName := range cfg.recordNames {
		recLogMsg := "Reconciling A record"
		recDoneMsg := "A record reconciled"
		if cfg.logPrefix != "" {
			recLogMsg = fmt.Sprintf("Reconciling %s A record", cfg.logPrefix)
			recDoneMsg = fmt.Sprintf("%s A record reconciled", cfg.logPrefixTitle)
		}
		log.Info(recLogMsg, "record", recordName, "zone", cfg.zoneName, "ip", azPLS.Status.PrivateEndpointIP)
		if _, err := r.RecordSets.CreateOrUpdate(ctx, resourceGroup, cfg.zoneName, armprivatedns.RecordTypeA, recordName, recordParams, nil); err != nil {
			return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), cfg.errorReasonPrefix+"ARecordCreationFailed",
				fmt.Errorf("failed to create %sA record %q: %w", cfg.errMsgQualifier(), recordName, err), log)
		}
		log.Info(recDoneMsg, "record", recordName, "ip", azPLS.Status.PrivateEndpointIP)
	}

	// 4. Update status
	patch := client.MergeFrom(azPLS.DeepCopy())
	cfg.updateStatus(azPLS, ptr.Deref(zoneResp.ID, ""))
	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		log.Error(err, "failed to update status with DNS information", "zone", cfg.zoneName)
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDNS creates or updates the Private DNS Zone, VNet link, and A records.
// The clusterName parameter is the HCP name, used to construct the zone name
// "<cluster-name>.hypershift.local".
func (r *AzurePrivateLinkServiceReconciler) reconcileDNS(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, clusterName string, log logr.Logger) (ctrl.Result, error) {
	dnsZoneName := fmt.Sprintf("%s.%s", clusterName, privateDNSZoneSuffix)
	return r.reconcileDNSZone(ctx, azPLS, dnsZoneConfig{
		zoneName:          dnsZoneName,
		linkName:          vnetLinkName(azPLS.Name),
		linkDisplayName:   "VNet Link",
		recordNames:       []string{kasARecordName, appsARecordName},
		logPrefix:         "",
		errorReasonPrefix: "",
		updateStatus: func(azPLS *hyperv1.AzurePrivateLinkService, zoneID string) {
			azPLS.Status.PrivateDNSZoneID = zoneID
			azPLS.Status.DNSZoneName = dnsZoneName
			meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.AzurePrivateDNSAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AzurePLSSuccessReason,
				Message:            "Private DNS zone and A records are available",
				ObservedGeneration: azPLS.Generation,
			})
		},
	}, log)
}

// reconcileBaseDomainDNS creates a Private DNS Zone for the cluster's base domain,
// links it to the guest VNet, and creates A records for the API and/or OAuth hostnames.
// This enables worker VMs to resolve api-<name>.<basedomain> and oauth-<name>.<basedomain>
// to the Private Endpoint IP, which is required for the console OAuth flow and other
// services that use the external API/OAuth hostnames from within the private network.
//
// Record selection depends on the CR name:
//   - private-router: Creates api-<name> record. Also creates oauth-<name> record
//     ONLY if no sibling OAuth AzurePrivateLinkService CR exists in the same namespace
//     (backward compatibility for clusters without a separate OAuth PLS).
//   - Any other CR (e.g., oauth-openshift): Creates only oauth-<name> record, pointing
//     to this CR's own PE IP.
func (r *AzurePrivateLinkServiceReconciler) reconcileBaseDomainDNS(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, clusterName string, log logr.Logger) (ctrl.Result, error) {
	recordNames, err := r.recordNamesForCR(ctx, azPLS, clusterName, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.reconcileDNSZone(ctx, azPLS, dnsZoneConfig{
		zoneName:          azPLS.Spec.BaseDomain,
		linkName:          baseDomainVNetLinkName(azPLS.Name),
		linkDisplayName:   "Base Domain VNet Link",
		recordNames:       recordNames,
		logPrefix:         "base domain",
		logPrefixTitle:    "Base domain",
		errorReasonPrefix: "BaseDomain",
		updateStatus: func(azPLS *hyperv1.AzurePrivateLinkService, zoneID string) {
			azPLS.Status.BaseDomainDNSZoneID = zoneID
			meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
				Type:               string(hyperv1.AzurePrivateDNSAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AzurePLSSuccessReason,
				Message:            "Base domain private DNS zone and A records are available",
				ObservedGeneration: azPLS.Generation,
			})
		},
	}, log)
}

// recordNamesForCR determines which DNS A records this CR is responsible for.
// The private-router CR creates the api record and, for backward compatibility,
// the oauth record when no sibling OAuth CR exists. All other CRs create only
// the oauth record.
func (r *AzurePrivateLinkServiceReconciler) recordNamesForCR(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, clusterName string, log logr.Logger) ([]string, error) {
	if azPLS.Name != privateRouterCRName {
		return []string{oauthBaseDomainRecordPrefix + clusterName}, nil
	}

	records := []string{kasBaseDomainRecordPrefix + clusterName}

	hasSiblingOAuth, err := r.hasSiblingCR(ctx, azPLS)
	if err != nil {
		return nil, fmt.Errorf("failed to check for sibling AzurePrivateLinkService CRs: %w", err)
	}
	if hasSiblingOAuth {
		log.Info("Sibling OAuth AzurePrivateLinkService CR exists, skipping oauth base domain record from private-router")
	} else {
		records = append(records, oauthBaseDomainRecordPrefix+clusterName)
	}

	return records, nil
}

// hasSiblingCR checks whether any other AzurePrivateLinkService CRs exist in the
// same namespace with the same base domain. This is used to determine whether the
// private-router CR should create the oauth base domain record or defer to a
// dedicated OAuth CR.
func (r *AzurePrivateLinkServiceReconciler) hasSiblingCR(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService) (bool, error) {
	azPLSList := &hyperv1.AzurePrivateLinkServiceList{}
	if err := r.List(ctx, azPLSList, client.InNamespace(azPLS.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list sibling AzurePrivateLinkService CRs: %w", err)
	}
	for _, sibling := range azPLSList.Items {
		if sibling.Name != azPLS.Name && sibling.Spec.BaseDomain == azPLS.Spec.BaseDomain && sibling.DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	return false, nil
}

// handleAzureError provides differentiated backoff for Azure API errors.
// It delegates to azureutil.ClassifyAzureError for error classification and
// requeue interval selection, then updates the CR status condition.
func (r *AzurePrivateLinkServiceReconciler) handleAzureError(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, conditionType string, reason string, err error, log logr.Logger) (ctrl.Result, error) {
	requeueAfter, message := azureutil.ClassifyAzureError(err)
	log.Error(err, message)

	patch := client.MergeFrom(azPLS.DeepCopy())
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf("%s: %v", message, err),
		ObservedGeneration: azPLS.Generation,
	})

	if patchErr := r.Status().Patch(ctx, azPLS, patch); patchErr != nil {
		log.Error(patchErr, "failed to update status")
		return ctrl.Result{}, patchErr
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// reconcileDelete cleans up all Azure resources created by this controller (CPO side).
// Deletion order within this controller: A record -> VNet link -> DNS zone -> Private Endpoint.
// The VNet link must be removed before the DNS zone can be deleted.
// DNS deletion errors are returned to ensure the controller retries and does not orphan resources.
//
// Cross-controller ordering: This function deletes the Private Endpoint BEFORE the HO
// controller's finalizer deletes the Private Link Service (PLS). Azure rejects PLS
// deletion when active PE connections exist, so PE removal here is a prerequisite for
// the HO finalizer to succeed. The HO controller will retry PLS deletion until this
// CPO finalizer has completed PE cleanup and removed its finalizer from the CR.
func (r *AzurePrivateLinkServiceReconciler) reconcileDelete(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, log logr.Logger) error {
	resourceGroup := azPLS.Spec.ResourceGroupName

	// 1. Delete DNS resources using the zone name persisted in status.
	// This avoids a dependency on the HostedControlPlane during deletion, which may
	// already be torn down or unavailable when the finalizer runs.
	dnsZoneName := azPLS.Status.DNSZoneName
	if dnsZoneName != "" {
		// Delete both A records (KAS apex and wildcard apps)
		for _, recordName := range []string{kasARecordName, appsARecordName} {
			log.Info("Deleting A record", "record", recordName, "zone", dnsZoneName)
			deleteCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
			if _, err := r.RecordSets.Delete(deleteCtx, resourceGroup, dnsZoneName, armprivatedns.RecordTypeA, recordName, nil); err != nil {
				cancel()
				if !azureutil.IsAzureNotFoundError(err) {
					return fmt.Errorf("failed to delete A record %q: %w", recordName, err)
				}
			}
			cancel()
		}

		// 2. Delete VNet link (must be deleted before zone)
		linkName := vnetLinkName(azPLS.Name)
		log.Info("Deleting VNet Link", "name", linkName)
		deleteCtx2, cancel2 := context.WithTimeout(ctx, azureAPITimeout)
		defer cancel2()
		linkPoller, err := r.VirtualNetworkLinks.BeginDelete(deleteCtx2, resourceGroup, dnsZoneName, linkName, nil)
		if err != nil {
			if !azureutil.IsAzureNotFoundError(err) {
				return fmt.Errorf("failed to begin deleting VNet Link: %w", err)
			}
		} else if linkPoller != nil {
			linkPollCtx, linkPollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
			defer linkPollCancel()

			if _, err := linkPoller.PollUntilDone(linkPollCtx, nil); err != nil {
				if !azureutil.IsAzureNotFoundError(err) {
					return fmt.Errorf("failed to delete VNet Link: %w", err)
				}
			}
		}

		// 3. Delete Private DNS Zone
		log.Info("Deleting Private DNS Zone", "zone", dnsZoneName)
		deleteCtx3, cancel3 := context.WithTimeout(ctx, azureAPITimeout)
		defer cancel3()
		zonePoller, err := r.PrivateDNSZones.BeginDelete(deleteCtx3, resourceGroup, dnsZoneName, nil)
		if err != nil {
			if !azureutil.IsAzureNotFoundError(err) {
				return fmt.Errorf("failed to begin deleting Private DNS Zone: %w", err)
			}
		} else if zonePoller != nil {
			zonePollCtx, zonePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
			defer zonePollCancel()

			if _, err := zonePoller.PollUntilDone(zonePollCtx, nil); err != nil {
				if !azureutil.IsAzureNotFoundError(err) {
					return fmt.Errorf("failed to delete Private DNS Zone: %w", err)
				}
			}
		}
	} else {
		log.V(1).Info("DNSZoneName not set in status, skipping DNS cleanup")
	}

	// 4. Delete base domain DNS resources (A records, VNet link, zone)
	baseDomain := azPLS.Spec.BaseDomain
	if baseDomain != "" {
		// Extract cluster name from the hypershift.local zone name (format: "<cluster-name>.hypershift.local")
		clusterName := ""
		if dnsZoneName != "" {
			// Strip the ".hypershift.local" suffix to get the cluster name
			if name, ok := strings.CutSuffix(dnsZoneName, "."+privateDNSZoneSuffix); ok {
				clusterName = name
			}
		}

		if clusterName != "" {
			// Determine which base domain A records this CR owns based on its name.
			// This mirrors the creation logic in reconcileBaseDomainDNS.
			var baseDomainRecords []string
			if azPLS.Name == privateRouterCRName {
				baseDomainRecords = append(baseDomainRecords, kasBaseDomainRecordPrefix+clusterName)
				// Only delete the oauth record if there is no sibling OAuth CR that
				// owns it. This prevents the private-router deletion from removing
				// an oauth record that now belongs to the dedicated OAuth CR.
				hasSiblings, err := r.hasSiblingCR(ctx, azPLS)
				if err != nil {
					return fmt.Errorf("failed to check for sibling CRs during base domain cleanup: %w", err)
				}
				if !hasSiblings {
					baseDomainRecords = append(baseDomainRecords, oauthBaseDomainRecordPrefix+clusterName)
				}
			} else {
				baseDomainRecords = append(baseDomainRecords, oauthBaseDomainRecordPrefix+clusterName)
			}

			for _, recordName := range baseDomainRecords {
				log.Info("Deleting base domain A record", "record", recordName, "zone", baseDomain)
				deleteCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
				if _, err := r.RecordSets.Delete(deleteCtx, resourceGroup, baseDomain, armprivatedns.RecordTypeA, recordName, nil); err != nil {
					cancel()
					if !azureutil.IsAzureNotFoundError(err) {
						return fmt.Errorf("failed to delete base domain A record %q: %w", recordName, err)
					}
				}
				cancel()
			}
		}

		// Delete base domain VNet link
		bdLinkName := baseDomainVNetLinkName(azPLS.Name)
		log.Info("Deleting base domain VNet Link", "name", bdLinkName)
		bdLinkCtx, bdLinkCancel := context.WithTimeout(ctx, azureAPITimeout)
		defer bdLinkCancel()
		bdLinkPoller, err := r.VirtualNetworkLinks.BeginDelete(bdLinkCtx, resourceGroup, baseDomain, bdLinkName, nil)
		if err != nil {
			if !azureutil.IsAzureNotFoundError(err) {
				return fmt.Errorf("failed to begin deleting base domain VNet Link: %w", err)
			}
		} else if bdLinkPoller != nil {
			bdLinkPollCtx, bdLinkPollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
			defer bdLinkPollCancel()

			if _, err := bdLinkPoller.PollUntilDone(bdLinkPollCtx, nil); err != nil {
				if !azureutil.IsAzureNotFoundError(err) {
					return fmt.Errorf("failed to delete base domain VNet Link: %w", err)
				}
			}
		}

		// Only delete the base domain DNS zone if no other CRs share it.
		// When multiple CRs (e.g., private-router and oauth-openshift) use the same
		// base domain zone, the zone must not be deleted until the last CR is removed.
		hasSiblings, err := r.hasSiblingCR(ctx, azPLS)
		if err != nil {
			return fmt.Errorf("failed to check for sibling CRs during base domain zone cleanup: %w", err)
		}

		if !hasSiblings {
			log.Info("Deleting base domain Private DNS Zone (last CR using this zone)", "zone", baseDomain)
			bdZoneCtx, bdZoneCancel := context.WithTimeout(ctx, azureAPITimeout)
			defer bdZoneCancel()
			bdZonePoller, err := r.PrivateDNSZones.BeginDelete(bdZoneCtx, resourceGroup, baseDomain, nil)
			if err != nil {
				if !azureutil.IsAzureNotFoundError(err) {
					return fmt.Errorf("failed to begin deleting base domain Private DNS Zone: %w", err)
				}
			} else if bdZonePoller != nil {
				bdZonePollCtx, bdZonePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
				defer bdZonePollCancel()

				if _, err := bdZonePoller.PollUntilDone(bdZonePollCtx, nil); err != nil {
					if !azureutil.IsAzureNotFoundError(err) {
						return fmt.Errorf("failed to delete base domain Private DNS Zone: %w", err)
					}
				}
			}
		} else {
			log.Info("Skipping base domain zone deletion, other CRs still use it", "zone", baseDomain)
		}
	}

	// 5. Delete Private Endpoint
	// Always attempt deletion by deterministic name, even when PrivateEndpointID is empty.
	// If the status was never populated (e.g., status update failed after PE creation),
	// relying solely on PrivateEndpointID would orphan the PE in the customer's subscription.
	peName := privateEndpointName(azPLS.Name)
	log.Info("Deleting Private Endpoint", "name", peName, "hasStatusID", azPLS.Status.PrivateEndpointID != "")
	deleteCtx4, cancel4 := context.WithTimeout(ctx, azureAPITimeout)
	defer cancel4()
	pePoller, err := r.PrivateEndpoints.BeginDelete(deleteCtx4, resourceGroup, peName, nil)
	if err != nil {
		if !azureutil.IsAzureNotFoundError(err) {
			return fmt.Errorf("failed to begin deleting Private Endpoint: %w", err)
		}
	} else if pePoller != nil {
		pePollCtx, pePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer pePollCancel()

		if _, err := pePoller.PollUntilDone(pePollCtx, nil); err != nil {
			if !azureutil.IsAzureNotFoundError(err) {
				return fmt.Errorf("failed to delete Private Endpoint: %w", err)
			}
		}
	}

	return nil
}

// getHostedControlPlane finds the HostedControlPlane that owns this CR.
func (r *AzurePrivateLinkServiceReconciler) getHostedControlPlane(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService) (*hyperv1.HostedControlPlane, error) {
	// Find the HostedControlPlane from the owner reference
	for _, ownerRef := range azPLS.OwnerReferences {
		if ownerRef.Kind == "HostedControlPlane" && ownerRef.APIVersion == hyperv1.GroupVersion.String() {
			hcp := &hyperv1.HostedControlPlane{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: azPLS.Namespace, Name: ownerRef.Name}, hcp); err != nil {
				return nil, fmt.Errorf("failed to get HostedControlPlane %s/%s: %w", azPLS.Namespace, ownerRef.Name, err)
			}
			return hcp, nil
		}
	}
	return nil, fmt.Errorf("no HostedControlPlane owner reference found on AzurePrivateLinkService %s/%s", azPLS.Namespace, azPLS.Name)
}

// extractPrivateEndpointIP extracts the private IP address from a Private Endpoint's
// network interface configuration.
func extractPrivateEndpointIP(pe armnetwork.PrivateEndpoint) string {
	if pe.Properties == nil {
		return ""
	}
	// Try CustomDNSConfigs first (most reliable source)
	for _, config := range pe.Properties.CustomDNSConfigs {
		if config != nil && len(config.IPAddresses) > 0 && config.IPAddresses[0] != nil {
			return *config.IPAddresses[0]
		}
	}
	// Fall back to network interface IP configurations
	for _, nic := range pe.Properties.NetworkInterfaces {
		if nic == nil || nic.Properties == nil {
			continue
		}
		for _, ipConfig := range nic.Properties.IPConfigurations {
			if ipConfig == nil || ipConfig.Properties == nil {
				continue
			}
			if ipConfig.Properties.PrivateIPAddress != nil {
				return *ipConfig.Properties.PrivateIPAddress
			}
		}
	}
	return ""
}

// extractPrivateEndpointConnectionState extracts the connection approval state from
// a Private Endpoint's ManualPrivateLinkServiceConnections. This controller uses
// manual connections (not auto-approved), so the state is found in
// ManualPrivateLinkServiceConnections[0].Properties.PrivateLinkServiceConnectionState.Status.
//
// Possible values returned by Azure:
//   - "Approved": The connection has been approved (either auto-approved or manually).
//   - "Pending": The connection is awaiting approval from the PLS owner.
//   - "Rejected": The connection was explicitly rejected.
//   - "Disconnected": The connection was removed.
//   - "" (empty): The connection state could not be determined (nil properties).
func extractPrivateEndpointConnectionState(pe armnetwork.PrivateEndpoint) string {
	if pe.Properties == nil {
		return ""
	}
	for _, conn := range pe.Properties.ManualPrivateLinkServiceConnections {
		if conn == nil || conn.Properties == nil || conn.Properties.PrivateLinkServiceConnectionState == nil {
			continue
		}
		return ptr.Deref(conn.Properties.PrivateLinkServiceConnectionState.Status, "")
	}
	return ""
}

// privateEndpointName constructs the Private Endpoint resource name from the CR name.
func privateEndpointName(crName string) string {
	return crName + privateEndpointNameSuffix
}

// vnetLinkName constructs the VNet Link resource name from the CR name.
func vnetLinkName(crName string) string {
	return crName + dnsZoneLinkNameSuffix
}

// baseDomainVNetLinkName constructs the base domain VNet Link resource name from the CR name.
func baseDomainVNetLinkName(crName string) string {
	return crName + baseDomainLinkNameSuffix
}
