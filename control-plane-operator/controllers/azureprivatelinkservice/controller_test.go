package azureprivatelinkservice

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

// --- Poller helpers ---

// donePollingHandler is a PollingHandler that is immediately done and returns
// either a result or an error via Result().
type donePollingHandler[T any] struct {
	result T
	err    error
}

func (h *donePollingHandler[T]) Done() bool                                   { return true }
func (h *donePollingHandler[T]) Poll(context.Context) (*http.Response, error) { return nil, nil }
func (h *donePollingHandler[T]) Result(_ context.Context, out *T) error {
	if h.err != nil {
		return h.err
	}
	*out = h.result
	return nil
}

// newDonePoller creates a Poller that is already done and returns the given result.
func newDonePoller[T any](t *testing.T, result T) *azruntime.Poller[T] {
	t.Helper()
	p, err := azruntime.NewPoller[T](nil, azruntime.Pipeline{}, &azruntime.NewPollerOptions[T]{
		Handler: &donePollingHandler[T]{result: result},
	})
	if err != nil {
		t.Fatalf("newDonePoller: unexpected error creating poller: %v", err)
	}
	return p
}

// newErrorPoller creates a Poller that is already done but returns an error from Result.
func newErrorPoller[T any](t *testing.T, err error) *azruntime.Poller[T] {
	t.Helper()
	p, pollerErr := azruntime.NewPoller[T](nil, azruntime.Pipeline{}, &azruntime.NewPollerOptions[T]{
		Handler: &donePollingHandler[T]{err: err},
	})
	if pollerErr != nil {
		t.Fatalf("newErrorPoller: unexpected error creating poller: %v", pollerErr)
	}
	return p
}

// --- Mock implementations ---

type mockPrivateEndpoints struct {
	createErr        error
	deleteErr        error
	getResponse      armnetwork.PrivateEndpointsClientGetResponse
	getErr           error
	createCalled     bool
	deleteCalled     bool
	getCalled        bool
	lastCreateParams armnetwork.PrivateEndpoint
	lastCreateName   string
	lastCreateRG     string
	lastDeleteName   string
	// createPoller, if set, is returned from BeginCreateOrUpdate instead of nil.
	createPoller *azruntime.Poller[armnetwork.PrivateEndpointsClientCreateOrUpdateResponse]
	// deletePoller, if set, is returned from BeginDelete instead of nil.
	deletePoller *azruntime.Poller[armnetwork.PrivateEndpointsClientDeleteResponse]
}

func (m *mockPrivateEndpoints) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, privateEndpointName string, parameters armnetwork.PrivateEndpoint, _ *armnetwork.PrivateEndpointsClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastCreateParams = parameters
	m.lastCreateName = privateEndpointName
	m.lastCreateRG = resourceGroupName
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createPoller, nil
}

func (m *mockPrivateEndpoints) BeginDelete(_ context.Context, resourceGroupName string, privateEndpointName string, _ *armnetwork.PrivateEndpointsClientBeginDeleteOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastDeleteName = privateEndpointName
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return m.deletePoller, nil
}

func (m *mockPrivateEndpoints) Get(_ context.Context, resourceGroupName string, privateEndpointName string, _ *armnetwork.PrivateEndpointsClientGetOptions) (armnetwork.PrivateEndpointsClientGetResponse, error) {
	m.getCalled = true
	return m.getResponse, m.getErr
}

type mockPrivateDNSZones struct {
	createErr        error
	deleteErr        error
	createCalled     bool
	deleteCalled     bool
	lastZoneName     string
	deletedZoneNames []string
	createPoller     *azruntime.Poller[armprivatedns.PrivateZonesClientCreateOrUpdateResponse]
	deletePoller     *azruntime.Poller[armprivatedns.PrivateZonesClientDeleteResponse]
}

func (m *mockPrivateDNSZones) BeginCreateOrUpdate(_ context.Context, _ string, privateZoneName string, _ armprivatedns.PrivateZone, _ *armprivatedns.PrivateZonesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastZoneName = privateZoneName
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createPoller, nil
}

func (m *mockPrivateDNSZones) BeginDelete(_ context.Context, _ string, privateZoneName string, _ *armprivatedns.PrivateZonesClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastZoneName = privateZoneName
	m.deletedZoneNames = append(m.deletedZoneNames, privateZoneName)
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return m.deletePoller, nil
}

type mockVirtualNetworkLinks struct {
	createErr        error
	deleteErr        error
	createCalled     bool
	deleteCalled     bool
	lastLinkName     string
	deletedLinkNames []string
	createPoller     *azruntime.Poller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse]
	deletePoller     *azruntime.Poller[armprivatedns.VirtualNetworkLinksClientDeleteResponse]
}

func (m *mockVirtualNetworkLinks) BeginCreateOrUpdate(_ context.Context, _ string, _ string, virtualNetworkLinkName string, _ armprivatedns.VirtualNetworkLink, _ *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastLinkName = virtualNetworkLinkName
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.createPoller, nil
}

func (m *mockVirtualNetworkLinks) BeginDelete(_ context.Context, _ string, _ string, virtualNetworkLinkName string, _ *armprivatedns.VirtualNetworkLinksClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastLinkName = virtualNetworkLinkName
	m.deletedLinkNames = append(m.deletedLinkNames, virtualNetworkLinkName)
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return m.deletePoller, nil
}

type mockRecordSets struct {
	createErr          error
	deleteErr          error
	deleteErrZone      string // if set, deleteErr only applies to this zone
	createCalled       bool
	deleteCalled       bool
	createCallCount    int
	deleteCallCount    int
	createdRecordNames []string
	deletedRecordNames []string
	lastRecordSetName  string
	lastRecordType     armprivatedns.RecordType
	lastRecordParams   armprivatedns.RecordSet
}

func (m *mockRecordSets) CreateOrUpdate(_ context.Context, _ string, _ string, recordType armprivatedns.RecordType, relativeRecordSetName string, parameters armprivatedns.RecordSet, _ *armprivatedns.RecordSetsClientCreateOrUpdateOptions) (armprivatedns.RecordSetsClientCreateOrUpdateResponse, error) {
	m.createCalled = true
	m.createCallCount++
	m.createdRecordNames = append(m.createdRecordNames, relativeRecordSetName)
	m.lastRecordSetName = relativeRecordSetName
	m.lastRecordType = recordType
	m.lastRecordParams = parameters
	return armprivatedns.RecordSetsClientCreateOrUpdateResponse{}, m.createErr
}

func (m *mockRecordSets) Delete(_ context.Context, _ string, privateDnsZoneName string, recordType armprivatedns.RecordType, relativeRecordSetName string, _ *armprivatedns.RecordSetsClientDeleteOptions) (armprivatedns.RecordSetsClientDeleteResponse, error) {
	m.deleteCalled = true
	m.deleteCallCount++
	m.deletedRecordNames = append(m.deletedRecordNames, relativeRecordSetName)
	m.lastRecordSetName = relativeRecordSetName
	m.lastRecordType = recordType
	if m.deleteErr != nil && (m.deleteErrZone == "" || m.deleteErrZone == privateDnsZoneName) {
		return armprivatedns.RecordSetsClientDeleteResponse{}, m.deleteErr
	}
	return armprivatedns.RecordSetsClientDeleteResponse{}, nil
}

// --- Helper functions ---

func newTestScheme(t *testing.T, g Gomega) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	g.Expect(hyperv1.AddToScheme(scheme)).ToNot(HaveOccurred())
	return scheme
}

func newTestAzurePLS(t *testing.T, name, namespace string) *hyperv1.AzurePrivateLinkService {
	t.Helper()
	return &hyperv1.AzurePrivateLinkService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hyperv1.GroupVersion.String(),
					Kind:       "HostedControlPlane",
					Name:       "test-hcp",
					UID:        types.UID("test-uid"),
				},
			},
		},
		Spec: hyperv1.AzurePrivateLinkServiceSpec{
			SubscriptionID:                 "test-subscription",
			ResourceGroupName:              "test-rg",
			Location:                       "eastus",
			NATSubnetID:                    "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/nat-subnet",
			AdditionalAllowedSubscriptions: []hyperv1.AzureSubscriptionID{"test-subscription"},
			GuestSubnetID:                  "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/guest-vnet/subnets/guest-subnet",
			GuestVNetID:                    "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/guest-vnet",
		},
	}
}

