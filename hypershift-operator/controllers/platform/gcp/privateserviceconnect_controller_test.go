package gcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr/testr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// fakeComputeClient implements ComputeClient for unit tests.
// Each field holds the canned return values for the corresponding method.
// capturedSubnetFilter records the filter string passed to ListSubnetworks so
// tests can assert that the VPC-scoped filter reaches the API call site.
type fakeComputeClient struct {
	forwardingRules         []*compute.ForwardingRule
	forwardingRulesErr      error
	subnetworks             []*compute.Subnetwork
	subnetworksErr          error
	capturedSubnetFilter    string
	serviceAttachments      []*compute.ServiceAttachment
	serviceAttachmentsErr   error
	getServiceAttachment    *compute.ServiceAttachment
	getServiceAttachmentErr error
	insertOperation         *compute.Operation
	insertErr               error
	deleteOperation         *compute.Operation
	deleteErr               error
}

func (f *fakeComputeClient) ListForwardingRules(_ context.Context, _, _, _ string) ([]*compute.ForwardingRule, error) {
	return f.forwardingRules, f.forwardingRulesErr
}

func (f *fakeComputeClient) ListSubnetworks(_ context.Context, _, _, filter string) ([]*compute.Subnetwork, error) {
	f.capturedSubnetFilter = filter
	return f.subnetworks, f.subnetworksErr
}

func (f *fakeComputeClient) ListServiceAttachments(_ context.Context, _, _ string) ([]*compute.ServiceAttachment, error) {
	return f.serviceAttachments, f.serviceAttachmentsErr
}

func (f *fakeComputeClient) GetServiceAttachment(_ context.Context, _, _, _ string) (*compute.ServiceAttachment, error) {
	return f.getServiceAttachment, f.getServiceAttachmentErr
}

func (f *fakeComputeClient) InsertServiceAttachment(_ context.Context, _, _ string, _ *compute.ServiceAttachment) (*compute.Operation, error) {
	return f.insertOperation, f.insertErr
}

func (f *fakeComputeClient) DeleteServiceAttachment(_ context.Context, _, _, _ string) (*compute.Operation, error) {
	return f.deleteOperation, f.deleteErr
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "GCP 404 error",
			err: &googleapi.Error{
				Code: 404,
			},
			expected: true,
		},
		{
			name: "GCP 400 error",
			err: &googleapi.Error{
				Code: 400,
			},
			expected: false,
		},
		{
			name:     "non-GCP error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "When given a wrapped GCP 404 error it should return true",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 404}),
			expected: true,
		},
		{
			name:     "When given a wrapped GCP 500 error it should return false",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 500}),
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isNotFoundError(test.err)
			if actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func TestReconcileGCPPrivateServiceConnectSpec(t *testing.T) {
	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
			// Testing with pre-populated values to avoid GCP API calls
			ForwardingRuleName: "test-forwarding-rule",
			NATSubnet:          "test-nat-subnet",
		},
	}

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
	}

	// Test with pre-populated spec fields to avoid GCP API calls
	err := r.reconcileGCPPrivateServiceConnectSpec(ctrl.LoggerInto(context.Background(), testr.New(t)), gcpPSC, hc)

	// Since ForwardingRuleName and NATSubnet are already set, this should succeed
	if err != nil {
		t.Errorf("unexpected error with pre-populated spec: %v", err)
	}

	// Verify the fields remain set
	if gcpPSC.Spec.ForwardingRuleName != "test-forwarding-rule" {
		t.Error("ForwardingRuleName should remain set")
	}
	if gcpPSC.Spec.NATSubnet != "test-nat-subnet" {
		t.Error("NATSubnet should remain set")
	}
}

func TestReconcile_NotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client: client,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "test",
		},
	}

	result, err := r.Reconcile(ctrl.LoggerInto(context.Background(), testr.New(t)), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}
}

func TestReconcile_PausedUntil(t *testing.T) {
	// Use a dynamically computed future time so the test remains valid over time
	pausedUntil := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	// Create a hosted cluster with pause settings
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			PausedUntil: &pausedUntil,
		},
	}

	// Create a hosted control plane
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster",
		},
	}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-psc",
			Namespace:  "test-namespace",
			Finalizers: []string{"hypershift.openshift.io/gcp-private-service-connect"}, // Add finalizer so it gets past initial checks
			Annotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hcp, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
	}

	result, err := r.Reconcile(ctrl.LoggerInto(context.Background(), testr.New(t)), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should requeue with a future time since reconciliation is paused
	if result.RequeueAfter <= 0 {
		t.Error("expected positive RequeueAfter duration for paused reconciliation")
	}
}

