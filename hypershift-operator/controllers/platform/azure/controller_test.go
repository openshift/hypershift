package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

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

	createCalled bool
	deleteCalled bool
	lastName     string
}

func (m *mockPrivateLinkServicesAPI) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, serviceName string, parameters armnetwork.PrivateLinkService, _ *armnetwork.PrivateLinkServicesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateLinkServicesClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastName = serviceName
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

func TestReconcile_NotFound(t *testing.T) {
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

func TestReconcile_PausedUntil(t *testing.T) {
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
			LoadBalancerIP:       "10.0.0.1",
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
			Name:       "test-pls",
			Namespace:  "test-ns",
			Finalizers: []string{azurePLSFinalizer},
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-ns/test-cluster",
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			LoadBalancerIP:       "10.0.0.1",
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       lbMock,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-pls",
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
			LoadBalancerIP:       "10.0.0.1",
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       lbMock,
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
		// Delete returns not found (already deleted scenario)
		deleteErr: newAzureNotFoundError(),
	}

	r := &AzurePrivateLinkServiceController{
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       &mockLoadBalancersAPI{},
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
		// Delete succeeds, returns nil poller
	}

	r := &AzurePrivateLinkServiceController{
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       &mockLoadBalancersAPI{},
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
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
		name      string
		clusterID string
		expected  string
	}{
		{
			name:      "When given a cluster ID, it should construct pls-<clusterID>",
			clusterID: "12345678-abcd-1234-abcd-123456789012",
			expected:  "pls-12345678-abcd-1234-abcd-123456789012",
		},
		{
			name:      "When given a short cluster ID, it should construct valid PLS name",
			clusterID: "abc",
			expected:  "pls-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := constructPLSName(tt.clusterID)
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
		name           string
		loadBalancerIP string
		loadBalancers  []*armnetwork.LoadBalancer
		expectedILBID  string
		expectedFIPID  string
		expectedErr    bool
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
								},
							},
						},
					},
				},
			},
			expectedILBID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1",
			expectedFIPID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/loadBalancers/lb1/frontendIPConfigurations/fe1",
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
				LoadBalancers: lbMock,
			}

			azPLS := &hyperv1.AzurePrivateLinkService{
				Spec: hyperv1.AzurePrivateLinkServiceSpec{
					LoadBalancerIP:    tt.loadBalancerIP,
					ResourceGroupName: "rg-test",
				},
			}

			ilbID, fipID, err := r.lookupILBByFrontendIP(context.Background(), azPLS)
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

func TestReconcile_WhenAllowedSubscriptionsChange_ItShouldUpdatePLS(t *testing.T) {
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
			LoadBalancerIP:       "10.0.0.1",
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456", "sub-789"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       &mockLoadBalancersAPI{},
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

	// BeginCreateOrUpdate should be called to update visibility/auto-approval
	if !plsMock.createCalled {
		t.Error("expected BeginCreateOrUpdate to be called for subscription update")
	}

	// Nil poller means requeue after PLSRequeueInterval
	if result.RequeueAfter != azureutil.PLSRequeueInterval {
		t.Errorf("expected RequeueAfter=%v for nil poller, got %v", azureutil.PLSRequeueInterval, result.RequeueAfter)
	}
}

func TestReconcile_WhenAllowedSubscriptionsMatch_ItShouldNotUpdatePLS(t *testing.T) {
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
			LoadBalancerIP:       "10.0.0.1",
			SubscriptionID:       "sub-123",
			ResourceGroupName:    "rg-test",
			Location:             "eastus",
			NATSubnetID:          "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Network/virtualNetworks/vnet/subnets/pls-subnet",
			AllowedSubscriptions: []string{"sub-456"},
			GuestSubnetID:        "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet/subnets/guest-subnet",
			GuestVNetID:          "/subscriptions/sub-456/resourceGroups/rg-guest/providers/Microsoft.Network/virtualNetworks/vnet",
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
		Client:              c,
		PrivateLinkServices: plsMock,
		LoadBalancers:       &mockLoadBalancersAPI{},
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
