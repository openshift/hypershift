package azure

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	supportutil "github.com/openshift/hypershift/support/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr/testr"
)

// mockPrivateLinkServicesAPI implements PrivateLinkServicesAPI for testing
type mockPrivateLinkServicesAPI struct {
	getResponse armnetwork.PrivateLinkServicesClientGetResponse
	getErr      error
	createErr   error
	deleteErr   error
	updatePEErr error

	createCalled     bool
	deleteCalled     bool
	updatePECalled   bool
	updatePECallArgs []string // tracks the PE connection names that were rejected
	lastName         string
	lastCreateParams armnetwork.PrivateLinkService
}

func (m *mockPrivateLinkServicesAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, serviceName string, parameters armnetwork.PrivateLinkService, _ *armnetwork.PrivateLinkServicesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateLinkServicesClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastName = serviceName
	m.lastCreateParams = parameters
	if m.createErr != nil {
		return nil, m.createErr
	}
	// Return nil poller -- tests that need a real poller should be structured differently.
	// For our unit tests, the create path is validated via the createErr path or status checks.
	return nil, nil
}

func (m *mockPrivateLinkServicesAPI) BeginDelete(_ context.Context, resourceGroupName string, serviceName string, _ *armnetwork.PrivateLinkServicesClientBeginDeleteOptions) (*azruntime.Poller[armnetwork.PrivateLinkServicesClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastName = serviceName
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return nil, nil
}

func (m *mockPrivateLinkServicesAPI) Get(_ context.Context, resourceGroupName string, serviceName string, _ *armnetwork.PrivateLinkServicesClientGetOptions) (armnetwork.PrivateLinkServicesClientGetResponse, error) {
	m.lastName = serviceName
	return m.getResponse, m.getErr
}

func (m *mockPrivateLinkServicesAPI) UpdatePrivateEndpointConnection(_ context.Context, resourceGroupName string, serviceName string, peConnectionName string, parameters armnetwork.PrivateEndpointConnection, _ *armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionOptions) (armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionResponse, error) {
	m.updatePECalled = true
	m.updatePECallArgs = append(m.updatePECallArgs, peConnectionName)
	if m.updatePEErr != nil {
		return armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionResponse{}, m.updatePEErr
	}
	return armnetwork.PrivateLinkServicesClientUpdatePrivateEndpointConnectionResponse{
		PrivateEndpointConnection: armnetwork.PrivateEndpointConnection{
			Name: parameters.Name,
			Properties: &armnetwork.PrivateEndpointConnectionProperties{
				PrivateLinkServiceConnectionState: parameters.Properties.PrivateLinkServiceConnectionState,
			},
		},
	}, nil
}

// mockLoadBalancersAPI implements LoadBalancersAPI for testing
type mockLoadBalancersAPI struct {
	loadBalancers []*armnetwork.LoadBalancer
}

func (m *mockLoadBalancersAPI) NewListPager(_ string, _ *armnetwork.LoadBalancersClientListOptions) *azruntime.Pager[armnetwork.LoadBalancersClientListResponse] {
	return azruntime.NewPager(azruntime.PagingHandler[armnetwork.LoadBalancersClientListResponse]{
		More: func(page armnetwork.LoadBalancersClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, page *armnetwork.LoadBalancersClientListResponse) (armnetwork.LoadBalancersClientListResponse, error) {
			return armnetwork.LoadBalancersClientListResponse{
				LoadBalancerListResult: armnetwork.LoadBalancerListResult{
					Value: m.loadBalancers,
				},
			}, nil
		},
	})
}

func newAzureNotFoundError() error {
	return &azcore.ResponseError{
		StatusCode: http.StatusNotFound,
	}
}

func TestReconcile_WhenCRDoesNotExist_ItShouldReturnEmptyResult(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

	r := &AzurePrivateLinkServiceController{
		Client: c,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}
}

func TestReconcile_WhenHostedClusterIsPaused_ItShouldRequeueAfterPauseExpiry(t *testing.T) {
	pausedUntil := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			PausedUntil: &pausedUntil,
		},
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:                 "10.0.0.1",
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceController{
		Client: c,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.RequeueAfter <= 0 {
		t.Error("expected positive RequeueAfter duration for paused reconciliation")
	}
}

func TestReconcile_WhenLoadBalancerIPIsSet_ItShouldCreateAPLS(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID: "12345678-abcd-1234-abcd-123456789012",
		},
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "private-router",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:                 "10.0.0.1",
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	plsMock := &mockPrivateLinkServicesAPI{
		// PLS does not exist yet
		getErr: newAzureNotFoundError(),
	}

	lbMock := &mockLoadBalancersAPI{
		loadBalancers: []*armnetwork.LoadBalancer{
			{
				ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/loadBalancers/test-ilb"),
				Properties: &armnetwork.LoadBalancerPropertiesFormat{
					FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
						{
							ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/loadBalancers/test-ilb/frontendIPConfigurations/fe-config"),
							Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
								PrivateIPAddress: to.Ptr("10.0.0.1"),
							},
						},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           lbMock,
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "private-router",
			Namespace: "test-ns",
		},
	}

	// The mock returns a nil poller from BeginCreateOrUpdate.
	// The controller handles this by requeueing after 30 seconds.
	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !plsMock.createCalled {
		t.Error("expected BeginCreateOrUpdate to be called")
	}

	expectedPLSName := "pls-12345678-abcd-1234-abcd-123456789012"
	if plsMock.lastName != expectedPLSName {
		t.Errorf("expected PLS name %q, got %q", expectedPLSName, plsMock.lastName)
	}

	if result.RequeueAfter != 30*time.Second {
		t.Errorf("expected RequeueAfter 30s for nil poller, got %v", result.RequeueAfter)
	}
}

func TestReconcile_WhenPLSAlreadyExists_ItShouldUpdateStatus(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID: "12345678-abcd-1234-abcd-123456789012",
		},
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:                 "10.0.0.1",
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	plsResourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-12345678-abcd-1234-abcd-123456789012"
	plsAlias := "pls-12345678-abcd-1234-abcd-123456789012.abc123.eastus.azure.privatelinkservice"

	plsMock := &mockPrivateLinkServicesAPI{
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				ID:       to.Ptr(plsResourceID),
				Location: to.Ptr("eastus"),
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Alias: to.Ptr(plsAlias),
					LoadBalancerFrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
						{
							ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/loadBalancers/test-ilb/frontendIPConfigurations/fe-config"),
						},
					},
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
					AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
				},
			},
		},
	}

	lbMock := &mockLoadBalancersAPI{}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           lbMock,
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.RequeueAfter != azureutil.DriftDetectionRequeueInterval {
		t.Errorf("expected RequeueAfter=%v for drift detection, got RequeueAfter=%v", azureutil.DriftDetectionRequeueInterval, result.RequeueAfter)
	}

	// Verify status was updated
	updated := &hyperv1.AzurePrivateLinkService{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated); err != nil {
		t.Fatalf("failed to get updated AzurePrivateLinkService: %v", err)
	}

	if updated.Status.PrivateLinkServiceID != plsResourceID {
		t.Errorf("expected PrivateLinkServiceID %q, got %q", plsResourceID, updated.Status.PrivateLinkServiceID)
	}

	if updated.Status.PrivateLinkServiceAlias != plsAlias {
		t.Errorf("expected PrivateLinkServiceAlias %q, got %q", plsAlias, updated.Status.PrivateLinkServiceAlias)
	}

	// Verify condition was set
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == string(hyperv1.AzurePLSCreated) {
			found = true
			if cond.Status != metav1.ConditionTrue {
				t.Errorf("expected AzurePLSCreated condition to be True, got %s", cond.Status)
			}
		}
	}
	if !found {
		t.Error("expected AzurePLSCreated condition to be set")
	}
}