func newTestHCP(t *testing.T, name, namespace, kasHostname string) *hyperv1.HostedControlPlane {
	t.Helper()
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("test-uid"),
		},
		Status: hyperv1.HostedControlPlaneStatus{
			ControlPlaneEndpoint: hyperv1.APIEndpoint{
				Host: kasHostname,
				Port: 6443,
			},
		},
	}
}

// --- Tests ---

func TestPrivateEndpointName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		crName   string
		expected string
	}{
		{
			name:     "When CR name is simple it should append PE suffix",
			crName:   "kube-apiserver-lb",
			expected: "kube-apiserver-lb-pe",
		},
		{
			name:     "When CR name is longer it should still append PE suffix",
			crName:   "my-hosted-cluster-kas-svc",
			expected: "my-hosted-cluster-kas-svc-pe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			result := privateEndpointName(tt.crName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestVNetLinkName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		crName   string
		expected string
	}{
		{
			name:     "When CR name is simple it should append VNet link suffix",
			crName:   "kube-apiserver-lb",
			expected: "kube-apiserver-lb-vnet-link",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			result := vnetLinkName(tt.crName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestExtractPrivateEndpointIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pe       armnetwork.PrivateEndpoint
		expected string
	}{
		{
			name: "When CustomDNSConfigs has IPs it should return the first IP",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{
							IPAddresses: []*string{ptr.To("10.0.1.5")},
						},
					},
				},
			},
			expected: "10.0.1.5",
		},
		{
			name: "When CustomDNSConfigs is empty it should fall back to network interfaces",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					NetworkInterfaces: []*armnetwork.Interface{
						{
							Properties: &armnetwork.InterfacePropertiesFormat{
								IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
									{
										Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
											PrivateIPAddress: ptr.To("10.0.1.6"),
										},
									},
								},
							},
						},
					},
				},
			},
			expected: "10.0.1.6",
		},
		{
			name: "When Properties is nil it should return empty string",
			pe: armnetwork.PrivateEndpoint{
				Properties: nil,
			},
			expected: "",
		},
		{
			name: "When no IPs are available it should return empty string",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			result := extractPrivateEndpointIP(tt.pe)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcile_WhenPLSAliasIsNotYetAvailable_ItShouldRequeue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// Add finalizer so we get past that step
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	// No PLS alias set -> should requeue

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{getErr: &azcore.ResponseError{StatusCode: 404}},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when PLS alias is not available")
}

func TestReconcile_WhenPLSAliasIsAvailable_ItShouldCreatePrivateEndpoint(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	// BeginCreateOrUpdate returns nil poller; controller nil-checks it safely.
	mockPE := &mockPrivateEndpoints{
		getErr: &azcore.ResponseError{StatusCode: 404}, // PE doesn't exist yet
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred(), "should not return error when poller is nil (mock)")
	g.Expect(mockPE.createCalled).To(BeTrue(), "should call BeginCreateOrUpdate for Private Endpoint")
	g.Expect(mockPE.lastCreateName).To(Equal("test-pls-pe"))
	g.Expect(mockPE.lastCreateRG).To(Equal("test-rg"))

	// Verify the PE parameters include the PLS alias and guest subnet
	g.Expect(mockPE.lastCreateParams.Properties).ToNot(BeNil())
	g.Expect(mockPE.lastCreateParams.Properties.Subnet).ToNot(BeNil())
	g.Expect(*mockPE.lastCreateParams.Properties.Subnet.ID).To(Equal(string(azPLS.Spec.GuestSubnetID)))
	g.Expect(mockPE.lastCreateParams.Properties.ManualPrivateLinkServiceConnections).To(HaveLen(1))
	g.Expect(*mockPE.lastCreateParams.Properties.ManualPrivateLinkServiceConnections[0].Properties.PrivateLinkServiceID).To(Equal(azPLS.Status.PrivateLinkServiceAlias))
}

func TestReconcile_WhenPEIsCreated_ItShouldCreateDNSZoneAndARecord(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	// Use private-router as the CR name so that reconcileDNS (hypershift.local zone) is invoked.
	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/private-router-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.Conditions = []metav1.Condition{
		{
			Type:   string(hyperv1.AzurePrivateEndpointAvailable),
			Status: metav1.ConditionTrue,
		},
	}

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	// PE already exists when Get is called
	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To(azPLS.Status.PrivateEndpointID),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{
							IPAddresses: []*string{ptr.To("10.0.1.5")},
						},
					},
				},
			},
		},
	}

	// DNS zone BeginCreateOrUpdate returns nil poller; controller nil-checks it safely.
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "private-router", Namespace: "test-ns"},
	})

	// PE Get was called (to check existence)
	g.Expect(mockPE.getCalled).To(BeTrue(), "should check PE existence via Get")

	// DNS zone creation was attempted with the cluster name-based zone
	g.Expect(mockDNS.createCalled).To(BeTrue(), "should call BeginCreateOrUpdate for Private DNS Zone")
	g.Expect(mockDNS.lastZoneName).To(Equal("test-hcp.hypershift.local"))

	// VNet link and A record creation should also have been called
	g.Expect(mockLinks.createCalled).To(BeTrue(), "should call BeginCreateOrUpdate for VNet Link")
	g.Expect(mockRecords.createCalled).To(BeTrue(), "should call CreateOrUpdate for A records")
	g.Expect(mockRecords.createCallCount).To(Equal(2), "should create two A records (KAS apex and wildcard apps)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api", "*.apps"), "should create KAS and wildcard apps A records")

	// The reconciliation should succeed with a requeue for drift detection
	g.Expect(err).ToNot(HaveOccurred(), "should not return error when pollers are nil (mock)")
	g.Expect(result.RequeueAfter).To(Equal(azureutil.DriftDetectionRequeueInterval), "should requeue for drift detection")
}

func TestReconcile_WhenCRIsDeleted_ItShouldCleanUpPEAndDNS(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.PrivateDNSZoneID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateDnsZones/test-hcp.hypershift.local"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	// BeginDelete returns nil pollers; controller nil-checks them safely.
	mockPE := &mockPrivateEndpoints{
		getErr: &azcore.ResponseError{StatusCode: 404},
	}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	// Call reconcileDelete directly to avoid the fake client DeletionTimestamp limitation
	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// Verify cleanup was attempted for all resource types
	g.Expect(mockRecords.deleteCalled).To(BeTrue(), "should attempt to delete A records")
	g.Expect(mockRecords.deleteCallCount).To(Equal(2), "should delete two A records (KAS apex and wildcard apps)")
	g.Expect(mockRecords.deletedRecordNames).To(ConsistOf("api", "*.apps"), "should delete KAS and wildcard apps A records")
	g.Expect(mockLinks.deleteCalled).To(BeTrue(), "should attempt to delete VNet Link")
	g.Expect(mockDNS.deleteCalled).To(BeTrue(), "should attempt to delete Private DNS Zone")
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should attempt to delete Private Endpoint")
	g.Expect(mockPE.lastDeleteName).To(Equal("test-pls-pe"))
}

func TestReconcile_WhenAllResourcesAreCreated_ItShouldSetAvailableCondition(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	// Simulate all Azure calls succeeding
	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To(azPLS.Status.PrivateEndpointID),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{
							IPAddresses: []*string{ptr.To("10.0.1.5")},
						},
					},
				},
			},
		},
	}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	// Test the PE reconciliation directly (DNS is tested separately)
	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should not requeue after successful PE reconciliation")

	// Verify status was updated
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())

	// Check PE Available condition
	peCondition := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.AzurePrivateEndpointAvailable))
	g.Expect(peCondition).ToNot(BeNil(), "PE Available condition should be set")
	g.Expect(peCondition.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(peCondition.Reason).To(Equal(hyperv1.AzurePLSSuccessReason))
}

func TestReconcile_WhenFinalizerNotPresent_ItShouldAddFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// No finalizer set

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should return zero result after adding finalizer")

	// Verify finalizer was added
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Finalizers).To(ContainElement(azurePrivateLinkServiceFinalizer), "finalizer should be added")
}

func TestReconcileDNS_WhenPEIPNotAvailable_ItShouldRequeue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// No PE IP set

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when PE IP is not available")
}