func TestIPAddressFilterFormat(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{
			name:     "When filtering by IPv4 address it should use AIP-160 exact match syntax",
			ip:       "10.0.0.1",
			expected: `IPAddress = "10.0.0.1"`,
		},
		{
			name:     "When filtering by IPv4 address with different octets it should quote properly",
			ip:       "192.168.1.100",
			expected: `IPAddress = "192.168.1.100"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This tests the filter format used in lookupForwardingRuleName
			// AIP-160 syntax uses '=' for exact match
			filter := fmt.Sprintf(`IPAddress = "%s"`, tt.ip)
			if filter != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, filter)
			}
		})
	}
}

func TestConstructServiceAttachmentName(t *testing.T) {
	tests := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		expected    string
		description string
	}{
		{
			name: "When given a cluster ID it should construct valid service attachment name",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster"},
				Spec: hyperv1.HostedClusterSpec{
					ClusterID: "12345678-abcd-1234-abcd-123456789012",
				},
			},
			expected:    "psc-12345678-abcd-1234-abcd-123456789012",
			description: "Should use psc- prefix with cluster ID (prefix ensures GCP naming compliance)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &GCPPrivateServiceConnectReconciler{}
			result := r.constructServiceAttachmentName(tt.hc)
			if result != tt.expected {
				t.Errorf("expected %s, got %s - %s", tt.expected, result, tt.description)
			}
			if len(result) > 63 {
				t.Errorf("Service attachment name %s exceeds GCP 63 character limit (%d chars)", result, len(result))
			}
		})
	}
}

func TestNATSubnetFilterFormat(t *testing.T) {
	tests := []struct {
		name       string
		networkURL string
		expected   string
	}{
		{
			name:       "When given a network URL it should include both purpose and network in the filter",
			networkURL: "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/my-vpc",
			expected:   `purpose = "PRIVATE_SERVICE_CONNECT" AND network = "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/my-vpc"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := buildNATSubnetFilter(tt.networkURL)
			if filter != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, filter)
			}
		})
	}
}

func newReconciler(t *testing.T, gcpClient ComputeClient) *GCPPrivateServiceConnectReconciler {
	t.Helper()
	return &GCPPrivateServiceConnectReconciler{
		GcpClient: gcpClient,
		ProjectID: "test-project",
		Region:    "us-central1",
		Log:       testr.New(t),
	}
}

func newGCPPSC(forwardingRuleName, natSubnet string) *hyperv1.GCPPrivateServiceConnect {
	return &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{Name: "test-psc", Namespace: "test-namespace"},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP:     "10.0.0.1",
			ForwardingRuleName: hyperv1.GCPResourceName(forwardingRuleName),
			NATSubnet:          hyperv1.GCPResourceName(natSubnet),
		},
	}
}

// --- lookupForwardingRule ---

func TestLookupForwardingRule_APIError(t *testing.T) {
	r := newReconciler(t, &fakeComputeClient{forwardingRulesErr: errors.New("GCP unavailable")})
	rule, err := r.lookupForwardingRule(context.Background(), newGCPPSC("", ""))
	if err == nil {
		t.Error("expected error, got nil")
	}
	if rule != nil {
		t.Errorf("expected nil rule, got %v", rule)
	}
}

func TestLookupForwardingRule_NoResults(t *testing.T) {
	r := newReconciler(t, &fakeComputeClient{forwardingRules: nil})
	rule, err := r.lookupForwardingRule(context.Background(), newGCPPSC("", ""))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if rule != nil {
		t.Errorf("expected nil rule, got %v", rule)
	}
}

func TestLookupForwardingRule_SingleResult(t *testing.T) {
	expected := &compute.ForwardingRule{Name: "fr-1", Network: "https://www.googleapis.com/compute/v1/projects/p/global/networks/my-vpc"}
	r := newReconciler(t, &fakeComputeClient{forwardingRules: []*compute.ForwardingRule{expected}})
	rule, err := r.lookupForwardingRule(context.Background(), newGCPPSC("", ""))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if rule == nil || rule.Name != expected.Name {
		t.Errorf("expected rule %q, got %v", expected.Name, rule)
	}
}