func TestReconcile_WhenCRIsDeleted_ItShouldDeleteThePLS(t *testing.T) {
	now := metav1.Now()

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pls",
			Namespace:         "test-ns",
			Finalizers:        []string{azurePLSFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
		Status: hyperv1.AzurePrivateLinkServiceStatus{
			PrivateLinkServiceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-test-cluster-id",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	plsMock := &mockPrivateLinkServicesAPI{
		// Get returns PLS with no PE connections (PE already cleaned up)
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{},
				},
			},
		},
		// Delete returns not found (already deleted scenario)
		deleteErr: newAzureNotFoundError(),
	}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           &mockLoadBalancersAPI{},
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}

	if !plsMock.deleteCalled {
		t.Error("expected BeginDelete to be called")
	}

	if plsMock.lastName != "pls-test-cluster-id" {
		t.Errorf("expected PLS name 'pls-test-cluster-id', got '%s'", plsMock.lastName)
	}

	// After the finalizer is removed from an object with a DeletionTimestamp,
	// the fake client deletes it. Verify the object no longer exists.
	updated := &hyperv1.AzurePrivateLinkService{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	if err == nil {
		for _, f := range updated.Finalizers {
			if f == azurePLSFinalizer {
				t.Error("expected finalizer to be removed after deletion")
			}
		}
	}
}

func TestReconcile_WhenCRIsDeleted_AndDeleteSucceeds_ItShouldRemoveFinalizer(t *testing.T) {
	now := metav1.Now()

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-pls",
			Namespace:         "test-ns",
			Finalizers:        []string{azurePLSFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
		Status: hyperv1.AzurePrivateLinkServiceStatus{
			PrivateLinkServiceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-test-cluster-id",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	plsMock := &mockPrivateLinkServicesAPI{
		// Get returns PLS with no PE connections
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{},
				},
			},
		},
		// Delete succeeds, returns nil poller
	}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           &mockLoadBalancersAPI{},
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}

	if !plsMock.deleteCalled {
		t.Error("expected BeginDelete to be called")
	}

	if plsMock.lastName != "pls-test-cluster-id" {
		t.Errorf("expected PLS name 'pls-test-cluster-id', got '%s'", plsMock.lastName)
	}

	// After the finalizer is removed from an object with a DeletionTimestamp,
	// the fake client deletes it. Verify the object no longer exists.
	updated := &hyperv1.AzurePrivateLinkService{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	if err == nil {
		for _, f := range updated.Finalizers {
			if f == azurePLSFinalizer {
				t.Error("expected finalizer to be removed after deletion")
			}
		}
	}
}

