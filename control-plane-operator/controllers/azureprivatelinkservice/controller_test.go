package azureprivatelinkservice

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

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
}

func (m *mockPrivateEndpoints) BeginCreateOrUpdate(_ context.Context, resourceGroupName string, privateEndpointName string, parameters armnetwork.PrivateEndpoint, _ *armnetwork.PrivateEndpointsClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastCreateParams = parameters
	m.lastCreateName = privateEndpointName
	m.lastCreateRG = resourceGroupName
	if m.createErr != nil {
		return nil, m.createErr
	}
	// Return a nil poller; tests use PollResult to complete
	return nil, nil
}

func (m *mockPrivateEndpoints) BeginDelete(_ context.Context, resourceGroupName string, privateEndpointName string, _ *armnetwork.PrivateEndpointsClientBeginDeleteOptions) (*azruntime.Poller[armnetwork.PrivateEndpointsClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastDeleteName = privateEndpointName
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return nil, nil
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
}

func (m *mockPrivateDNSZones) BeginCreateOrUpdate(_ context.Context, _ string, privateZoneName string, _ armprivatedns.PrivateZone, _ *armprivatedns.PrivateZonesClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastZoneName = privateZoneName
	if m.createErr != nil {
		return nil, m.createErr
	}
	return nil, nil
}

func (m *mockPrivateDNSZones) BeginDelete(_ context.Context, _ string, privateZoneName string, _ *armprivatedns.PrivateZonesClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.PrivateZonesClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastZoneName = privateZoneName
	m.deletedZoneNames = append(m.deletedZoneNames, privateZoneName)
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return nil, nil
}

type mockVirtualNetworkLinks struct {
	createErr        error
	deleteErr        error
	createCalled     bool
	deleteCalled     bool
	lastLinkName     string
	deletedLinkNames []string
}

func (m *mockVirtualNetworkLinks) BeginCreateOrUpdate(_ context.Context, _ string, _ string, virtualNetworkLinkName string, _ armprivatedns.VirtualNetworkLink, _ *armprivatedns.VirtualNetworkLinksClientBeginCreateOrUpdateOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientCreateOrUpdateResponse], error) {
	m.createCalled = true
	m.lastLinkName = virtualNetworkLinkName
	if m.createErr != nil {
		return nil, m.createErr
	}
	return nil, nil
}

func (m *mockVirtualNetworkLinks) BeginDelete(_ context.Context, _ string, _ string, virtualNetworkLinkName string, _ *armprivatedns.VirtualNetworkLinksClientBeginDeleteOptions) (*azruntime.Poller[armprivatedns.VirtualNetworkLinksClientDeleteResponse], error) {
	m.deleteCalled = true
	m.lastLinkName = virtualNetworkLinkName
	m.deletedLinkNames = append(m.deletedLinkNames, virtualNetworkLinkName)
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	return nil, nil
}