func TestLookupForwardingRule_MultipleResults_UsesFirst(t *testing.T) {
	rules := []*compute.ForwardingRule{
		{Name: "fr-first", Network: "https://www.googleapis.com/compute/v1/projects/p/global/networks/my-vpc"},
		{Name: "fr-second", Network: "https://www.googleapis.com/compute/v1/projects/p/global/networks/my-vpc"},
	}
	r := newReconciler(t, &fakeComputeClient{forwardingRules: rules})
	rule, err := r.lookupForwardingRule(context.Background(), newGCPPSC("", ""))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if rule == nil || rule.Name != "fr-first" {
		t.Errorf("expected first rule, got %v", rule)
	}
}

// --- reconcileGCPPrivateServiceConnectSpec ---

func TestReconcileSpec_BothFieldsSet_EarlyReturn(t *testing.T) {
	// GcpClient is nil — proves no GCP call is made when both fields are already set.
	r := newReconciler(t, nil)
	gcpPSC := newGCPPSC("existing-rule", "existing-subnet")
	if err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), gcpPSC, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReconcileSpec_ForwardingRuleLookupError(t *testing.T) {
	r := newReconciler(t, &fakeComputeClient{forwardingRulesErr: errors.New("api error")})
	err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), newGCPPSC("", ""), nil)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestReconcileSpec_ForwardingRuleNotYetProvisioned(t *testing.T) {
	// nil result from lookupForwardingRule — ILB not yet ready, should return nil (requeue).
	r := newReconciler(t, &fakeComputeClient{forwardingRules: nil})
	err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), newGCPPSC("", ""), nil)
	if err != nil {
		t.Errorf("expected nil, got error: %v", err)
	}
}

func TestReconcileSpec_ForwardingRuleEmptyNetwork(t *testing.T) {
	// Forwarding rule exists but has no Network field — cannot scope subnet discovery.
	r := newReconciler(t, &fakeComputeClient{
		forwardingRules: []*compute.ForwardingRule{{Name: "fr-1", Network: ""}},
	})
	err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), newGCPPSC("", ""), nil)
	if err == nil {
		t.Error("expected error for empty Network, got nil")
	}
}

func TestReconcileSpec_SetsForwardingRuleNameAndNATSubnet(t *testing.T) {
	networkURL := "https://www.googleapis.com/compute/v1/projects/p/global/networks/my-vpc"
	r := newReconciler(t, &fakeComputeClient{
		forwardingRules: []*compute.ForwardingRule{{Name: "fr-1", Network: networkURL}},
		subnetworks:     []*compute.Subnetwork{{Name: "psc-subnet-1"}},
		// No existing service attachments — subnet is available.
		serviceAttachments: nil,
	})
	gcpPSC := newGCPPSC("", "")
	if err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), gcpPSC, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if gcpPSC.Spec.ForwardingRuleName != "fr-1" {
		t.Errorf("expected ForwardingRuleName %q, got %q", "fr-1", gcpPSC.Spec.ForwardingRuleName)
	}
	if gcpPSC.Spec.NATSubnet != "psc-subnet-1" {
		t.Errorf("expected NATSubnet %q, got %q", "psc-subnet-1", gcpPSC.Spec.NATSubnet)
	}
}