func TestReconcile_WhenLoadBalancerIPNotSet_ItShouldRequeue(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			// LoadBalancerIP intentionally left empty
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceController{
		Client: c,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if result.RequeueAfter != 30*time.Second {
		t.Errorf("expected RequeueAfter 30s, got %v", result.RequeueAfter)
	}
}

func TestConstructPLSName(t *testing.T) {
	tests := []struct {
		name        string
		clusterID   string
		serviceName string
		expected    string
	}{
		{
			name:        "When given private-router service, it should use legacy pls-<clusterID> format",
			clusterID:   "12345678-abcd-1234-abcd-123456789012",
			serviceName: "private-router",
			expected:    "pls-12345678-abcd-1234-abcd-123456789012",
		},
		{
			name:        "When given oauth-openshift service, it should use pls-<serviceName>-<clusterID> format",
			clusterID:   "12345678-abcd-1234-abcd-123456789012",
			serviceName: "oauth-openshift",
			expected:    "pls-oauth-openshift-12345678-abcd-1234-abcd-123456789012",
		},
		{
			name:        "When given a short cluster ID with private-router, it should construct valid legacy PLS name",
			clusterID:   "abc",
			serviceName: "private-router",
			expected:    "pls-abc",
		},
		{
			name:        "When given a short cluster ID with a non-private-router service, it should include service name",
			clusterID:   "abc",
			serviceName: "my-service",
			expected:    "pls-my-service-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := constructPLSName(tt.clusterID, tt.serviceName)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestIsAzureNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is Azure 404, it should return true",
			err:      &azcore.ResponseError{StatusCode: 404},
			expected: true,
		},
		{
			name:     "When error is Azure 400, it should return false",
			err:      &azcore.ResponseError{StatusCode: 400},
			expected: false,
		},
		{
			name:     "When error is non-Azure error, it should return false",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "When error is nil, it should return false",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := azureutil.IsAzureNotFoundError(tt.err)
			if actual != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, actual)
			}
		})
	}
}

func TestLookupILBByFrontendIP(t *testing.T) {
	tests := []struct {
		name             string
		loadBalancerIP   string
		loadBalancers    []*armnetwork.LoadBalancer
		expectedILBID    string
		expectedFIPID    string
		expectedSubnetID string
		expectedErr      bool
	}{
		{
			name:           "When ILB exists with matching frontend IP, it should return ILB ID and frontend config ID",
			loadBalancerIP: "10.0.0.5",
			loadBalancers: []*armnetwork.LoadBalancer{
				{
					ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1"),
					Properties: &armnetwork.LoadBalancerPropertiesFormat{
						FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
							{
								ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1/frontendIPConfigurations/fe1"),
								Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
									PrivateIPAddress: to.Ptr("10.0.0.5"),
									Subnet: &armnetwork.Subnet{
										ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/default"),
									},
								},
							},
						},
					},
				},
			},
			expectedILBID:    "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1",
			expectedFIPID:    "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1/frontendIPConfigurations/fe1",
			expectedSubnetID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/default",
		},
		{
			name:           "When no ILB matches the frontend IP, it should return empty strings",
			loadBalancerIP: "10.0.0.99",
			loadBalancers: []*armnetwork.LoadBalancer{
				{
					ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1"),
					Properties: &armnetwork.LoadBalancerPropertiesFormat{
						FrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
							{
								ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1/frontendIPConfigurations/fe1"),
								Properties: &armnetwork.FrontendIPConfigurationPropertiesFormat{
									PrivateIPAddress: to.Ptr("10.0.0.5"),
								},
							},
						},
					},
				},
			},
			expectedILBID: "",
			expectedFIPID: "",
		},
		{
			name:           "When LB has nil properties, it should skip and return empty",
			loadBalancerIP: "10.0.0.1",
			loadBalancers: []*armnetwork.LoadBalancer{
				{
					ID:         to.Ptr("lb-id"),
					Properties: nil,
				},
			},
			expectedILBID: "",
			expectedFIPID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lbMock := &mockLoadBalancersAPI{
				loadBalancers: tt.loadBalancers,
			}

			r := &AzurePrivateLinkServiceController{
				LoadBalancers:           lbMock,
				ManagementResourceGroup: "rg-test",
			}

			azPLS := &hyperv1.AzurePrivateLinkService{
				Spec: hyperv1.AzurePrivateLinkServiceSpec{
					LoadBalancerIP:    tt.loadBalancerIP,
					ResourceGroupName: "rg-test",
				},
			}

			ilbID, fipID, subnetID, err := r.lookupILBByFrontendIP(context.Background(), azPLS)
			if tt.expectedErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectedErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if ilbID != tt.expectedILBID {
				t.Errorf("expected ILB ID %q, got %q", tt.expectedILBID, ilbID)
			}
			if fipID != tt.expectedFIPID {
				t.Errorf("expected frontend IP config ID %q, got %q", tt.expectedFIPID, fipID)
			}
			if subnetID != tt.expectedSubnetID {
				t.Errorf("expected subnet ID %q, got %q", tt.expectedSubnetID, subnetID)
			}
		})
	}
}

