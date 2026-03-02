package azureprivatelinkservice

import (
	"context"
	"fmt"
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

	// aRecordName is the record name used within the Private DNS zone.
	// Using "@" targets the zone apex, mapping the KAS hostname directly.
	aRecordName = "@"

	// aRecordTTL is the TTL for A records in seconds.
	aRecordTTL int64 = 300
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
func (r *AzurePrivateLinkServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

	// 2. Handle deletion
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

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(azPLS, azurePrivateLinkServiceFinalizer) {
		controllerutil.AddFinalizer(azPLS, azurePrivateLinkServiceFinalizer)
		return ctrl.Result{}, r.Update(ctx, azPLS)
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

	kasHostname := hcp.Status.ControlPlaneEndpoint.Host
	if kasHostname == "" {
		log.Info("KAS hostname not yet available, waiting")
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	// 6. Reconcile Private Endpoint
	if result, err := r.reconcilePrivateEndpoint(ctx, azPLS, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 7. Reconcile Private DNS Zone, VNet link, and A record
	if result, err := r.reconcileDNS(ctx, azPLS, kasHostname, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 8. Set overall Available condition
	patch := client.MergeFrom(azPLS.DeepCopy())
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AzurePrivateLinkServiceAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AzurePLSSuccessReason,
		Message: "All Azure Private Link Service resources are available",
	})
	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update Available condition: %w", err)
	}

	// 9. Requeue for drift detection
	return ctrl.Result{RequeueAfter: azureutil.DriftDetectionRequeueInterval}, nil
}

// reconcilePrivateEndpoint creates or updates the Private Endpoint in the guest VNet.
func (r *AzurePrivateLinkServiceReconciler) reconcilePrivateEndpoint(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, log logr.Logger) (ctrl.Result, error) {
	resourceGroup := azPLS.Spec.ResourceGroupName
	peName := privateEndpointName(azPLS.Name)
	if err := azureutil.ValidateAzureResourceName(peName, "Private Endpoint"); err != nil {
		return ctrl.Result{}, err
	}

	// Check if PE already exists
	existingPE, err := r.PrivateEndpoints.Get(ctx, resourceGroup, peName, nil)
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
			"hypershift.openshift.io/resource-type": ptr.To("private-endpoint"),
		},
		Properties: &armnetwork.PrivateEndpointProperties{
			Subnet: &armnetwork.Subnet{
				ID: ptr.To(azPLS.Spec.GuestSubnetID),
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

	if connectionState != "" && connectionState != "Approved" {
		meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
			Type:   string(hyperv1.AzurePrivateEndpointAvailable),
			Status: metav1.ConditionFalse,
			Reason: "PrivateEndpointConnectionNotApproved",
			Message: fmt.Sprintf("Private Endpoint connection state is %q, not Approved. "+
				"Ensure the guest subscription is in the PLS auto-approval list, "+
				"or manually approve the connection on the Private Link Service.", connectionState),
		})

		if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
			log.Error(err, "failed to update status with Private Endpoint connection state")
			return ctrl.Result{}, err
		}

		// Requeue to check again; the connection may be approved out-of-band.
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AzurePrivateEndpointAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AzurePLSSuccessReason,
		Message: "Private Endpoint is available",
	})

	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		log.Error(err, "failed to update status with Private Endpoint information")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDNS creates or updates the Private DNS Zone, VNet link, and A record.