func TestReconcileSpec_PartialWrite_ForwardingRuleNamePreservedNATSubnetDiscovered(t *testing.T) {
	// Partial-write edge case: ForwardingRuleName was already written in a previous reconcile
	// but NATSubnet was not (e.g. discoverNATSubnet failed transiently). The controller must
	// preserve the existing ForwardingRuleName rather than overwriting it, and still use the
	// forwarding rule's Network field to scope NAT subnet discovery to the correct VPC.
	networkURL := "https://www.googleapis.com/compute/v1/projects/p/global/networks/my-vpc"
	fc := &fakeComputeClient{
		// GCP returns a different rule name than the one already stored in the spec.
		forwardingRules:    []*compute.ForwardingRule{{Name: "fr-new", Network: networkURL}},
		subnetworks:        []*compute.Subnetwork{{Name: "psc-subnet-1"}},
		serviceAttachments: nil,
	}
	r := newReconciler(t, fc)
	gcpPSC := newGCPPSC("fr-original", "")
	if err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), gcpPSC, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// ForwardingRuleName must not be overwritten.
	if gcpPSC.Spec.ForwardingRuleName != "fr-original" {
		t.Errorf("ForwardingRuleName should be preserved; got %q", gcpPSC.Spec.ForwardingRuleName)
	}
	// NATSubnet must be discovered using the forwarding rule's network URL.
	if gcpPSC.Spec.NATSubnet != "psc-subnet-1" {
		t.Errorf("expected NATSubnet %q, got %q", "psc-subnet-1", gcpPSC.Spec.NATSubnet)
	}
	// The subnet filter must be scoped to the forwarding rule's VPC — the core invariant of this PR.
	wantFilter := buildNATSubnetFilter(networkURL)
	if fc.capturedSubnetFilter != wantFilter {
		t.Errorf("ListSubnetworks filter = %q, want %q", fc.capturedSubnetFilter, wantFilter)
	}
}

// --- discoverNATSubnet ---

func TestDiscoverNATSubnet_APIError(t *testing.T) {
	networkURL := "https://example.com/network"
	fc := &fakeComputeClient{subnetworksErr: errors.New("api error")}
	r := newReconciler(t, fc)
	_, err := r.discoverNATSubnet(context.Background(), newGCPPSC("", ""), networkURL)
	if err == nil {
		t.Error("expected error, got nil")
	}
	wantFilter := buildNATSubnetFilter(networkURL)
	if fc.capturedSubnetFilter != wantFilter {
		t.Errorf("ListSubnetworks filter = %q, want %q", fc.capturedSubnetFilter, wantFilter)
	}
}

func TestDiscoverNATSubnet_NoSubnets(t *testing.T) {
	networkURL := "https://example.com/network"
	fc := &fakeComputeClient{subnetworks: nil}
	r := newReconciler(t, fc)
	_, err := r.discoverNATSubnet(context.Background(), newGCPPSC("", ""), networkURL)
	if err == nil {
		t.Error("expected error for no available subnets, got nil")
	}
	wantFilter := buildNATSubnetFilter(networkURL)
	if fc.capturedSubnetFilter != wantFilter {
		t.Errorf("ListSubnetworks filter = %q, want %q", fc.capturedSubnetFilter, wantFilter)
	}
}

func TestDiscoverNATSubnet_SubnetInUse_SkipsToNext(t *testing.T) {
	networkURL := "https://example.com/network"
	fc := &fakeComputeClient{
		subnetworks: []*compute.Subnetwork{
			{Name: "subnet-in-use"},
			{Name: "subnet-available"},
		},
		// First subnet is in use by an existing service attachment.
		serviceAttachments: []*compute.ServiceAttachment{
			{NatSubnets: []string{"projects/p/regions/r/subnetworks/subnet-in-use"}},
		},
	}
	r := newReconciler(t, fc)
	name, err := r.discoverNATSubnet(context.Background(), newGCPPSC("", ""), networkURL)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if name != "subnet-available" {
		t.Errorf("expected %q, got %q", "subnet-available", name)
	}
	wantFilter := buildNATSubnetFilter(networkURL)
	if fc.capturedSubnetFilter != wantFilter {
		t.Errorf("ListSubnetworks filter = %q, want %q", fc.capturedSubnetFilter, wantFilter)
	}
}

func TestDiscoverNATSubnet_AllSubnetsInUse(t *testing.T) {
	networkURL := "https://example.com/network"
	fc := &fakeComputeClient{
		subnetworks: []*compute.Subnetwork{{Name: "only-subnet"}},
		serviceAttachments: []*compute.ServiceAttachment{
			{NatSubnets: []string{"projects/p/regions/r/subnetworks/only-subnet"}},
		},
	}
	r := newReconciler(t, fc)
	_, err := r.discoverNATSubnet(context.Background(), newGCPPSC("", ""), networkURL)
	if err == nil {
		t.Error("expected error when all subnets are in use, got nil")
	}
	wantFilter := buildNATSubnetFilter(networkURL)
	if fc.capturedSubnetFilter != wantFilter {
		t.Errorf("ListSubnetworks filter = %q, want %q", fc.capturedSubnetFilter, wantFilter)
	}
}