func TestHandleAzureError(t *testing.T) {
	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceController{
		Client: c,
	}

	tests := []struct {
		name            string
		err             error
		expectedRequeue time.Duration
	}{
		{
			name:            "When Azure returns 429 without Retry-After, it should requeue after 5 minutes",
			err:             &azcore.ResponseError{StatusCode: 429},
			expectedRequeue: 5 * time.Minute,
		},
		{
			name: "When Azure returns 429 with Retry-After header, it should use the header value",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"120"},
					},
				},
			},
			expectedRequeue: 120 * time.Second,
		},
		{
			name:            "When Azure returns 403, it should requeue after 10 minutes",
			err:             &azcore.ResponseError{StatusCode: 403},
			expectedRequeue: 10 * time.Minute,
		},
		{
			name:            "When Azure returns 409, it should requeue after 30 seconds",
			err:             &azcore.ResponseError{StatusCode: 409},
			expectedRequeue: 30 * time.Second,
		},
		{
			name:            "When a non-Azure error occurs, it should requeue after 2 minutes",
			err:             fmt.Errorf("unexpected error"),
			expectedRequeue: 2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := r.handleAzureError(log.IntoContext(context.Background(), testr.New(t)), azPLS.DeepCopy(), "TestReason", tt.err)
			if result.RequeueAfter != tt.expectedRequeue {
				t.Errorf("expected RequeueAfter %v, got %v", tt.expectedRequeue, result.RequeueAfter)
			}
		})
	}
}

func TestReconcile_WhenAdditionalAllowedSubscriptionsChange_ItShouldUpdatePLS(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID: "12345678-abcd-1234-abcd-123456789012",
		},
	}

	// CR spec has two subscriptions, but existing PLS only has one
	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:                 "10.0.0.1",
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456", "sub-789"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	plsResourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-12345678-abcd-1234-abcd-123456789012"
	plsAlias := "pls-12345678-abcd-1234-abcd-123456789012.abc123.eastus.azure.privatelinkservice"

	plsMock := &mockPrivateLinkServicesAPI{
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				ID:       to.Ptr(plsResourceID),
				Location: to.Ptr("eastus"),
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Alias: to.Ptr(plsAlias),
					LoadBalancerFrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
						{
							ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/loadBalancers/test-ilb/frontendIPConfigurations/fe-config"),
						},
					},
					// Existing PLS only has sub-456; spec wants sub-456 + sub-789
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
					AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           &mockLoadBalancersAPI{},
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	g := NewWithT(t)

	// BeginCreateOrUpdate should be called to update visibility/auto-approval
	g.Expect(plsMock.createCalled).To(BeTrue(), "expected BeginCreateOrUpdate to be called for subscription update")

	// Verify the PLS was updated with the correct visibility and auto-approval subscriptions.
	// The desired subscriptions are: the CR's SubscriptionID (sub-123) + AdditionalAllowed (sub-456, sub-789).
	g.Expect(plsMock.lastCreateParams.Properties).ToNot(BeNil())
	g.Expect(plsMock.lastCreateParams.Properties.Visibility).ToNot(BeNil())
	g.Expect(plsMock.lastCreateParams.Properties.AutoApproval).ToNot(BeNil())

	visibilitySubs := derefStringSlice(plsMock.lastCreateParams.Properties.Visibility.Subscriptions)
	autoApprovalSubs := derefStringSlice(plsMock.lastCreateParams.Properties.AutoApproval.Subscriptions)

	// buildAllowedSubscriptions derives the list from the guest subnet's subscription (sub-456)
	// plus AdditionalAllowedSubscriptions (sub-456, sub-789), deduplicating sub-456.
	g.Expect(visibilitySubs).To(ConsistOf("sub-456", "sub-789"),
		"visibility subscriptions should include guest subscription + additional allowed subscriptions")
	g.Expect(autoApprovalSubs).To(ConsistOf("sub-456", "sub-789"),
		"auto-approval subscriptions should match visibility subscriptions")

	// Nil poller means requeue after PLSRequeueInterval
	g.Expect(result.RequeueAfter).To(Equal(azureutil.PLSRequeueInterval))
}