func TestReconcileDelete_WhenDNSZoneNameNotSet_ItShouldSkipDNSCleanup(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateDNSZoneID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateDnsZones/test-hcp.hypershift.local"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	// DNSZoneName is NOT set -- simulates a CR created before this field was added

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// DNS cleanup should be skipped since DNSZoneName is not set in status
	g.Expect(mockDNS.deleteCalled).To(BeFalse(), "should skip DNS zone deletion when DNSZoneName not set")
	g.Expect(mockLinks.deleteCalled).To(BeFalse(), "should skip VNet link deletion when DNSZoneName not set")
	g.Expect(mockRecords.deleteCalled).To(BeFalse(), "should skip A record deletion when DNSZoneName not set")

	// PE should still be cleaned up
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should still attempt PE deletion even when DNSZoneName not set")
}

func TestGetHostedControlPlane(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	tests := []struct {
		name      string
		azPLS     *hyperv1.AzurePrivateLinkService
		objects   []client.Object
		expectErr bool
		expectHCP string
	}{
		{
			name: "When owner reference exists it should find the HCP",
			azPLS: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "test-ns",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: hyperv1.GroupVersion.String(),
							Kind:       "HostedControlPlane",
							Name:       "my-hcp",
						},
					},
				},
			},
			objects: []client.Object{
				newTestHCP(t, "my-hcp", "test-ns", "api.test.example.com"),
			},
			expectErr: false,
			expectHCP: "my-hcp",
		},
		{
			name: "When no owner reference exists it should return error",
			azPLS: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "test-ns",
				},
			},
			objects:   []client.Object{},
			expectErr: true,
		},
		{
			name: "When HCP does not exist it should return error",
			azPLS: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "test-ns",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: hyperv1.GroupVersion.String(),
							Kind:       "HostedControlPlane",
							Name:       "missing-hcp",
						},
					},
				},
			},
			objects:   []client.Object{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}
			hcp, err := r.getHostedControlPlane(t.Context(), tt.azPLS)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(hcp.Name).To(Equal(tt.expectHCP))
			}
		})
	}
}

func TestReconcilePrivateEndpoint_WhenPEAlreadyExists_ItShouldUpdateStatus(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias.guid.eastus.azure.privatelinkservice"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateEndpoints/existing-pe"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{
							IPAddresses: []*string{ptr.To("10.0.2.10")},
						},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify PE was NOT created (only Get was called)
	g.Expect(mockPE.getCalled).To(BeTrue(), "should call Get")
	g.Expect(mockPE.createCalled).To(BeFalse(), "should NOT call Create when PE already exists")

	// Verify status was updated with existing PE info
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Status.PrivateEndpointID).To(Equal("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateEndpoints/existing-pe"))
	g.Expect(updated.Status.PrivateEndpointIP).To(Equal("10.0.2.10"))
}

func TestReconcile_WhenCRNotFound_ItShouldReturnNoError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())
}

func TestReconcile_WhenCRAlreadyDeleted_ItShouldReturnNoError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	now := metav1.Now()
	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.DeletionTimestamp = &now
	// Our finalizer is removed; add a different one so the fake client accepts the object
	azPLS.Finalizers = []string{"some-other-finalizer"}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())
}

func TestExtractPrivateEndpointConnectionState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pe       armnetwork.PrivateEndpoint
		expected string
	}{
		{
			name: "When PE connection is approved it should return Approved",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
						{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Approved"),
								},
							},
						},
					},
				},
			},
			expected: "Approved",
		},
		{
			name: "When PE connection is pending it should return Pending",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
						{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Pending"),
								},
							},
						},
					},
				},
			},
			expected: "Pending",
		},
		{
			name: "When PE connection is rejected it should return Rejected",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
						{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Rejected"),
								},
							},
						},
					},
				},
			},
			expected: "Rejected",
		},
		{
			name: "When PE properties is nil it should return empty string",
			pe: armnetwork.PrivateEndpoint{
				Properties: nil,
			},
			expected: "",
		},
		{
			name: "When ManualPrivateLinkServiceConnections is empty it should return empty string",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{},
				},
			},
			expected: "",
		},
		{
			name: "When connection has nil Properties it should return empty string",
			pe: armnetwork.PrivateEndpoint{
				Properties: &armnetwork.PrivateEndpointProperties{
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
						{
							Properties: nil,
						},
					},
				},
			},
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			result := extractPrivateEndpointConnectionState(tt.pe)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestEnsureHCPFinalizer_WhenNotPresent_ItShouldAddFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	// No finalizer set on HCP

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hcp).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client: fakeClient,
	}

	result, err := r.ensureHCPFinalizer(t.Context(), hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should return zero result after adding HCP finalizer")

	// Verify finalizer was added to HCP
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP should have the Azure PLS finalizer")
}

func TestEnsureHCPFinalizer_WhenAlreadyPresent_ItShouldNotModify(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hcp).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client: fakeClient,
	}

	result, err := r.ensureHCPFinalizer(t.Context(), hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify finalizer is still present and only once
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(Equal([]string{hcpAzurePLSFinalizerName}))
}

func TestReconcile_WhenPLSAliasIsAvailable_ItShouldAddHCPFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	// No HCP finalizer yet

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getErr: &azcore.ResponseError{StatusCode: 404},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})
	g.Expect(err).ToNot(HaveOccurred())

	// Verify HCP finalizer was added
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP should have the Azure PLS finalizer after reconciliation with PLS alias available")
}

func TestReconcileHCPDeletion_WhenHCPIsBeingDeleted_ItShouldCleanUpAndRemoveFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Status.PrivateDNSZoneID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateDnsZones/test-hcp.hypershift.local"

	now := metav1.Now()
	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.DeletionTimestamp = &now
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName} // Required for DeletionTimestamp to be respected by fake client

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	// Call reconcileHCPDeletion directly
	result, err := r.reconcileHCPDeletion(t.Context(), azPLS, hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify Azure cleanup was attempted
	g.Expect(mockRecords.deleteCalled).To(BeTrue(), "should attempt to delete A records")
	g.Expect(mockRecords.deleteCallCount).To(Equal(2), "should delete two A records (KAS apex and wildcard apps)")
	g.Expect(mockRecords.deletedRecordNames).To(ConsistOf("api", "*.apps"), "should delete KAS and wildcard apps A records")
	g.Expect(mockLinks.deleteCalled).To(BeTrue(), "should attempt to delete VNet Link")
	g.Expect(mockDNS.deleteCalled).To(BeTrue(), "should attempt to delete Private DNS Zone")
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should attempt to delete Private Endpoint")

	// Verify the HCP finalizer was removed. The fake client garbage-collects
	// objects whose DeletionTimestamp is set and all finalizers are removed,
	// so a NotFound error confirms the finalizer was successfully removed.
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	if apierrors.IsNotFound(err) {
		// Expected: fake client deleted the HCP because all finalizers were removed
		return
	}
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).ToNot(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be removed after cleanup")
}

func TestReconcileHCPDeletion_WhenHCPDoesNotHaveFinalizer_ItShouldBeNoOp(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}

	now := metav1.Now()
	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.DeletionTimestamp = &now
	hcp.Finalizers = []string{"some-other-finalizer"} // Has a different finalizer, not ours

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	mockDNS := &mockPrivateDNSZones{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileHCPDeletion(t.Context(), azPLS, hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify no Azure cleanup was attempted
	g.Expect(mockPE.deleteCalled).To(BeFalse(), "should not attempt PE deletion when HCP finalizer not present")
	g.Expect(mockDNS.deleteCalled).To(BeFalse(), "should not attempt DNS deletion when HCP finalizer not present")
}

func TestReconcileHCPDeletion_WhenAzureCleanupFails_ItShouldReturnErrorAndPreserveFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Status.PrivateDNSZoneID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateDnsZones/test-hcp.hypershift.local"

	now := metav1.Now()
	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.DeletionTimestamp = &now
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	// Configure mockRecordSets with a non-404 error to make reconcileDelete fail
	deleteErr := fmt.Errorf("mock Azure API failure")
	mockRecords := &mockRecordSets{deleteErr: deleteErr}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          mockRecords,
	}

	_, err := r.reconcileHCPDeletion(t.Context(), azPLS, hcp, testr.New(t))
	g.Expect(err).To(HaveOccurred(), "should return error when Azure cleanup fails")
	g.Expect(err).To(MatchError(ContainSubstring("failed to clean up Azure resources during HCP deletion")))

	// Verify the HCP finalizer was NOT removed
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be preserved when cleanup fails")
}