func (r *AzurePrivateLinkServiceReconciler) reconcileDNS(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, kasHostname string, log logr.Logger) (ctrl.Result, error) {
	// Ensure PE is available before creating DNS
	if azPLS.Status.PrivateEndpointIP == "" {
		log.Info("Private Endpoint IP not available yet, skipping DNS reconciliation")
		return ctrl.Result{RequeueAfter: azureutil.PLSRequeueInterval}, nil
	}

	resourceGroup := azPLS.Spec.ResourceGroupName
	dnsZoneName := kasHostname

	// 1. Create Private DNS Zone
	log.Info("Reconciling Private DNS Zone", "zone", dnsZoneName)
	zoneParams := armprivatedns.PrivateZone{
		Location: ptr.To("global"),
		Tags: map[string]*string{
			"hypershift.openshift.io/resource-type": ptr.To("private-dns-zone"),
		},
	}

	apiCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
	defer cancel()

	zonePoller, err := r.PrivateDNSZones.BeginCreateOrUpdate(apiCtx, resourceGroup, dnsZoneName, zoneParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), "DNSZoneCreationFailed", fmt.Errorf("failed to begin creating Private DNS Zone: %w", err), log)
	}

	var zoneResp armprivatedns.PrivateZonesClientCreateOrUpdateResponse
	if zonePoller != nil {
		zonePollCtx, zonePollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer zonePollCancel()

		zoneResp, err = zonePoller.PollUntilDone(zonePollCtx, nil)
		if err != nil {
			return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), "DNSZoneCreationFailed", fmt.Errorf("failed to create Private DNS Zone: %w", err), log)
		}
	}
	log.Info("Private DNS Zone reconciled", "id", ptr.Deref(zoneResp.ID, ""))

	// 2. Create VNet Link
	linkName := vnetLinkName(azPLS.Name)
	if err := azureutil.ValidateAzureResourceName(linkName, "VNet Link"); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Reconciling VNet Link", "name", linkName)
	linkParams := armprivatedns.VirtualNetworkLink{
		Location: ptr.To("global"),
		Tags: map[string]*string{
			"hypershift.openshift.io/resource-type": ptr.To("vnet-link"),
		},
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			VirtualNetwork: &armprivatedns.SubResource{
				ID: ptr.To(azPLS.Spec.GuestVNetID),
			},
			RegistrationEnabled: ptr.To(false),
		},
	}

	linkCtx, linkCancel := context.WithTimeout(ctx, azureAPITimeout)
	defer linkCancel()

	linkPoller, err := r.VirtualNetworkLinks.BeginCreateOrUpdate(linkCtx, resourceGroup, dnsZoneName, linkName, linkParams, nil)
	if err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), "VNetLinkCreationFailed", fmt.Errorf("failed to begin creating VNet Link: %w", err), log)
	}

	if linkPoller != nil {
		linkPollCtx, linkPollCancel := context.WithTimeout(ctx, azureutil.PollTimeout)
		defer linkPollCancel()

		if _, err := linkPoller.PollUntilDone(linkPollCtx, nil); err != nil {
			return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), "VNetLinkCreationFailed", fmt.Errorf("failed to create VNet Link: %w", err), log)
		}
	}
	log.Info("VNet Link reconciled", "name", linkName)

	// 3. Create A Record
	log.Info("Reconciling A record", "ip", azPLS.Status.PrivateEndpointIP)
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

	if _, err := r.RecordSets.CreateOrUpdate(ctx, resourceGroup, dnsZoneName, armprivatedns.RecordTypeA, aRecordName, recordParams, nil); err != nil {
		return r.handleAzureError(ctx, azPLS, string(hyperv1.AzurePrivateDNSAvailable), "ARecordCreationFailed", fmt.Errorf("failed to create A record: %w", err), log)
	}
	log.Info("A record reconciled", "ip", azPLS.Status.PrivateEndpointIP)

	// 4. Update status with DNS zone ID, zone name, and set condition
	patch := client.MergeFrom(azPLS.DeepCopy())
	azPLS.Status.PrivateDNSZoneID = ptr.Deref(zoneResp.ID, "")
	azPLS.Status.DNSZoneName = dnsZoneName
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AzurePrivateDNSAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AzurePLSSuccessReason,
		Message: "Private DNS zone and A records are available",
	})
	if err := r.Status().Patch(ctx, azPLS, patch); err != nil {
		log.Error(err, "failed to update status with DNS information")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleAzureError provides differentiated backoff for Azure API errors.
// It delegates to azureutil.ClassifyAzureError for error classification and
// requeue interval selection, then updates the CR status condition.
func (r *AzurePrivateLinkServiceReconciler) handleAzureError(ctx context.Context, azPLS *hyperv1.AzurePrivateLinkService, conditionType string, reason string, err error, log logr.Logger) (ctrl.Result, error) {
	requeueAfter, message := azureutil.ClassifyAzureError(err)
	log.Error(err, message)

	patch := client.MergeFrom(azPLS.DeepCopy())
	meta.SetStatusCondition(&azPLS.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
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
		log.Info("Deleting A record", "zone", dnsZoneName)
		deleteCtx, cancel := context.WithTimeout(ctx, azureAPITimeout)
		defer cancel()

		if _, err := r.RecordSets.Delete(deleteCtx, resourceGroup, dnsZoneName, armprivatedns.RecordTypeA, aRecordName, nil); err != nil {
			if !azureutil.IsAzureNotFoundError(err) {
				return fmt.Errorf("failed to delete A record: %w", err)
			}
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

	// 4. Delete Private Endpoint
	peName := privateEndpointName(azPLS.Name)
	if azPLS.Status.PrivateEndpointID != "" {
		log.Info("Deleting Private Endpoint", "name", peName)
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