// derefStringSlice dereferences a slice of string pointers.
func derefStringSlice(ptrs []*string) []string {
	out := make([]string, 0, len(ptrs))
	for _, p := range ptrs {
		if p != nil {
			out = append(out, *p)
		}
	}
	return out
}

func TestReconcile_WhenAdditionalAllowedSubscriptionsMatch_ItShouldNotUpdatePLS(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			ClusterID: "12345678-abcd-1234-abcd-123456789012",
		},
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:                 "10.0.0.1",
			SubscriptionID:                 "sub-123",
			ResourceGroupName:              "rg-test",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"sub-456"},
			GuestSubnetID:                  "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(azPLS, hc).
		WithStatusSubresource(azPLS).
		Build()

	plsResourceID := "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-12345678-abcd-1234-abcd-123456789012"
	plsAlias := "pls-12345678-abcd-1234-abcd-123456789012.abc123.eastus.azure.privatelinkservice"

	plsMock := &mockPrivateLinkServicesAPI{
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				ID:       to.Ptr(plsResourceID),
				Location: to.Ptr("eastus"),
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Alias: to.Ptr(plsAlias),
					LoadBalancerFrontendIPConfigurations: []*armnetwork.FrontendIPConfiguration{
						{
							ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/loadBalancers/test-ilb/frontendIPConfigurations/fe-config"),
						},
					},
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
					AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
						Subscriptions: []*string{to.Ptr("sub-456")},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceController{
		Client:                  c,
		PrivateLinkServices:     plsMock,
		LoadBalancers:           &mockLoadBalancersAPI{},
		ManagementResourceGroup: "rg-test",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
	}

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// BeginCreateOrUpdate should NOT be called when subscriptions match
	if plsMock.createCalled {
		t.Error("expected BeginCreateOrUpdate NOT to be called when subscriptions match")
	}

	if result.RequeueAfter != azureutil.DriftDetectionRequeueInterval {
		t.Errorf("expected RequeueAfter=%v for drift detection, got %v", azureutil.DriftDetectionRequeueInterval, result.RequeueAfter)
	}
}