func TestReconcile_WhenHCPIsBeingDeleted_ItShouldTriggerCleanupInsteadOfCreation(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	now := metav1.Now()
	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.DeletionTimestamp = &now
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getErr: &azcore.ResponseError{StatusCode: 404},
	}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify PE creation was NOT called (cleanup path does not create)
	g.Expect(mockPE.createCalled).To(BeFalse(), "should not create PE when HCP is being deleted")

	// Verify cleanup was performed
	g.Expect(mockRecords.deleteCalled).To(BeTrue(), "should clean up A records during HCP deletion")
	g.Expect(mockRecords.deleteCallCount).To(Equal(2), "should delete two A records (KAS apex and wildcard apps)")
	g.Expect(mockLinks.deleteCalled).To(BeTrue(), "should clean up VNet link during HCP deletion")
	g.Expect(mockDNS.deleteCalled).To(BeTrue(), "should clean up DNS zone during HCP deletion")
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should clean up PE during HCP deletion")

	// Verify the HCP finalizer was removed. The fake client garbage-collects
	// objects whose DeletionTimestamp is set and all finalizers are removed,
	// so a NotFound error confirms the finalizer was successfully removed.
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	if apierrors.IsNotFound(err) {
		// Expected: fake client deleted the HCP because all finalizers were removed
		return
	}
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).ToNot(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be removed")
}

func TestReconcile_WhenPEConnectionNotApproved_ItShouldRequeueWithWarning(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias.guid.eastus.azure.privatelinkservice"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/pe/id"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.5")}},
					},
					ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
						{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Pending"),
								},
							},
						},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when connection not approved")

	// Check condition was set to False with appropriate reason
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())

	peCondition := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.AzurePrivateEndpointAvailable))
	g.Expect(peCondition).ToNot(BeNil())
	g.Expect(peCondition.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(peCondition.Reason).To(Equal("PrivateEndpointConnectionNotApproved"))
}

func TestReconcile_WhenNonPrivateRouterCR_ItShouldSkipHypershiftLocalDNS(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	// Use a non-private-router name (e.g., oauth-openshift)
	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-pls-alias.guid.eastus.azure.privatelinkservice"
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/oauth-openshift-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"
	azPLS.Status.Conditions = []metav1.Condition{
		{
			Type:   string(hyperv1.AzurePrivateEndpointAvailable),
			Status: metav1.ConditionTrue,
		},
	}

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To(azPLS.Status.PrivateEndpointID),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.6")}},
					},
				},
			},
		},
	}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(azureutil.DriftDetectionRequeueInterval), "should requeue for drift detection")

	// DNS zone should NOT have been created for hypershift.local zone (only private-router creates it)
	g.Expect(mockDNS.createCalled).To(BeFalse(), "non-private-router CR should not create hypershift.local DNS zone")
	g.Expect(mockLinks.createCalled).To(BeFalse(), "non-private-router CR should not create VNet link for hypershift.local zone")
	g.Expect(mockRecords.createCalled).To(BeFalse(), "non-private-router CR should not create any A records (no base domain set)")

	// DNSZoneName should be persisted in status for delete-path cleanup
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Status.DNSZoneName).To(Equal("test-hcp.hypershift.local"), "non-private-router CRs should persist DNSZoneName for delete-path cleanup")
}

func TestReconcileBaseDomainDNS_WhenPrivateRouterWithNoSibling_ItShouldCreateBothRecords(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.reconcileBaseDomainDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// When no sibling OAuth CR exists, private-router should create both api and oauth records
	g.Expect(mockRecords.createCallCount).To(Equal(2), "should create two A records (api and oauth)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api-test-hcp", "oauth-test-hcp"), "should create api and oauth base domain records")
}

func TestReconcileBaseDomainDNS_WhenPrivateRouterWithSiblingOAuth_ItShouldOnlyCreateAPIRecord(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	// Create a sibling OAuth CR in the same namespace with the same base domain
	oauthPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	oauthPLS.Spec.BaseDomain = "example.com"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, oauthPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.reconcileBaseDomainDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// When a sibling OAuth CR exists, private-router should only create the api record
	g.Expect(mockRecords.createCallCount).To(Equal(1), "should create only one A record (api)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api-test-hcp"), "should only create api base domain record")
}

func TestReconcileBaseDomainDNS_WhenOAuthCR_ItShouldOnlyCreateOAuthRecord(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	result, err := r.reconcileBaseDomainDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// OAuth CR should only create the oauth record
	g.Expect(mockRecords.createCallCount).To(Equal(1), "should create only one A record (oauth)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("oauth-test-hcp"), "should only create oauth base domain record")
}

func TestReconcileDelete_WhenSiblingCRsExist_ItShouldNotDeleteBaseDomainZone(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/private-router-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Spec.BaseDomain = "example.com"

	// Create a sibling OAuth CR in the same namespace with the same base domain
	oauthPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	oauthPLS.Spec.BaseDomain = "example.com"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, oauthPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// A records should only include the api record (sibling OAuth owns the oauth record)
	g.Expect(mockRecords.deleteCalled).To(BeTrue(), "should delete A records")
	// The hypershift.local records (api, *.apps) + only api-test-hcp from base domain = 3
	g.Expect(mockRecords.deletedRecordNames).To(ConsistOf("api", "*.apps", "api-test-hcp"),
		"should delete hypershift.local records and only api base domain record (sibling owns oauth)")

	// VNet link deletion should include hypershift.local link + base domain link
	g.Expect(mockLinks.deletedLinkNames).To(ConsistOf("private-router-vnet-link", "private-router-basedomain-vnet-link"),
		"should delete both VNet links")

	// DNS zone deletion should only include hypershift.local zone, not base domain zone (sibling still uses it)
	g.Expect(mockDNS.deletedZoneNames).To(ConsistOf("test-hcp.hypershift.local"),
		"should preserve the base domain zone while a sibling CR still exists")

	// PE should still be cleaned up
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should still delete Private Endpoint")
}

func TestReconcileDelete_WhenNoSiblingCRs_ItShouldDeleteBaseDomainZone(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateEndpointID = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/privateEndpoints/private-router-pe"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Spec.BaseDomain = "example.com"

	// No sibling CRs

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	mockDNS := &mockPrivateDNSZones{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// With no siblings, private-router should delete both api and oauth base domain records
	g.Expect(mockRecords.deleteCalled).To(BeTrue(), "should delete A records")
	g.Expect(mockRecords.deletedRecordNames).To(ConsistOf("api", "*.apps", "api-test-hcp", "oauth-test-hcp"),
		"should delete hypershift.local records and both api+oauth base domain records when no siblings")

	// Both zones should be deleted (last CR using them)
	g.Expect(mockDNS.deletedZoneNames).To(ConsistOf("test-hcp.hypershift.local", "example.com"),
		"should delete both zones when the last CR goes away")

	// Both VNet links should be deleted
	g.Expect(mockLinks.deletedLinkNames).To(ConsistOf("private-router-vnet-link", "private-router-basedomain-vnet-link"),
		"should delete both VNet links when the last CR goes away")
}

func TestBaseDomainVNetLinkName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		crName   string
		expected string
	}{
		{
			name:     "When CR name is private-router, it should append basedomain VNet link suffix",
			crName:   "private-router",
			expected: "private-router-basedomain-vnet-link",
		},
		{
			name:     "When CR name is oauth-openshift, it should append basedomain VNet link suffix",
			crName:   "oauth-openshift",
			expected: "oauth-openshift-basedomain-vnet-link",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			result := baseDomainVNetLinkName(tt.crName)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestDNSZoneConfigErrMsgQualifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		logPrefix string
		expected  string
	}{
		{
			name:      "When logPrefix is empty, it should return empty string",
			logPrefix: "",
			expected:  "",
		},
		{
			name:      "When logPrefix is set, it should return prefix followed by a space",
			logPrefix: "base domain",
			expected:  "base domain ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			cfg := dnsZoneConfig{logPrefix: tt.logPrefix}
			g.Expect(cfg.errMsgQualifier()).To(Equal(tt.expected))
		})
	}
}

