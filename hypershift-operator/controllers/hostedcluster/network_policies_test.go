package hostedcluster

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
)

func TestReconcileNetworkPolicies_GCP_PrivateRouter(t *testing.T) {
	// Create test GCP HostedCluster
	hcluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gcp-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gcp-cluster",
			Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
		},
	}

	// Create test environment
	//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
	kubernetesEndpoint := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes",
			Namespace: "default",
		},
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	managementClusterNetwork := &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
	}

	// Setup fake client
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add hyperv1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}
	if err := configv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add configv1 scheme: %v", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add networkingv1 scheme: %v", err)
	}

	objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	reconciler := &HostedClusterReconciler{
		Client:                        fakeClient,
		ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
	}

	// Track created network policies
	createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
	createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, client client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
			if err := f(); err != nil {
				return controllerutil.OperationResultNone, err
			}
			createdNetworkPolicies[netPol.Name] = netPol
		}
		return controllerutil.OperationResultCreated, nil
	})

	// Execute the test
	ctx := context.Background()
	log := ctrl.Log.WithName("test-gcp")
	version := semver.MustParse("4.15.0")

	err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, version, false)
	if err != nil {
		t.Fatalf("reconcileNetworkPolicies failed: %v", err)
	}

	// Verify private-router NetworkPolicy is created for GCP
	privateRouterPolicy, exists := createdNetworkPolicies["private-router"]
	if !exists {
		t.Error("Expected private-router NetworkPolicy to be created for GCP platform")
	} else {
		verifyPrivateRouterNetworkPolicy(t, privateRouterPolicy)
	}

	// Verify core policies are created
	expectedPolicies := []string{"openshift-ingress", "same-namespace", "kas", "openshift-monitoring"}
	for _, policyName := range expectedPolicies {
		if _, exists := createdNetworkPolicies[policyName]; !exists {
			t.Errorf("Expected %s NetworkPolicy to be created", policyName)
		}
	}
}

// verifyPrivateRouterNetworkPolicy verifies that the private-router NetworkPolicy has the correct configuration
func verifyPrivateRouterNetworkPolicy(t *testing.T, policy *networkingv1.NetworkPolicy) {
	// Verify policy types
	expectedTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	if len(policy.Spec.PolicyTypes) != len(expectedTypes) {
		t.Errorf("Expected %d policy types, got %d", len(expectedTypes), len(policy.Spec.PolicyTypes))
	}
	for i, expectedType := range expectedTypes {
		if i >= len(policy.Spec.PolicyTypes) || policy.Spec.PolicyTypes[i] != expectedType {
			t.Errorf("Expected policy type %s at index %d, got %s", expectedType, i, policy.Spec.PolicyTypes[i])
		}
	}

	// Verify pod selector
	expectedLabels := map[string]string{"app": "private-router"}
	if len(policy.Spec.PodSelector.MatchLabels) != len(expectedLabels) {
		t.Errorf("Expected %d pod selector labels, got %d", len(expectedLabels), len(policy.Spec.PodSelector.MatchLabels))
	}
	for key, expectedValue := range expectedLabels {
		if actualValue, exists := policy.Spec.PodSelector.MatchLabels[key]; !exists || actualValue != expectedValue {
			t.Errorf("Expected pod selector label %s=%s, got %s=%s", key, expectedValue, key, actualValue)
		}
	}

	// Verify ingress rules
	if len(policy.Spec.Ingress) == 0 {
		t.Error("Expected at least one ingress rule")
	} else {
		ingressRule := policy.Spec.Ingress[0]
		if len(ingressRule.Ports) != 2 {
			t.Errorf("Expected 2 ingress ports, got %d", len(ingressRule.Ports))
		} else {
			// Check for ports 8080 and 8443
			expectedPorts := []int32{8080, 8443}
			actualPorts := make([]int32, len(ingressRule.Ports))
			for i, port := range ingressRule.Ports {
				if port.Port != nil {
					actualPorts[i] = port.Port.IntVal
				}
			}
			for _, expectedPort := range expectedPorts {
				found := false
				for _, actualPort := range actualPorts {
					if actualPort == expectedPort {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected ingress port %d not found", expectedPort)
				}
			}
		}
	}

	// Verify egress rules exist (detailed verification would require more complex setup)
	if len(policy.Spec.Egress) == 0 {
		t.Error("Expected at least one egress rule")
	}
}

func TestGCPPrivateRouterNetworkPolicy_IngressOnly(t *testing.T) {
	// Test GCP platform with ingressOnly parameter functionality
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
			},
		},
	}

	//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
	kubernetesEndpoint := &corev1.Endpoints{
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	policy := networkpolicy.PrivateRouterNetworkPolicy("test-namespace")

	// Test with ingressOnly = true
	err := reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, false, nil, true)
	if err != nil {
		t.Fatalf("reconcilePrivateRouterNetworkPolicy with ingressOnly=true failed: %v", err)
	}

	// Verify only ingress policy type is set
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Error("Expected only Ingress policy type when ingressOnly=true")
	}

	// Verify no egress rules
	if len(policy.Spec.Egress) != 0 {
		t.Error("Expected no egress rules when ingressOnly=true")
	}

	// Verify GCP-specific port configuration
	if len(policy.Spec.Ingress) > 0 {
		ingressRule := policy.Spec.Ingress[0]
		if len(ingressRule.Ports) == 2 {
			// Verify ports 8080 and 8443 are present
			foundPorts := make(map[int32]bool)
			for _, port := range ingressRule.Ports {
				if port.Port != nil {
					foundPorts[port.Port.IntVal] = true
				}
			}
			if !foundPorts[8080] || !foundPorts[8443] {
				t.Error("Expected GCP private router to have ports 8080 and 8443")
			}
		}
	}
}