func TestPlsSubscriptionsDrifted(t *testing.T) {
	r := &AzurePrivateLinkServiceController{}

	tests := []struct {
		name     string
		pls      armnetwork.PrivateLinkService
		desired  []string
		expected bool
	}{
		{
			name: "When subscriptions match, it should return false",
			pls: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-123")},
					},
					AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
						Subscriptions: []*string{to.Ptr("sub-123")},
					},
				},
			},
			desired:  []string{"sub-123"},
			expected: false,
		},
		{
			name: "When subscriptions match in different order, it should return false",
			pls: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-456"), to.Ptr("sub-123")},
					},
					AutoApproval: &armnetwork.PrivateLinkServicePropertiesAutoApproval{
						Subscriptions: []*string{to.Ptr("sub-456"), to.Ptr("sub-123")},
					},
				},
			},
			desired:  []string{"sub-123", "sub-456"},
			expected: false,
		},
		{
			name: "When a subscription is added, it should return true",
			pls: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-123")},
					},
				},
			},
			desired:  []string{"sub-123", "sub-456"},
			expected: true,
		},
		{
			name: "When a subscription is removed, it should return true",
			pls: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Visibility: &armnetwork.PrivateLinkServicePropertiesVisibility{
						Subscriptions: []*string{to.Ptr("sub-123"), to.Ptr("sub-456")},
					},
				},
			},
			desired:  []string{"sub-123"},
			expected: true,
		},
		{
			name: "When PLS has nil properties, it should return true if desired is non-empty",
			pls: armnetwork.PrivateLinkService{
				Properties: nil,
			},
			desired:  []string{"sub-123"},
			expected: true,
		},
		{
			name: "When PLS has nil visibility, it should return true if desired is non-empty",
			pls: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					Visibility: nil,
				},
			},
			desired:  []string{"sub-123"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.plsSubscriptionsDrifted(tt.pls, tt.desired)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRejectPrivateEndpointConnections(t *testing.T) {
	tests := []struct {
		name                 string
		getResponse          armnetwork.PrivateLinkServicesClientGetResponse
		getErr               error
		updatePEErr          error
		expectedRejected     int
		expectedErr          bool
		expectedUpdateCalled bool
		expectedUpdateArgs   []string
	}{
		{
			name: "When PLS has approved PE connections, it should reject them",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-1"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Approved"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     1,
			expectedUpdateCalled: true,
			expectedUpdateArgs:   []string{"pe-conn-1"},
		},
		{
			name: "When PLS has pending PE connections, it should reject them",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-pending"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Pending"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     1,
			expectedUpdateCalled: true,
			expectedUpdateArgs:   []string{"pe-conn-pending"},
		},
		{
			name: "When PLS has multiple active PE connections, it should reject all of them",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-1"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Approved"),
									},
								},
							},
							{
								Name: to.Ptr("pe-conn-2"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Pending"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     2,
			expectedUpdateCalled: true,
			expectedUpdateArgs:   []string{"pe-conn-1", "pe-conn-2"},
		},
		{
			name: "When PLS has already rejected PE connections, it should skip them",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-rejected"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Rejected"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     0,
			expectedUpdateCalled: false,
		},
		{
			name: "When PLS has mixed PE connection states, it should only reject active ones",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-approved"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Approved"),
									},
								},
							},
							{
								Name: to.Ptr("pe-conn-rejected"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Rejected"),
									},
								},
							},
							{
								Name: to.Ptr("pe-conn-disconnected"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Disconnected"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     1,
			expectedUpdateCalled: true,
			expectedUpdateArgs:   []string{"pe-conn-approved"},
		},
		{
			name: "When PLS has no PE connections, it should return 0",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{},
					},
				},
			},
			expectedRejected:     0,
			expectedUpdateCalled: false,
		},
		{
			name: "When PLS has nil properties, it should return 0",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: nil,
				},
			},
			expectedRejected:     0,
			expectedUpdateCalled: false,
		},
		{
			name:             "When PLS is not found, it should return 0 without error",
			getErr:           newAzureNotFoundError(),
			expectedRejected: 0,
			expectedErr:      false,
		},
		{
			name:             "When Get fails with a non-404 error, it should return an error",
			getErr:           errors.New("internal server error"),
			expectedRejected: 0,
			expectedErr:      true,
		},
		{
			name: "When UpdatePrivateEndpointConnection fails, it should return an error",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-1"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Approved"),
									},
								},
							},
						},
					},
				},
			},
			updatePEErr:          errors.New("update failed"),
			expectedRejected:     0,
			expectedErr:          true,
			expectedUpdateCalled: true,
		},
		{
			name: "When PE connection has nil Name, it should be skipped",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: nil,
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
										Status: to.Ptr("Approved"),
									},
								},
							},
						},
					},
				},
			},
			expectedRejected:     0,
			expectedUpdateCalled: false,
		},
		{
			name: "When PE connection has nil connection state, it should be skipped",
			getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
				PrivateLinkService: armnetwork.PrivateLinkService{
					Properties: &armnetwork.PrivateLinkServiceProperties{
						PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
							{
								Name: to.Ptr("pe-conn-no-state"),
								Properties: &armnetwork.PrivateEndpointConnectionProperties{
									PrivateLinkServiceConnectionState: nil,
								},
							},
						},
					},
				},
			},
			expectedRejected:     0,
			expectedUpdateCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plsMock := &mockPrivateLinkServicesAPI{
				getResponse: tt.getResponse,
				getErr:      tt.getErr,
				updatePEErr: tt.updatePEErr,
			}

			r := &AzurePrivateLinkServiceController{
				PrivateLinkServices: plsMock,
			}

			rejected, err := r.rejectPrivateEndpointConnections(
				log.IntoContext(context.Background(), testr.New(t)),
				"rg-test",
				"pls-test",
			)

			if tt.expectedErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectedErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if rejected != tt.expectedRejected {
				t.Errorf("expected %d rejected connections, got %d", tt.expectedRejected, rejected)
			}
			if tt.expectedUpdateCalled != plsMock.updatePECalled {
				t.Errorf("expected updatePECalled=%v, got %v", tt.expectedUpdateCalled, plsMock.updatePECalled)
			}
			if tt.expectedUpdateArgs != nil {
				if len(plsMock.updatePECallArgs) != len(tt.expectedUpdateArgs) {
					t.Errorf("expected %d update calls, got %d", len(tt.expectedUpdateArgs), len(plsMock.updatePECallArgs))
				} else {
					for i, expected := range tt.expectedUpdateArgs {
						if plsMock.updatePECallArgs[i] != expected {
							t.Errorf("expected update call %d to have PE name %q, got %q", i, expected, plsMock.updatePECallArgs[i])
						}
					}
				}
			}
		})
	}
}

func TestDelete_WhenPEConnectionsExist_ItShouldRejectAndRequeue(t *testing.T) {
	plsMock := &mockPrivateLinkServicesAPI{
		// Get returns PLS with an approved PE connection
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{
						{
							Name: to.Ptr("pe-conn-1"),
							Properties: &armnetwork.PrivateEndpointConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: to.Ptr("Approved"),
								},
							},
						},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceController{
		PrivateLinkServices:     plsMock,
		ManagementResourceGroup: "rg-test",
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
		Status: hyperv1.AzurePrivateLinkServiceStatus{
			PrivateLinkServiceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-test",
		},
	}

	completed, err := r.delete(log.IntoContext(context.Background(), testr.New(t)), azPLS)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if completed {
		t.Error("expected deletion to NOT be completed after rejecting PE connections")
	}
	if !plsMock.updatePECalled {
		t.Error("expected UpdatePrivateEndpointConnection to be called")
	}
	if plsMock.deleteCalled {
		t.Error("expected BeginDelete NOT to be called when PE connections were rejected")
	}
}