func TestRecordNamesForCR(t *testing.T) {
	tests := []struct {
		name        string
		crName      string
		clusterName string
		siblings    []client.Object
		expected    []string
	}{
		{
			name:        "When CR is not private-router, it should return only the oauth record",
			crName:      "oauth-openshift",
			clusterName: "my-cluster",
			expected:    []string{"oauth-my-cluster"},
		},
		{
			name:        "When CR is private-router with no sibling, it should return api and oauth records",
			crName:      "private-router",
			clusterName: "my-cluster",
			expected:    []string{"api-my-cluster", "oauth-my-cluster"},
		},
		{
			name:        "When CR is private-router with sibling OAuth CR, it should return only api record",
			crName:      "private-router",
			clusterName: "my-cluster",
			siblings: []client.Object{
				func() *hyperv1.AzurePrivateLinkService {
					cr := newTestAzurePLS(t, "oauth-openshift", "test-ns")
					cr.Spec.BaseDomain = "example.com"
					return cr
				}(),
			},
			expected: []string{"api-my-cluster"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			scheme := newTestScheme(t, g)

			azPLS := newTestAzurePLS(t, tt.crName, "test-ns")
			azPLS.Spec.BaseDomain = "example.com"

			objs := append([]client.Object{azPLS}, tt.siblings...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}
			result, err := r.recordNamesForCR(t.Context(), azPLS, tt.clusterName, testr.New(t))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestDeleteBaseDomainARecords(t *testing.T) {
	tests := []struct {
		name                   string
		crName                 string
		dnsZoneName            string
		baseDomain             string
		siblings               []client.Object
		expectedDeletedRecords []string
		expectDeleteCalled     bool
	}{
		{
			name:               "When dnsZoneName is empty, it should skip deletion",
			crName:             "private-router",
			dnsZoneName:        "",
			baseDomain:         "example.com",
			expectDeleteCalled: false,
		},
		{
			name:               "When dnsZoneName has wrong suffix, it should skip deletion",
			crName:             "private-router",
			dnsZoneName:        "cluster.wrong.suffix",
			baseDomain:         "example.com",
			expectDeleteCalled: false,
		},
		{
			name:                   "When private-router with no siblings, it should delete api and oauth records",
			crName:                 "private-router",
			dnsZoneName:            "my-cluster.hypershift.local",
			baseDomain:             "example.com",
			expectedDeletedRecords: []string{"api-my-cluster", "oauth-my-cluster"},
			expectDeleteCalled:     true,
		},
		{
			name:        "When private-router with sibling OAuth CR, it should delete only api record",
			crName:      "private-router",
			dnsZoneName: "my-cluster.hypershift.local",
			baseDomain:  "example.com",
			siblings: []client.Object{
				func() *hyperv1.AzurePrivateLinkService {
					cr := newTestAzurePLS(t, "oauth-openshift", "test-ns")
					cr.Spec.BaseDomain = "example.com"
					return cr
				}(),
			},
			expectedDeletedRecords: []string{"api-my-cluster"},
			expectDeleteCalled:     true,
		},
		{
			name:                   "When non-private-router CR, it should delete only oauth record",
			crName:                 "oauth-openshift",
			dnsZoneName:            "my-cluster.hypershift.local",
			baseDomain:             "example.com",
			expectedDeletedRecords: []string{"oauth-my-cluster"},
			expectDeleteCalled:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			scheme := newTestScheme(t, g)

			azPLS := newTestAzurePLS(t, tt.crName, "test-ns")
			azPLS.Spec.BaseDomain = tt.baseDomain
			azPLS.Status.DNSZoneName = tt.dnsZoneName

			objs := append([]client.Object{azPLS}, tt.siblings...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			mockRecords := &mockRecordSets{}
			r := &AzurePrivateLinkServiceReconciler{
				Client:     fakeClient,
				RecordSets: mockRecords,
			}

			err := r.deleteBaseDomainARecords(t.Context(), azPLS, "test-rg", tt.baseDomain, tt.dnsZoneName, testr.New(t))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(mockRecords.deleteCalled).To(Equal(tt.expectDeleteCalled))
			if tt.expectDeleteCalled {
				g.Expect(mockRecords.deletedRecordNames).To(Equal(tt.expectedDeletedRecords))
			}
		})
	}
}

func TestMapHCPToAzurePLS(t *testing.T) {
	tests := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		plsCRs         []client.Object
		expectRequests int
	}{
		{
			name: "When HCP has the Azure PLS finalizer and PLS CRs exist, it should return requests for all PLS CRs",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
				hcp.Finalizers = []string{hcpAzurePLSFinalizerName}
				return hcp
			}(),
			plsCRs: []client.Object{
				newTestAzurePLS(t, "private-router", "test-ns"),
				newTestAzurePLS(t, "oauth-openshift", "test-ns"),
			},
			expectRequests: 2,
		},
		{
			name: "When HCP does not have the Azure PLS finalizer, it should return no requests",
			hcp:  newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com"),
			plsCRs: []client.Object{
				newTestAzurePLS(t, "private-router", "test-ns"),
			},
			expectRequests: 0,
		},
		{
			name: "When HCP has the finalizer but no PLS CRs exist, it should return no requests",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
				hcp.Finalizers = []string{hcpAzurePLSFinalizerName}
				return hcp
			}(),
			plsCRs:         []client.Object{},
			expectRequests: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			scheme := newTestScheme(t, g)

			objs := append([]client.Object{tt.hcp}, tt.plsCRs...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}
			mapFn := r.mapHCPToAzurePLS()
			ctx := log.IntoContext(t.Context(), testr.New(t))
			requests := mapFn(ctx, tt.hcp)
			g.Expect(requests).To(HaveLen(tt.expectRequests))
		})
	}
}

func TestHasSiblingCR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		azPLS     *hyperv1.AzurePrivateLinkService
		siblings  []client.Object
		expectHas bool
		expectErr bool
	}{
		{
			name: "When sibling with same base domain exists it should return true",
			azPLS: func() *hyperv1.AzurePrivateLinkService {
				cr := newTestAzurePLS(t, "private-router", "test-ns")
				cr.Spec.BaseDomain = "example.com"
				return cr
			}(),
			siblings: []client.Object{
				func() *hyperv1.AzurePrivateLinkService {
					cr := newTestAzurePLS(t, "oauth-openshift", "test-ns")
					cr.Spec.BaseDomain = "example.com"
					return cr
				}(),
			},
			expectHas: true,
		},
		{
			name: "When no siblings exist it should return false",
			azPLS: func() *hyperv1.AzurePrivateLinkService {
				cr := newTestAzurePLS(t, "private-router", "test-ns")
				cr.Spec.BaseDomain = "example.com"
				return cr
			}(),
			siblings:  []client.Object{},
			expectHas: false,
		},
		{
			name: "When sibling has different base domain it should return false",
			azPLS: func() *hyperv1.AzurePrivateLinkService {
				cr := newTestAzurePLS(t, "private-router", "test-ns")
				cr.Spec.BaseDomain = "example.com"
				return cr
			}(),
			siblings: []client.Object{
				func() *hyperv1.AzurePrivateLinkService {
					cr := newTestAzurePLS(t, "oauth-openshift", "test-ns")
					cr.Spec.BaseDomain = "other.com"
					return cr
				}(),
			},
			expectHas: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			scheme := newTestScheme(t, g)

			objs := append([]client.Object{tt.azPLS}, tt.siblings...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}
			has, err := r.hasSiblingCR(t.Context(), tt.azPLS)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(has).To(Equal(tt.expectHas))
			}
		})
	}
}

func TestMapHCPToAzurePLS_WhenObjectIsNotHCP_ItShouldReturnNil(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}
	mapFunc := r.mapHCPToAzurePLS()

	ctx := log.IntoContext(t.Context(), testr.New(t))
	requests := mapFunc(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "not-an-hcp", Namespace: "test-ns"},
	})
	g.Expect(requests).To(BeNil())
}

func TestErrMsgQualifier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		logPrefix string
		expected  string
	}{
		{
			name:      "When logPrefix is empty it should return empty string",
			logPrefix: "",
			expected:  "",
		},
		{
			name:      "When logPrefix is set it should return prefix with trailing space",
			logPrefix: "base domain",
			expected:  "base domain ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)
			cfg := dnsZoneConfig{logPrefix: tt.logPrefix}
			g.Expect(cfg.errMsgQualifier()).To(Equal(tt.expected))
		})
	}
}