type mockRecordSets struct {
	createErr          error
	deleteErr          error
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

func (m *mockRecordSets) Delete(_ context.Context, _ string, _ string, recordType armprivatedns.RecordType, relativeRecordSetName string, _ *armprivatedns.RecordSetsClientDeleteOptions) (armprivatedns.RecordSetsClientDeleteResponse, error) {
	m.deleteCalled = true
	m.deleteCallCount++
	m.deletedRecordNames = append(m.deletedRecordNames, relativeRecordSetName)
	m.lastRecordSetName = relativeRecordSetName
	m.lastRecordType = recordType
	return armprivatedns.RecordSetsClientDeleteResponse{}, m.deleteErr
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when PLS alias is not available")
}

func TestReconcile_WhenPLSAliasIsAvailable_ItShouldCreatePrivateEndpoint(t *testing.T) {
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

	_, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
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
	err := r.reconcileDelete(context.Background(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred()) // reconcileDelete is best-effort, always returns nil

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
	result, err := r.reconcilePrivateEndpoint(context.Background(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should not requeue after successful PE reconciliation")

	// Verify status was updated
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())

	// Check PE Available condition
	var peCondition *metav1.Condition
	for i := range updated.Status.Conditions {
		if updated.Status.Conditions[i].Type == string(hyperv1.AzurePrivateEndpointAvailable) {
			peCondition = &updated.Status.Conditions[i]
			break
		}
	}
	g.Expect(peCondition).ToNot(BeNil(), "PE Available condition should be set")
	g.Expect(peCondition.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(peCondition.Reason).To(Equal(hyperv1.AzurePLSSuccessReason))
}

func TestReconcile_WhenFinalizerNotPresent_ItShouldAddFinalizer(t *testing.T) {
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should return zero result after adding finalizer")

	// Verify finalizer was added
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Finalizers).To(ContainElement(azurePrivateLinkServiceFinalizer), "finalizer should be added")
}

func TestReconcileDNS_WhenPEIPNotAvailable_ItShouldRequeue(t *testing.T) {
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

	result, err := r.reconcileDNS(context.Background(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when PE IP is not available")
}

func TestReconcileDelete_WhenDNSZoneNameNotSet_ItShouldSkipDNSCleanup(t *testing.T) {
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

	err := r.reconcileDelete(context.Background(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())

	// DNS cleanup should be skipped since DNSZoneName is not set in status
	g.Expect(mockDNS.deleteCalled).To(BeFalse(), "should skip DNS zone deletion when DNSZoneName not set")
	g.Expect(mockLinks.deleteCalled).To(BeFalse(), "should skip VNet link deletion when DNSZoneName not set")
	g.Expect(mockRecords.deleteCalled).To(BeFalse(), "should skip A record deletion when DNSZoneName not set")

	// PE should still be cleaned up
	g.Expect(mockPE.deleteCalled).To(BeTrue(), "should still attempt PE deletion even when DNSZoneName not set")
}

func TestGetHostedControlPlane(t *testing.T) {
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
			hcp, err := r.getHostedControlPlane(context.Background(), tt.azPLS)

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

	result, err := r.reconcilePrivateEndpoint(context.Background(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify PE was NOT created (only Get was called)
	g.Expect(mockPE.getCalled).To(BeTrue(), "should call Get")
	g.Expect(mockPE.createCalled).To(BeFalse(), "should NOT call Create when PE already exists")

	// Verify status was updated with existing PE info
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Status.PrivateEndpointID).To(Equal("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateEndpoints/existing-pe"))
	g.Expect(updated.Status.PrivateEndpointIP).To(Equal("10.0.2.10"))
}

func TestReconcile_WhenCRNotFound_ItShouldReturnNoError(t *testing.T) {
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "test-ns"},
	})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())
}

func TestReconcile_WhenCRAlreadyDeleted_ItShouldReturnNoError(t *testing.T) {
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
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

	result, err := r.ensureHCPFinalizer(context.Background(), hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue(), "should return zero result after adding HCP finalizer")

	// Verify finalizer was added to HCP
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP should have the Azure PLS finalizer")
}

func TestEnsureHCPFinalizer_WhenAlreadyPresent_ItShouldNotModify(t *testing.T) {
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

	result, err := r.ensureHCPFinalizer(context.Background(), hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify finalizer is still present and only once
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(Equal([]string{hcpAzurePLSFinalizerName}))
}

func TestReconcile_WhenPLSAliasIsAvailable_ItShouldAddHCPFinalizer(t *testing.T) {
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

	_, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-pls", Namespace: "test-ns"},
	})
	g.Expect(err).ToNot(HaveOccurred())

	// Verify HCP finalizer was added
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP should have the Azure PLS finalizer after reconciliation with PLS alias available")
}

func TestReconcileHCPDeletion_WhenHCPIsBeingDeleted_ItShouldCleanUpAndRemoveFinalizer(t *testing.T) {
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
	result, err := r.reconcileHCPDeletion(context.Background(), azPLS, hcp, testr.New(t))
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
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	if apierrors.IsNotFound(err) {
		// Expected: fake client deleted the HCP because all finalizers were removed
		return
	}
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).ToNot(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be removed after cleanup")
}

func TestReconcileHCPDeletion_WhenHCPDoesNotHaveFinalizer_ItShouldBeNoOp(t *testing.T) {
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

	result, err := r.reconcileHCPDeletion(context.Background(), azPLS, hcp, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// Verify no Azure cleanup was attempted
	g.Expect(mockPE.deleteCalled).To(BeFalse(), "should not attempt PE deletion when HCP finalizer not present")
	g.Expect(mockDNS.deleteCalled).To(BeFalse(), "should not attempt DNS deletion when HCP finalizer not present")
}

func TestReconcileHCPDeletion_WhenAzureCleanupFails_ItShouldReturnErrorAndPreserveFinalizer(t *testing.T) {
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

	_, err := r.reconcileHCPDeletion(context.Background(), azPLS, hcp, testr.New(t))
	g.Expect(err).To(HaveOccurred(), "should return error when Azure cleanup fails")
	g.Expect(err.Error()).To(ContainSubstring("failed to clean up Azure resources during HCP deletion"))

	// Verify the HCP finalizer was NOT removed
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).To(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be preserved when cleanup fails")
}

func TestReconcile_WhenHCPIsBeingDeleted_ItShouldTriggerCleanupInsteadOfCreation(t *testing.T) {
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
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
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-hcp", Namespace: "test-ns"}, updatedHCP)
	if apierrors.IsNotFound(err) {
		// Expected: fake client deleted the HCP because all finalizers were removed
		return
	}
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updatedHCP.Finalizers).ToNot(ContainElement(hcpAzurePLSFinalizerName), "HCP finalizer should be removed")
}

func TestReconcile_WhenPEConnectionNotApproved_ItShouldRequeueWithWarning(t *testing.T) {
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

	result, err := r.reconcilePrivateEndpoint(context.Background(), azPLS, testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.RequeueAfter).ToNot(BeZero(), "should requeue when connection not approved")

	// Check condition was set to False with appropriate reason
	updated := &hyperv1.AzurePrivateLinkService{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "test-pls", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())

	var peCondition *metav1.Condition
	for i := range updated.Status.Conditions {
		if updated.Status.Conditions[i].Type == string(hyperv1.AzurePrivateEndpointAvailable) {
			peCondition = &updated.Status.Conditions[i]
			break
		}
	}
	g.Expect(peCondition).ToNot(BeNil())
	g.Expect(peCondition.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(peCondition.Reason).To(Equal("PrivateEndpointConnectionNotApproved"))
}

func TestReconcile_WhenNonPrivateRouterCR_ItShouldSkipHypershiftLocalDNS(t *testing.T) {
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

	result, err := r.Reconcile(log.IntoContext(context.Background(), testr.New(t)), ctrl.Request{
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
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "oauth-openshift", Namespace: "test-ns"}, updated)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(updated.Status.DNSZoneName).To(Equal("test-hcp.hypershift.local"), "non-private-router CRs should persist DNSZoneName for delete-path cleanup")
}

func TestReconcileBaseDomainDNS_WhenPrivateRouterWithNoSibling_ItShouldCreateBothRecords(t *testing.T) {
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

	result, err := r.reconcileBaseDomainDNS(context.Background(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// When no sibling OAuth CR exists, private-router should create both api and oauth records
	g.Expect(mockRecords.createCallCount).To(Equal(2), "should create two A records (api and oauth)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api-test-hcp", "oauth-test-hcp"), "should create api and oauth base domain records")
}

func TestReconcileBaseDomainDNS_WhenPrivateRouterWithSiblingOAuth_ItShouldOnlyCreateAPIRecord(t *testing.T) {
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

	result, err := r.reconcileBaseDomainDNS(context.Background(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// When a sibling OAuth CR exists, private-router should only create the api record
	g.Expect(mockRecords.createCallCount).To(Equal(1), "should create only one A record (api)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("api-test-hcp"), "should only create api base domain record")
}

func TestReconcileBaseDomainDNS_WhenOAuthCR_ItShouldOnlyCreateOAuthRecord(t *testing.T) {
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

	result, err := r.reconcileBaseDomainDNS(context.Background(), azPLS, "test-hcp", testr.New(t))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.IsZero()).To(BeTrue())

	// OAuth CR should only create the oauth record
	g.Expect(mockRecords.createCallCount).To(Equal(1), "should create only one A record (oauth)")
	g.Expect(mockRecords.createdRecordNames).To(ConsistOf("oauth-test-hcp"), "should only create oauth base domain record")
}

func TestReconcileDelete_WhenSiblingCRsExist_ItShouldNotDeleteBaseDomainZone(t *testing.T) {
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

	err := r.reconcileDelete(context.Background(), azPLS, testr.New(t))
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

	err := r.reconcileDelete(context.Background(), azPLS, testr.New(t))
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
			has, err := r.hasSiblingCR(context.Background(), tt.azPLS)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(has).To(Equal(tt.expectHas))
			}
		})
	}
}