func TestDelete_WhenNoPEConnections_ItShouldProceedToDelete(t *testing.T) {
	plsMock := &mockPrivateLinkServicesAPI{
		// Get returns PLS with no PE connections
		getResponse: armnetwork.PrivateLinkServicesClientGetResponse{
			PrivateLinkService: armnetwork.PrivateLinkService{
				Properties: &armnetwork.PrivateLinkServiceProperties{
					PrivateEndpointConnections: []*armnetwork.PrivateEndpointConnection{},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceController{
		PrivateLinkServices:     plsMock,
		ManagementResourceGroup: "rg-test",
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
		Status: hyperv1.AzurePrivateLinkServiceStatus{
			PrivateLinkServiceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-test",
		},
	}

	completed, err := r.delete(log.IntoContext(context.Background(), testr.New(t)), azPLS)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !completed {
		t.Error("expected deletion to be completed")
	}
	if !plsMock.deleteCalled {
		t.Error("expected BeginDelete to be called")
	}
	if plsMock.updatePECalled {
		t.Error("expected UpdatePrivateEndpointConnection NOT to be called when no PE connections exist")
	}
}

func TestDelete_WhenPLSAlreadyDeleted_ItShouldReturnCompleted(t *testing.T) {
	plsMock := &mockPrivateLinkServicesAPI{
		// Get returns not found -- PLS already deleted
		getErr: newAzureNotFoundError(),
	}

	r := &AzurePrivateLinkServiceController{
		PrivateLinkServices:     plsMock,
		ManagementResourceGroup: "rg-test",
	}

	azPLS := &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pls",
			Namespace: "test-ns",
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			ResourceGroupName: "rg-test",
		},
		Status: hyperv1.AzurePrivateLinkServiceStatus{
			PrivateLinkServiceID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/privateLinkServices/pls-test",
		},
	}

	completed, err := r.delete(log.IntoContext(context.Background(), testr.New(t)), azPLS)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// When the PLS is not found during the Get for PE rejection, the delete should
	// proceed to BeginDelete which also returns not found, completing the deletion.
	// Since rejectPrivateEndpointConnections returns (0, nil) for not-found PLS,
	// the flow continues to BeginDelete.
	if plsMock.updatePECalled {
		t.Error("expected UpdatePrivateEndpointConnection NOT to be called when PLS is not found")
	}
	// BeginDelete should still be called because the PE rejection step treats 404 as "nothing to reject"
	if !plsMock.deleteCalled {
		// The mock's getErr is set to not-found for the PE rejection Get,
		// but the mock also needs a deleteErr for the delete Get.
		// Actually, the mock's getErr applies to ALL Get calls.
		// In the delete flow, Get is called once (for PE rejection).
		// Then BeginDelete is called. But BeginDelete uses the deleteErr field.
		// So deleteCalled should be true and delete should succeed (nil error, nil poller = completed).
		t.Error("expected BeginDelete to be called")
	}
	if !completed {
		t.Error("expected deletion to be completed")
	}
}

// mockSubnetsAPI implements SubnetsAPI for testing
type mockSubnetsAPI struct {
	getResponse armnetwork.SubnetsClientGetResponse
	getErr      error
	createErr   error
	subnets     []*armnetwork.Subnet
}

func (m *mockSubnetsAPI) Get(_ context.Context, _ string, _ string, _ string, _ *armnetwork.SubnetsClientGetOptions) (armnetwork.SubnetsClientGetResponse, error) {
	return m.getResponse, m.getErr
}

func (m *mockSubnetsAPI) BeginCreateOrUpdate(_ context.Context, _ string, _ string, _ string, _ armnetwork.Subnet, _ *armnetwork.SubnetsClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.SubnetsClientCreateOrUpdateResponse], error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return nil, nil
}

func (m *mockSubnetsAPI) NewListPager(_ string, _ string, _ *armnetwork.SubnetsClientListOptions) *azruntime.Pager[armnetwork.SubnetsClientListResponse] {
	return azruntime.NewPager(azruntime.PagingHandler[armnetwork.SubnetsClientListResponse]{
		More: func(page armnetwork.SubnetsClientListResponse) bool {
			return false
		},
		Fetcher: func(ctx context.Context, page *armnetwork.SubnetsClientListResponse) (armnetwork.SubnetsClientListResponse, error) {
			return armnetwork.SubnetsClientListResponse{
				SubnetListResult: armnetwork.SubnetListResult{
					Value: m.subnets,
				},
			}, nil
		},
	})
}

func TestEnsureNATSubnet(t *testing.T) {
	const (
		testILBSubnetID = "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/worker-subnet"
		testInfraID     = "test-infra"
		testSubnetName  = testInfraID + "-pls-nat"
	)

	testHC := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedClusterSpec{
			InfraID: testInfraID,
		},
	}

	tests := []struct {
		name        string
		subnetsMock *mockSubnetsAPI
		expectedID  string
		expectErr   bool
		errContains string
	}{
		{
			name: "When NAT subnet already exists, it should return existing subnet ID",
			subnetsMock: &mockSubnetsAPI{
				getResponse: armnetwork.SubnetsClientGetResponse{
					Subnet: armnetwork.Subnet{
						ID: to.Ptr("/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/" + testSubnetName),
					},
				},
			},
			expectedID: "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet-test/subnets/" + testSubnetName,
			expectErr:  false,
		},
		{
			name: "When Get returns a non-404 error, it should return the error",
			subnetsMock: &mockSubnetsAPI{
				getErr: fmt.Errorf("unexpected Azure API error"),
			},
			expectErr:   true,
			errContains: "failed to check for existing NAT subnet",
		},
		{
			name: "When subnet does not exist and BeginCreateOrUpdate fails, it should return the error",
			subnetsMock: &mockSubnetsAPI{
				getErr:    newAzureNotFoundError(),
				createErr: fmt.Errorf("insufficient permissions"),
				subnets:   []*armnetwork.Subnet{},
			},
			expectErr:   true,
			errContains: "failed to create NAT subnet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &AzurePrivateLinkServiceController{
				Subnets: tt.subnetsMock,
			}

			subnetID, err := r.ensureNATSubnet(context.Background(), testHC, testILBSubnetID)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errContains))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(subnetID).To(Equal(tt.expectedID))
			}
		})
	}
}