func TestReconcilePrivateEndpoint_WhenGetReturnsNon404Error_ItShouldSetErrorCondition(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getErr: &azcore.ResponseError{StatusCode: 500},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred(), "handleAzureError returns nil error with RequeueAfter")
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue after Azure error")

	updated := &hyperv1.AzurePrivateLinkService{}
	g.Expect(fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)).To(Succeed())

	peCondition := meta.FindStatusCondition(updated.Status.Conditions, string(hyperv1.AzurePrivateEndpointAvailable))
	g.Expect(peCondition).ToNot(BeNil())
	g.Expect(peCondition.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(peCondition.Reason).To(Equal("PrivateEndpointGetFailed"))
}

func TestReconcilePrivateEndpoint_WhenCreateFails_ItShouldSetErrorCondition(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getErr:    &azcore.ResponseError{StatusCode: 404},
		createErr: fmt.Errorf("mock creation failure"),
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
	g.Expect(mockPE.createCalled).To(BeTrue())
}

func TestReconcileDNS_WhenDNSZoneCreateFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{createErr: fmt.Errorf("mock DNS zone creation failure")},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDNS_WhenVNetLinkCreateFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{createErr: fmt.Errorf("mock VNet link creation failure")},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDNS_WhenARecordCreateFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{createErr: fmt.Errorf("mock A record creation failure")},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileBaseDomainDNS_WhenDNSZoneCreateFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{createErr: fmt.Errorf("mock DNS zone creation failure")},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileBaseDomainDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDelete_WhenARecordDeleteReturnsNon404Error_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{deleteErr: fmt.Errorf("mock delete error")},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete A record")))
}

func TestReconcileDelete_WhenVNetLinkDeleteReturnsNon404Error_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{deleteErr: fmt.Errorf("mock delete error")},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to begin deleting VNet Link")))
}

func TestReconcileDelete_WhenDNSZoneDeleteReturnsNon404Error_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{deleteErr: fmt.Errorf("mock delete error")},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to begin deleting Private DNS Zone")))
}

func TestReconcileDelete_WhenPEDeleteReturnsNon404Error_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// No DNS zone → skips DNS cleanup, goes straight to PE

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deleteErr: fmt.Errorf("mock delete error")},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to begin deleting Private Endpoint")))
}

func TestReconcileDelete_WhenAllDeletesReturn404_ItShouldCompleteSuccessfully(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	notFoundErr := &azcore.ResponseError{StatusCode: 404}
	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deleteErr: notFoundErr},
		PrivateDNSZones:     &mockPrivateDNSZones{deleteErr: notFoundErr},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{deleteErr: notFoundErr},
		RecordSets:          &mockRecordSets{deleteErr: notFoundErr},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred(), "404 errors should be treated as already deleted")
}

func TestReconcileDelete_WhenNonPrivateRouterWithBaseDomain_ItShouldDeleteOAuthRecordOnly(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Spec.BaseDomain = "example.com"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// Should delete hypershift.local records (api, *.apps) + only oauth base domain record
	g.Expect(mockRecords.deletedRecordNames).To(ContainElement("oauth-test-hcp"))
	g.Expect(mockRecords.deletedRecordNames).ToNot(ContainElement("api-test-hcp"),
		"non-private-router CR should not delete the api base domain record")
}

func TestReconcileDelete_WhenBaseDomainButClusterNameCannotBeDerived_ItShouldSkipRecordDeletion(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	// No DNSZoneName → can't derive cluster name → skip record deletion

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	mockRecords := &mockRecordSets{}
	mockLinks := &mockVirtualNetworkLinks{}
	mockDNS := &mockPrivateDNSZones{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// No base domain A records should be deleted since cluster name couldn't be derived
	g.Expect(mockRecords.deletedRecordNames).To(BeEmpty(),
		"should not delete any A records when cluster name cannot be derived")
	// Zone and VNet link deletion still proceed because they only require the baseDomain
	// from the spec (not the cluster name). Only A record names depend on the cluster name.
	g.Expect(mockLinks.deleteCalled).To(BeTrue(), "should still attempt base domain VNet link deletion")
	g.Expect(mockDNS.deleteCalled).To(BeTrue(), "should still attempt base domain zone deletion")
}

func TestReconcileDelete_WhenBaseDomainRecordDeleteFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Spec.BaseDomain = "example.com"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	// Only error on base domain zone deletes; hypershift.local record deletes should succeed.
	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{deleteErr: fmt.Errorf("mock delete error"), deleteErrZone: "example.com"},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete base domain A record")))
}

func TestReconcileDelete_WhenBaseDomainVNetLinkDeleteFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	// No DNSZoneName → skips hypershift.local section and base domain records

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{deleteErr: fmt.Errorf("mock delete error")},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to begin deleting VNet Link")))
}

func TestReconcileDelete_WhenBaseDomainZoneDeleteFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	// No DNSZoneName → skips hypershift.local section and base domain records

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{deleteErr: fmt.Errorf("mock delete error")},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to begin deleting Private DNS Zone")))
}

// Tests CR deletion with DNSZoneName set in status, which causes reconcileDelete
// to attempt DNS record, VNet link, and zone cleanup in addition to PE deletion.
// Compare with TestReconcile_WhenCRDeletionSucceeds which has no DNSZoneName and
// only verifies PE deletion proceeds.
func TestReconcile_WhenCRDeletionWithFinalizer_ItShouldCleanUpAndRemoveFinalizer(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	now := metav1.Now()
	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.DeletionTimestamp = &now
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify finalizer was removed (object may be garbage collected by fake client)
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	if apierrors.IsNotFound(err) {
		return // Object was garbage collected after all finalizers removed
	}
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Finalizers).ToNot(ContainElement(azurePrivateLinkServiceFinalizer))
}

func TestReconcile_WhenNonPrivateRouterWithDNSZoneNameAlreadySet_ItShouldPreserveExistingDNSZoneName(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"
	azPLS.Status.PrivateEndpointID = "/pe/id"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local" // Already set

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/pe/id"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.6")}},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(azureutil.DriftDetectionRequeueInterval))

	updated := &hyperv1.AzurePrivateLinkService{}
	g.Expect(fakeClient.Get(t.Context(), types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"}, updated)).To(Succeed())
	g.Expect(updated.Status.DNSZoneName).To(Equal("test-hcp.hypershift.local"))
}

func TestReconcile_WhenPrivateRouterWithBaseDomain_ItShouldReconcileBaseDomainDNS(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.PrivateEndpointID = "/pe/id"
	azPLS.Spec.BaseDomain = "example.com"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/pe/id"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.5")}},
					},
				},
			},
		},
	}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          mockRecords,
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "private-router", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(azureutil.DriftDetectionRequeueInterval))

	// Should have created both hypershift.local records (api, *.apps) and
	// base domain records (api-test-hcp, oauth-test-hcp since no sibling)
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api", "*.apps", "api-test-hcp", "oauth-test-hcp"))
}

