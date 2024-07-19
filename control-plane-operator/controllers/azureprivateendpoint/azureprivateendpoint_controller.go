package azureprivateendpoint

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azureruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
)

const (
	finalizer     = "hypershift.openshift.io/control-plane-operator-finalizer"
	defaultResync = 10 * time.Minute
)

type AzurePrivateEndpointReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
}

func (r *AzurePrivateEndpointReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.AzurePrivateEndpoint{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewItemExponentialFailureRateLimiter(3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	r.Client = mgr.GetClient()

	return nil
}

func (r *AzurePrivateEndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log, err := logr.FromContext(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("logger not found: %w", err)
	}

	log.Info("reconciling azure private endpoint")

	azurePrivateEndpoint := &hyperv1.AzurePrivateEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(azurePrivateEndpoint), azurePrivateEndpoint); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}

	originalObj := azurePrivateEndpoint.DeepCopy()
	// Fetch the HostedControlPlane
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := r.List(ctx, hcpList, &client.ListOptions{Namespace: req.Namespace}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}
	if len(hcpList.Items) == 0 {
		// remove finalizer and return early if HostedControlPlane is deleted
		if controllerutil.ContainsFinalizer(azurePrivateEndpoint, finalizer) {
			controllerutil.RemoveFinalizer(azurePrivateEndpoint, finalizer)
			if err := r.Patch(ctx, azurePrivateEndpoint, client.MergeFrom(originalObj)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	if len(hcpList.Items) > 1 {
		return ctrl.Result{}, fmt.Errorf("unexpected number of HostedControlPlanes in namespace, expected: 1, actual: %d", len(hcpList.Items))
	}
	hcp := &hcpList.Items[0]

	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// Return early if deleted
	if !azurePrivateEndpoint.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(azurePrivateEndpoint, finalizer) {
			// If we previously removed our finalizer, don't delete again and return early
			return ctrl.Result{}, nil
		}
		if err := r.delete(ctx, azurePrivateEndpoint, hcp); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
		}

		if controllerutil.ContainsFinalizer(azurePrivateEndpoint, finalizer) {
			controllerutil.RemoveFinalizer(azurePrivateEndpoint, finalizer)
			if err := r.Patch(ctx, azurePrivateEndpoint, client.MergeFrom(originalObj)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the azurePrivateEndpoint has a finalizer for cleanup
	if !controllerutil.ContainsFinalizer(azurePrivateEndpoint, finalizer) {
		controllerutil.AddFinalizer(azurePrivateEndpoint, finalizer)
		if err := r.Patch(ctx, azurePrivateEndpoint, client.MergeFrom(originalObj)); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	// Reconcile the AzurePrivateEndpoint
	originalObj = azurePrivateEndpoint.DeepCopy()
	if err := r.reconcileAzurePrivateEndpoint(ctx, azurePrivateEndpoint, hcp); err != nil {
		meta.SetStatusCondition(&azurePrivateEndpoint.Status.Conditions, metav1.Condition{
			Type:    string(hyperv1.AzurePrivateEndpointAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.AzureErrorReason,
			Message: err.Error(),
		})
		if err := r.Status().Patch(ctx, azurePrivateEndpoint, client.MergeFrom(originalObj)); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&azurePrivateEndpoint.Status.Conditions, metav1.Condition{
		Type:    string(hyperv1.AzurePrivateEndpointAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  hyperv1.AsExpectedReason,
		Message: "",
	})
	if err := r.Status().Patch(ctx, azurePrivateEndpoint, client.MergeFrom(originalObj)); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconcilation complete")
	// always requeue to catch and report out of band changes.
	return ctrl.Result{RequeueAfter: defaultResync}, nil
}

func (r *AzurePrivateEndpointReconciler) reconcileAzurePrivateEndpoint(ctx context.Context, azurePrivateEndpoint *hyperv1.AzurePrivateEndpoint, hcp *hyperv1.HostedControlPlane) error {
	// log, err := logr.FromContext(ctx)
	// if err != nil {
	// 	return fmt.Errorf("logger not found: %w", err)
	// }
	if hcp.Spec.Platform.Azure == nil {
		return nil
	}
	subscriptionID := hcp.Spec.Platform.Azure.SubscriptionID

	cred, err := r.getAzureCredential(ctx, hcp)
	if err != nil {
		return err
	}

	endpointClient, err := armnetwork.NewPrivateEndpointsClient(subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	response, err := endpointClient.BeginCreateOrUpdate(ctx, azurePrivateEndpoint.Spec.ResourceGroupName, azurePrivateEndpoint.Name, armnetwork.PrivateEndpoint{
		Location: ptr.To(azurePrivateEndpoint.Spec.Location),
		Properties: &armnetwork.PrivateEndpointProperties{
			PrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
				{
					Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
						PrivateLinkServiceID: ptr.To(azurePrivateEndpoint.Spec.PrivateLinkServiceID),
					},
					Name: ptr.To("hypershift-connection"),
				},
			},
			Subnet: &armnetwork.Subnet{
				ID: ptr.To(azurePrivateEndpoint.Spec.SubnetID),
			},
		},
	}, nil)
	if err != nil {
		return err
	}

	result, err := response.PollUntilDone(ctx, &azureruntime.PollUntilDoneOptions{Frequency: time.Second * 30})
	if err != nil {
		return fmt.Errorf("failed polling for private endpoint creation response: %v", err)
	}
	azurePrivateEndpoint.Status.EndpointID = *result.PrivateEndpoint.ID

	if len(result.Properties.NetworkInterfaces) == 0 {
		return fmt.Errorf("NetworkInterfaces should not be empty")
	}
	networkInterfaceResource, _ := arm.ParseResourceID(*result.Properties.NetworkInterfaces[0].ID)

	netInterfaceClient, err := armnetwork.NewInterfacesClient(subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	networkInterfaceRespo, err := netInterfaceClient.Get(ctx, azurePrivateEndpoint.Spec.ResourceGroupName, networkInterfaceResource.Name, nil)
	if err != nil {
		return err
	}

	privateEndpointIP := networkInterfaceRespo.Properties.IPConfigurations[0].Properties.PrivateIPAddress
	dnsName, err := r.reconcileDNSRecord(ctx, cred, privateEndpointIP, hcp)
	if err != nil {
		return err
	}

	azurePrivateEndpoint.Status.DNSNames = []string{dnsName}
	return nil
}

func (r *AzurePrivateEndpointReconciler) reconcileDNSRecord(ctx context.Context, cred *azidentity.ClientSecretCredential, privateEndpointIP *string, hcp *hyperv1.HostedControlPlane) (string, error) {
	if hcp.Spec.DNS.PrivateZoneID == "" {
		return "", fmt.Errorf("hcp.Spec.DNS.PrivateZoneID is not set")
	}
	privateDNSZoneResource, _ := arm.ParseResourceID(hcp.Spec.DNS.PrivateZoneID)

	dnsRecordsClient, err := armprivatedns.NewRecordSetsClient(hcp.Spec.Platform.Azure.SubscriptionID, cred, nil)
	if err != nil {
		return "", err
	}

	recordSetName := "api"
	_, err = dnsRecordsClient.CreateOrUpdate(ctx, hcp.Spec.Platform.Azure.ResourceGroupName, privateDNSZoneResource.Name,
		armprivatedns.RecordTypeA, recordSetName, armprivatedns.RecordSet{
			Properties: &armprivatedns.RecordSetProperties{
				TTL: ptr.To[int64](3600),
				ARecords: []*armprivatedns.ARecord{
					{
						IPv4Address: privateEndpointIP,
					},
				},
			},
		}, nil)
	if err != nil {
		return "", err
	}

	fqdn := fmt.Sprintf("%s.%s", recordSetName, privateDNSZoneResource.Name)
	return fqdn, nil
}

func (r *AzurePrivateEndpointReconciler) getAzureCredential(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*azidentity.ClientSecretCredential, error) {
	credentialsSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: hcp.Spec.Platform.Azure.Credentials.Name}}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return nil, fmt.Errorf("failed to get azure credentials secret: %v", err)
	}

	tenantID := string(credentialsSecret.Data["AZURE_TENANT_ID"])
	clientID := string(credentialsSecret.Data["AZURE_CLIENT_ID"])
	clientSecret := string(credentialsSecret.Data["AZURE_CLIENT_SECRET"])

	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain azure client credential: %v", err)
	}

	return cred, nil
}

func (r *AzurePrivateEndpointReconciler) delete(ctx context.Context, awsEndpointService *hyperv1.AzurePrivateEndpoint, hcp *hyperv1.HostedControlPlane) error {
	// log, err := logr.FromContext(ctx)
	// if err != nil {
	// 	return fmt.Errorf("logger not found: %w", err)
	// }
	if hcp.Spec.Platform.Azure == nil {
		return nil
	}
	subscriptionID := hcp.Spec.Platform.Azure.SubscriptionID

	cred, err := r.getAzureCredential(ctx, hcp)
	if err != nil {
		return err
	}

	endpointClient, err := armnetwork.NewPrivateEndpointsClient(subscriptionID, cred, nil)
	if err != nil {
		return err
	}

	deleteResponse, err := endpointClient.BeginDelete(ctx, awsEndpointService.Spec.ResourceGroupName, awsEndpointService.Name, nil)
	if err != nil {
		var azureErr *azcore.ResponseError
		if errors.As(err, &azureErr) && azureErr.StatusCode == http.StatusNotFound {
			// already deleted!
			return nil
		}
		return err
	}

	_, err = deleteResponse.PollUntilDone(ctx, &azureruntime.PollUntilDoneOptions{Frequency: time.Second * 30})
	if err != nil {
		return err
	}

	// TODO: cleanup DNS A records
	return nil
}