func TestOverlapsAny(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		existing  []string
		expected  bool
	}{
		{
			name:      "When candidate does not overlap with any existing CIDR, it should return false",
			candidate: "10.0.2.0/24",
			existing:  []string{"10.0.0.0/24", "10.0.1.0/24"},
			expected:  false,
		},
		{
			name:      "When candidate exactly matches an existing CIDR, it should return true",
			candidate: "10.0.1.0/24",
			existing:  []string{"10.0.0.0/24", "10.0.1.0/24"},
			expected:  true,
		},
		{
			name:      "When candidate is contained within a larger existing CIDR, it should return true",
			candidate: "10.0.1.0/24",
			existing:  []string{"10.0.0.0/16"},
			expected:  true,
		},
		{
			name:      "When candidate contains a smaller existing CIDR, it should return true",
			candidate: "10.0.0.0/16",
			existing:  []string{"10.0.1.0/24"},
			expected:  true,
		},
		{
			name:      "When existing CIDRs list is empty, it should return false",
			candidate: "10.0.1.0/24",
			existing:  []string{},
			expected:  false,
		},
		{
			name:      "When candidate is in a completely different address space, it should return false",
			candidate: "192.168.0.0/24",
			existing:  []string{"10.0.0.0/24", "10.0.1.0/24"},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			_, candidateCIDR, err := net.ParseCIDR(tt.candidate)
			g.Expect(err).ToNot(HaveOccurred())

			var existingCIDRs []*net.IPNet
			for _, cidr := range tt.existing {
				_, parsed, err := net.ParseCIDR(cidr)
				g.Expect(err).ToNot(HaveOccurred())
				existingCIDRs = append(existingCIDRs, parsed)
			}

			result := overlapsAny(candidateCIDR, existingCIDRs)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestFindAvailableCIDR(t *testing.T) {
	tests := []struct {
		name         string
		subnets      []*armnetwork.Subnet
		expectedCIDR string
		expectedErr  bool
	}{
		{
			name: "When only the default subnet exists, it should return 10.0.1.0/24",
			subnets: []*armnetwork.Subnet{
				{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.0.0/24"),
					},
				},
			},
			expectedCIDR: "10.0.1.0/24",
		},
		{
			name:         "When no subnets exist, it should return 10.0.1.0/24",
			subnets:      []*armnetwork.Subnet{},
			expectedCIDR: "10.0.1.0/24",
		},
		{
			name: "When the first few /24 blocks are taken, it should skip them and return the next available",
			subnets: []*armnetwork.Subnet{
				{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.0.0/24"),
					},
				},
				{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.1.0/24"),
					},
				},
				{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.2.0/24"),
					},
				},
			},
			expectedCIDR: "10.0.3.0/24",
		},
		{
			name: "When a larger CIDR overlaps with candidate blocks, it should skip overlapping candidates",
			subnets: []*armnetwork.Subnet{
				{
					Properties: &armnetwork.SubnetPropertiesFormat{
						AddressPrefix: to.Ptr("10.0.0.0/16"),
					},
				},
			},
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			subnetsMock := &mockSubnetsAPI{
				subnets: tt.subnets,
			}

			r := &AzurePrivateLinkServiceController{
				Subnets: subnetsMock,
			}

			cidr, err := r.findAvailableCIDR(context.Background(), "rg-test", "vnet-test")
			if tt.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cidr).To(Equal(tt.expectedCIDR))
			}
		})
	}
}