func TestReconcilePrivateEndpoint_WhenPollerSucceeds_ItShouldUpdateStatusWithPEInfo(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	peResp := armnetwork.PrivateEndpointsClientCreateOrUpdateResponse{
		PrivateEndpoint: armnetwork.PrivateEndpoint{
			ID: ptr.To("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateEndpoints/test-pls-pe"),
			Properties: &armnetwork.PrivateEndpointProperties{
				CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
					{IPAddresses: []*string{ptr.To("10.0.1.5")}},
				},
			},
		},
	}

	mockPE := &mockPrivateEndpoints{
		getErr:       &azcore.ResponseError{StatusCode: 404},
		createPoller: newDonePoller(t, peResp),
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	updated := &hyperv1.AzurePrivateLinkService{}
	g.Expect(fakeClient.Get(t.Context(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)).To(Succeed())
	g.Expect(updated.Status.PrivateEndpointIP).To(Equal("10.0.1.5"))
	g.Expect(updated.Status.PrivateEndpointID).To(ContainSubstring("test-pls-pe"))
}

func TestReconcilePrivateEndpoint_WhenPollerFails_ItShouldSetErrorCondition(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getErr:       &azcore.ResponseError{StatusCode: 404},
		createPoller: newErrorPoller[armnetwork.PrivateEndpointsClientCreateOrUpdateResponse](t, fmt.Errorf("poller failed")),
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: mockPE,
	}

	result, err := r.reconcilePrivateEndpoint(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDNS_WhenDNSZonePollerSucceeds_ItShouldCreateLinkAndRecords(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	zoneResp := armprivatedns.PrivateZonesClientCreateOrUpdateResponse{
		PrivateZone: armprivatedns.PrivateZone{
			ID: ptr.To("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/test.hypershift.local"),
		},
	}
	linkResp := armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse{}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{createPoller: newDonePoller(t, zoneResp)},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{createPoller: newDonePoller(t, linkResp)},
		RecordSets:          mockRecords,
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api", "*.apps"))
}

func TestReconcileDNS_WhenDNSZonePollerFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client: fakeClient,
		PrivateDNSZones: &mockPrivateDNSZones{
			createPoller: newErrorPoller[armprivatedns.PrivateZonesClientCreateOrUpdateResponse](t, fmt.Errorf("zone poller failed")),
		},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDNS_WhenVNetLinkPollerFails_ItShouldRequeueAfterError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:          fakeClient,
		PrivateDNSZones: &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{
			createPoller: newErrorPoller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse](t, fmt.Errorf("link poller failed")),
		},
		RecordSets: &mockRecordSets{},
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcileDNS_WhenVNetAlreadyLinked_ItShouldContinue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockRecords := &mockRecordSets{}
	r := &AzurePrivateLinkServiceReconciler{
		Client:          fakeClient,
		PrivateDNSZones: &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{
			createPoller: newErrorPoller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse](t,
				fmt.Errorf("already linked to the virtual network")),
		},
		RecordSets: mockRecords,
	}

	result, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should continue past VNet already linked")
	g.Expect(mockRecords.createCalled).To(BeTrue(), "should still create A records")
}

func TestReconcileDelete_WhenPollersSucceed_ItShouldDeleteAllResources(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		deletePoller: newDonePoller(t, armnetwork.PrivateEndpointsClientDeleteResponse{}),
	}
	mockDNS := &mockPrivateDNSZones{
		deletePoller: newDonePoller(t, armprivatedns.PrivateZonesClientDeleteResponse{}),
	}
	mockLinks := &mockVirtualNetworkLinks{
		deletePoller: newDonePoller(t, armprivatedns.VirtualNetworkLinksClientDeleteResponse{}),
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(mockPE.deleteCalled).To(BeTrue())
	g.Expect(mockDNS.deleteCalled).To(BeTrue())
	g.Expect(mockLinks.deleteCalled).To(BeTrue())
}

func TestReconcileDelete_WhenVNetLinkPollerFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: &mockPrivateEndpoints{},
		PrivateDNSZones:  &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{
			deletePoller: newErrorPoller[armprivatedns.VirtualNetworkLinksClientDeleteResponse](t, fmt.Errorf("poller failed")),
		},
		RecordSets: &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete VNet Link")))
}

func TestReconcileDelete_WhenDNSZonePollerFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{deletePoller: newErrorPoller[armprivatedns.PrivateZonesClientDeleteResponse](t, fmt.Errorf("poller failed"))},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete Private DNS Zone")))
}

func TestReconcileDelete_WhenPEPollerFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// No DNS zone → skip DNS cleanup

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deletePoller: newErrorPoller[armnetwork.PrivateEndpointsClientDeleteResponse](t, fmt.Errorf("poller failed"))},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete Private Endpoint")))
}

func TestReconcileDelete_WhenDeletePollersReturn404_ItShouldSucceed(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	notFoundErr := &azcore.ResponseError{StatusCode: 404}
	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deletePoller: newErrorPoller[armnetwork.PrivateEndpointsClientDeleteResponse](t, notFoundErr)},
		PrivateDNSZones:     &mockPrivateDNSZones{deletePoller: newErrorPoller[armprivatedns.PrivateZonesClientDeleteResponse](t, notFoundErr)},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{deletePoller: newErrorPoller[armprivatedns.VirtualNetworkLinksClientDeleteResponse](t, notFoundErr)},
		RecordSets:          &mockRecordSets{deleteErr: notFoundErr},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred(), "404 from pollers should be treated as already deleted")
}

func TestReconcileDelete_WhenBaseDomainPollersSucceed_ItShouldDeleteAllBaseDomainResources(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"
	azPLS.Spec.BaseDomain = "example.com"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	mockLinks := &mockVirtualNetworkLinks{
		deletePoller: newDonePoller(t, armprivatedns.VirtualNetworkLinksClientDeleteResponse{}),
	}
	mockDNS := &mockPrivateDNSZones{
		deletePoller: newDonePoller(t, armprivatedns.PrivateZonesClientDeleteResponse{}),
	}
	mockRecords := &mockRecordSets{}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deletePoller: newDonePoller(t, armnetwork.PrivateEndpointsClientDeleteResponse{})},
		PrivateDNSZones:     mockDNS,
		VirtualNetworkLinks: mockLinks,
		RecordSets:          mockRecords,
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// Both hypershift.local zone and base domain zone should be deleted
	g.Expect(mockDNS.deletedZoneNames).To(ConsistOf("test-hcp.hypershift.local", "example.com"))
	// Both VNet links should be deleted
	g.Expect(mockLinks.deletedLinkNames).To(ConsistOf("private-router-vnet-link", "private-router-basedomain-vnet-link"))
}

func TestReconcileDelete_WhenBaseDomainVNetLinkPollerFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	// No DNSZoneName → skips hypershift.local section, goes to base domain

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:           fakeClient,
		PrivateEndpoints: &mockPrivateEndpoints{},
		PrivateDNSZones:  &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{
			deletePoller: newErrorPoller[armprivatedns.VirtualNetworkLinksClientDeleteResponse](t, fmt.Errorf("poller failed")),
		},
		RecordSets: &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete VNet Link")))
}

func TestReconcileDelete_WhenBaseDomainZonePollerFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Spec.BaseDomain = "example.com"
	// No DNSZoneName → skips hypershift.local section

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{deletePoller: newErrorPoller[armprivatedns.PrivateZonesClientDeleteResponse](t, fmt.Errorf("poller failed"))},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	err := r.reconcileDelete(t.Context(), azPLS, testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete Private DNS Zone")))
}

// Tests CR deletion without DNSZoneName, which skips DNS cleanup and only verifies
// PE deletion. Compare with TestReconcile_WhenCRDeletionWithFinalizer which has
// DNSZoneName set and exercises the full DNS cleanup path.
func TestReconcile_WhenCRDeletionSucceeds_ItShouldRemoveFinalizerAndCleanUp(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	now := metav1.Now()
	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.DeletionTimestamp = &now
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{}
	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should clean up PE during CR deletion")
}

func TestReconcile_WhenCRDeletionFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	now := metav1.Now()
	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.DeletionTimestamp = &now
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{deleteErr: fmt.Errorf("PE delete failed")},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{deleteErr: fmt.Errorf("record delete failed")},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to delete resources")))
}

func TestReconcile_WhenGetHCPFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	// CR has PLS alias but no HCP owner reference → getHostedControlPlane fails
	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.OwnerReferences = nil // Remove owner reference

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to get HostedControlPlane")))
}

func TestReconcile_WhenPEReconcileFails_ItShouldReturnRequeueResult(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{getErr: &azcore.ResponseError{StatusCode: 500}},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue after PE error")
}

func TestReconcile_WhenDNSReconcileFails_ItShouldReturnRequeueResult(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.PrivateEndpointID = "/pe/id"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/pe/id"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.5")}},
					},
				},
			},
		},
	}

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{createErr: fmt.Errorf("DNS zone creation failed")},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "private-router", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue after DNS error")
}

func TestReconcile_WhenBaseDomainDNSFails_ItShouldReturnRequeueResult(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "private-router", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"
	azPLS.Status.PrivateEndpointID = "/pe/id"
	azPLS.Spec.BaseDomain = "example.com"

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		Build()

	mockPE := &mockPrivateEndpoints{
		getResponse: armnetwork.PrivateEndpointsClientGetResponse{
			PrivateEndpoint: armnetwork.PrivateEndpoint{
				ID: ptr.To("/pe/id"),
				Properties: &armnetwork.PrivateEndpointProperties{
					CustomDNSConfigs: []*armnetwork.CustomDNSConfigPropertiesFormat{
						{IPAddresses: []*string{ptr.To("10.0.1.5")}},
					},
				},
			},
		},
	}

	mockRecords := &mockRecordSets{
		createErr: fmt.Errorf("A record creation failed"),
	}
	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    mockPE,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          mockRecords,
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "private-router", Namespace: "test-ns"},
	})

	// The first DNS zone (hypershift.local) A record creation will fail
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero())
}

func TestReconcile_WhenFinalizerAddConflicts_ItShouldRequeue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	// No finalizer → Reconcile will try to add one

	conflictErr := apierrors.NewConflict(
		hyperv1.Resource("azureprivatelinkservices"), "test-pls", fmt.Errorf("conflict"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, ok := obj.(*hyperv1.AzurePrivateLinkService); ok {
					return conflictErr
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(time.Second), "should requeue on conflict")
}

func TestEnsureHCPFinalizer_WhenPatchConflicts_ItShouldRequeue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	// No finalizer → will try to add one

	conflictErr := apierrors.NewConflict(
		hyperv1.Resource("hostedcontrolplanes"), "test-hcp", fmt.Errorf("conflict"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hcp).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*hyperv1.HostedControlPlane); ok {
					return conflictErr
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}

	result, err := r.ensureHCPFinalizer(t.Context(), hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(time.Second), "should requeue on conflict")
}

func TestReconcileHCPDeletion_WhenPatchConflicts_ItShouldRequeue(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")

	now := metav1.Now()
	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.DeletionTimestamp = &now
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	conflictErr := apierrors.NewConflict(
		hyperv1.Resource("hostedcontrolplanes"), "test-hcp", fmt.Errorf("conflict"))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if _, ok := obj.(*hyperv1.HostedControlPlane); ok {
					return conflictErr
				}
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateEndpoints:    &mockPrivateEndpoints{},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	result, err := r.reconcileHCPDeletion(t.Context(), azPLS, hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).To(Equal(time.Second), "should requeue on conflict")
}

func TestUpdatePrivateEndpointStatus_WhenStatusPatchFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return fmt.Errorf("status patch failed")
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}

	pe := armnetwork.PrivateEndpoint{
		ID:         ptr.To("/pe/id"),
		Properties: &armnetwork.PrivateEndpointProperties{},
	}

	_, err := r.updatePrivateEndpointStatus(t.Context(), azPLS, pe, "10.0.1.5", testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("status patch failed")))
}

func TestUpdatePrivateEndpointStatus_WhenConnectionPendingAndPatchFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return fmt.Errorf("status patch failed")
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}

	pe := armnetwork.PrivateEndpoint{
		ID: ptr.To("/pe/id"),
		Properties: &armnetwork.PrivateEndpointProperties{
			ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
				{
					Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
						PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
							Status: ptr.To("Pending"),
						},
					},
				},
			},
		},
	}

	_, err := r.updatePrivateEndpointStatus(t.Context(), azPLS, pe, "10.0.1.5", testr.New(t))
	g.Expect(err).To(HaveOccurred())
}

func TestHandleAzureError_WhenStatusPatchFails_ItShouldReturnPatchError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return fmt.Errorf("status patch failed")
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{Client: fakeClient}

	_, err := r.handleAzureError(t.Context(), azPLS, "TestCondition", "TestReason", fmt.Errorf("test error"), testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("status patch failed")))
}

func TestReconcileDNSZone_WhenStatusPatchFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "test-pls", "test-ns")
	azPLS.Status.PrivateEndpointIP = "10.0.1.5"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				return fmt.Errorf("status patch failed")
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client:              fakeClient,
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.reconcileDNS(t.Context(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("status patch failed")))
}

func TestExtractPrivateEndpointIP_WhenNICHasNilElements_ItShouldSkipThem(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)

	pe := armnetwork.PrivateEndpoint{
		Properties: &armnetwork.PrivateEndpointProperties{
			NetworkInterfaces: []*armnetwork.Interface{
				nil, // nil NIC entry
				{
					Properties: &armnetwork.InterfacePropertiesFormat{
						IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
							nil, // nil IP config entry
							{
								Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
									PrivateIPAddress: ptr.To("10.0.1.7"),
								},
							},
						},
					},
				},
			},
		},
	}

	result := extractPrivateEndpointIP(pe)
	g.Expect(result).To(Equal("10.0.1.7"))
}

func TestReconcile_WhenNonPrivateRouterDNSZoneNamePatchFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	// Non-private-router CR with empty DNSZoneName triggers the status patch at Reconcile step 9
	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"
	azPLS.Status.PrivateEndpointID = "/pe/id"
	// DNSZoneName intentionally left empty to trigger the persist path

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	// Allow the first status patch (PE status update) but fail on the second (DNS zone name persist)
	patchCount := 0
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*hyperv1.AzurePrivateLinkService); ok {
					patchCount++
					if patchCount > 1 {
						return fmt.Errorf("simulated status patch failure")
					}
				}
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	// PE get returns existing PE so reconcilePrivateEndpoint just updates status
	r := &AzurePrivateLinkServiceReconciler{
		Client: fakeClient,
		PrivateEndpoints: &mockPrivateEndpoints{
			getResponse: armnetwork.PrivateEndpointsClientGetResponse{
				PrivateEndpoint: armnetwork.PrivateEndpoint{
					ID:   ptr.To("/pe/id"),
					Name: ptr.To("oauth-openshift-pe"),
					Properties: &armnetwork.PrivateEndpointProperties{
						ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Approved"),
								},
							},
						}},
						NetworkInterfaces: []*armnetwork.Interface{{
							Properties: &armnetwork.InterfacePropertiesFormat{
								IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
									Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
										PrivateIPAddress: ptr.To("10.0.1.6"),
									},
								}},
							},
						}},
					},
				},
			},
		},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"},
	})

	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to persist DNS zone name in status")))
}

func TestReconcile_WhenAvailableConditionPatchFails_ItShouldReturnError(t *testing.T) {
	t.Parallel()
	g := NewGomegaWithT(t)
	scheme := newTestScheme(t, g)

	azPLS := newTestAzurePLS(t, "oauth-openshift", "test-ns")
	azPLS.Finalizers = []string{azurePrivateLinkServiceFinalizer}
	azPLS.Status.PrivateLinkServiceAlias = "test-alias"
	azPLS.Status.PrivateEndpointIP = "10.0.1.6"
	azPLS.Status.PrivateEndpointID = "/pe/id"
	azPLS.Status.DNSZoneName = "test-hcp.hypershift.local" // Already set, skips persist path

	hcp := newTestHCP(t, "test-hcp", "test-ns", "api.test.example.com")
	hcp.Finalizers = []string{hcpAzurePLSFinalizerName}

	// Allow the first status patch (PE status update) but fail on the Available condition patch
	patchCount := 0
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(azPLS, hcp).
		WithStatusSubresource(azPLS).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*hyperv1.AzurePrivateLinkService); ok {
					patchCount++
					if patchCount > 1 {
						return fmt.Errorf("simulated Available condition patch failure")
					}
				}
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &AzurePrivateLinkServiceReconciler{
		Client: fakeClient,
		PrivateEndpoints: &mockPrivateEndpoints{
			getResponse: armnetwork.PrivateEndpointsClientGetResponse{
				PrivateEndpoint: armnetwork.PrivateEndpoint{
					ID:   ptr.To("/pe/id"),
					Name: ptr.To("oauth-openshift-pe"),
					Properties: &armnetwork.PrivateEndpointProperties{
						ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{{
							Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
								PrivateLinkServiceConnectionState: &armnetwork.PrivateLinkServiceConnectionState{
									Status: ptr.To("Approved"),
								},
							},
						}},
						NetworkInterfaces: []*armnetwork.Interface{{
							Properties: &armnetwork.InterfacePropertiesFormat{
								IPConfigurations: []*armnetwork.InterfaceIPConfiguration{{
									Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
										PrivateIPAddress: ptr.To("10.0.1.6"),
									},
								}},
							},
						}},
					},
				},
			},
		},
		PrivateDNSZones:     &mockPrivateDNSZones{},
		VirtualNetworkLinks: &mockVirtualNetworkLinks{},
		RecordSets:          &mockRecordSets{},
	}

	_, err := r.Reconcile(log.IntoContext(t.Context(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"},
	})

	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(MatchError(ContainSubstring("failed to update Available condition")))
}
