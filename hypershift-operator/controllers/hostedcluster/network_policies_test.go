package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
	"github.com/openshift/hypershift/support/capabilities"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func TestReconcileNetworkPolicies_OpenshiftIngressPolicy(t *testing.T) {
	tests := []struct {
		name          string
		hcluster      *hyperv1.HostedCluster
		hcp           *hyperv1.HostedControlPlane
		expectCreated bool
		expectDeleted bool
	}{
		{
			name: "When AWS public cluster uses KAS LoadBalancer it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When AWS private cluster uses KAS LoadBalancer it should delete policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Private}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "When AWS PublicAndPrivate cluster uses KAS LoadBalancer it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.PublicAndPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.PublicAndPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When AWS public cluster uses KAS Route with hostname it should delete policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:  hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
						}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:  hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
						}},
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "When GCP PublicAndPrivate cluster uses KAS LoadBalancer it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{EndpointAccess: hyperv1.GCPEndpointAccessPublicAndPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{EndpointAccess: hyperv1.GCPEndpointAccessPublicAndPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When GCP private cluster uses KAS LoadBalancer it should delete policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{EndpointAccess: hyperv1.GCPEndpointAccessPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{EndpointAccess: hyperv1.GCPEndpointAccessPrivate}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "When IBM Cloud cluster uses KAS Route it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When Agent cluster uses KAS LoadBalancer it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AgentPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AgentPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When Agent cluster uses KAS Route with hostname it should delete policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AgentPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:  hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
						}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AgentPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:  hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
						}},
					},
				},
			},
			expectDeleted: true,
		},
		{
			name: "When KubeVirt cluster uses KAS LoadBalancer it should create policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.KubevirtPlatform, Kubevirt: &hyperv1.KubevirtPlatformSpec{}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.KubevirtPlatform, Kubevirt: &hyperv1.KubevirtPlatformSpec{}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(tc.hcluster.Namespace, tc.hcluster.Name)
			tc.hcp.Namespace = controlPlaneNamespaceName
			tc.hcp.Name = tc.hcluster.Name

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

			//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
			kubernetesEndpoint := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
				//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
				Subsets: []corev1.EndpointSubset{
					{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}},
				},
			}

			managementClusterNetwork := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
					ServiceNetwork: []string{"172.30.0.0/16"},
				},
			}

			objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}

			// For deletion tests, pre-create the openshift-ingress NetworkPolicy
			if tc.expectDeleted {
				existingPolicy := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
				existingPolicy.Spec.PodSelector = metav1.LabelSelector{}
				existingPolicy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
				objs = append(objs, existingPolicy)
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
					if err := f(); err != nil {
						return controllerutil.OperationResultNone, err
					}
					createdNetworkPolicies[netPol.Name] = netPol
				}
				return controllerutil.OperationResultCreated, nil
			})

			ctx := context.Background()
			log := ctrl.Log.WithName("test")
			version := semver.MustParse("4.15.0")

			err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, tc.hcluster, tc.hcp, version, false)
			if err != nil {
				t.Fatalf("reconcileNetworkPolicies failed: %v", err)
			}

			_, policyCreated := createdNetworkPolicies["openshift-ingress"]
			if tc.expectCreated && !policyCreated {
				t.Error("Expected openshift-ingress NetworkPolicy to be created, but it was not")
			}
			if !tc.expectCreated && policyCreated {
				t.Error("Expected openshift-ingress NetworkPolicy to NOT be created, but it was")
			}

			if tc.expectDeleted {
				policyObj := networkpolicy.OpenshiftIngressNetworkPolicy(controlPlaneNamespaceName)
				err := fakeClient.Get(ctx, client.ObjectKeyFromObject(policyObj), policyObj)
				if !apierrors.IsNotFound(err) {
					t.Error("Expected openshift-ingress NetworkPolicy to be deleted from the cluster, but it still exists")
				}
			}
		})
	}
}

func TestReconcileLoadBalancerOauthNetworkPolicy(t *testing.T) {
	testCases := []struct {
		name string
	}{
		{
			name: "When reconciling the loadbalancer-oauth network policy, it should allow ingress on port 6443 from any source",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			policy := networkpolicy.LoadBalancerOauthNetworkPolicy("test-namespace")
			err := reconcileLoadBalancerOauthNetworkPolicy(policy)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify it only has ingress policy type
			g.Expect(policy.Spec.PolicyTypes).To(Equal([]networkingv1.PolicyType{networkingv1.PolicyTypeIngress}))

			// Verify the pod selector targets oauth-openshift pods
			g.Expect(policy.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{
				"app": "oauth-openshift",
			}))

			// Verify exactly one ingress rule
			g.Expect(policy.Spec.Ingress).To(HaveLen(1))

			// Verify the ingress rule allows from any source (empty From list)
			g.Expect(policy.Spec.Ingress[0].From).To(BeEmpty())

			// Verify the ingress rule allows port 6443/TCP
			expectedPort := intstr.FromInt(6443)
			expectedProtocol := corev1.ProtocolTCP
			g.Expect(policy.Spec.Ingress[0].Ports).To(HaveLen(1))
			g.Expect(policy.Spec.Ingress[0].Ports[0].Port).To(Equal(&expectedPort))
			g.Expect(policy.Spec.Ingress[0].Ports[0].Protocol).To(Equal(&expectedProtocol))
		})
	}
}

func TestGetManagementClusterNetwork(t *testing.T) {
	testCases := []struct {
		name          string
		capabilities  fakecapabilities.FakeCapabilitiesSupportAllExcept
		objects       []client.Object
		expectNetwork bool
		expectError   bool
		expectedName  string
	}{
		{
			name:          "When CapabilityNetworks is not supported, it should return nil",
			capabilities:  fakecapabilities.FakeCapabilitiesSupportAllExcept{NotSupported: map[capabilities.CapabilityType]struct{}{capabilities.CapabilityNetworks: {}}},
			objects:       nil,
			expectNetwork: false,
		},
		{
			name:         "When CapabilityNetworks is supported and network exists, it should return the network",
			capabilities: fakecapabilities.FakeCapabilitiesSupportAllExcept{NotSupported: map[capabilities.CapabilityType]struct{}{}},
			objects: []client.Object{
				&configv1.Network{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec: configv1.NetworkSpec{
						ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
					},
				},
			},
			expectNetwork: true,
			expectedName:  "cluster",
		},
		{
			name:         "When CapabilityNetworks is supported but network does not exist, it should return an error",
			capabilities: fakecapabilities.FakeCapabilitiesSupportAllExcept{NotSupported: map[capabilities.CapabilityType]struct{}{}},
			objects:      nil,
			expectError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(configv1.AddToScheme(scheme)).To(Succeed())

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tc.objects != nil {
				builder = builder.WithObjects(tc.objects...)
			}
			fakeClient := builder.Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: &tc.capabilities,
			}

			network, err := reconciler.getManagementClusterNetwork(t.Context())

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectNetwork {
				g.Expect(network).ToNot(BeNil())
				g.Expect(network.Name).To(Equal(tc.expectedName))
			} else if !tc.expectError {
				g.Expect(network).To(BeNil())
			}
		})
	}
}

func TestReconcileManagementKASPolicies(t *testing.T) {
	testCases := []struct {
		name                                                       string
		platformType                                               hyperv1.PlatformType
		controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel bool
		enableCVOManagementClusterMetricsAccess                    bool
		expectManagementKAS                                        bool
		expectMetricsServer                                        bool
	}{
		{
			name:         "When label is not applied, it should create no policies",
			platformType: hyperv1.AWSPlatform,
			controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel: false,
			expectManagementKAS: false,
			expectMetricsServer: false,
		},
		{
			name:         "When platform is not AWS, it should create no policies",
			platformType: hyperv1.AzurePlatform,
			controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel: true,
			expectManagementKAS: false,
			expectMetricsServer: false,
		},
		{
			name:         "When AWS platform with label applied, it should create management-kas policy",
			platformType: hyperv1.AWSPlatform,
			controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel: true,
			expectManagementKAS: true,
			expectMetricsServer: false,
		},
		{
			name:         "When AWS platform with label and CVO metrics enabled, it should create both policies",
			platformType: hyperv1.AWSPlatform,
			controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel: true,
			enableCVOManagementClusterMetricsAccess:                    true,
			expectManagementKAS:                                        true,
			expectMetricsServer:                                        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: tc.platformType},
				},
			}
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: tc.platformType},
				},
			}

			managementClusterNetwork := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
				},
			}

			//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
			kubernetesEndpoint := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
				//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
				Subsets: []corev1.EndpointSubset{
					{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}},
				},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                                  fakeClient,
				ManagementClusterCapabilities:           fakecapabilities.NewSupportAllExcept(),
				EnableCVOManagementClusterMetricsAccess: tc.enableCVOManagementClusterMetricsAccess,
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			err := reconciler.reconcileManagementKASPolicies(t.Context(), createOrUpdate, hcluster, hcp, controlPlaneNamespaceName, managementClusterNetwork, kubernetesEndpoint, tc.controlPlaneOperatorAppliesManagementKASNetworkPolicyLabel)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasManagementKAS := createdPolicies["management-kas"]
			g.Expect(hasManagementKAS).To(Equal(tc.expectManagementKAS), "management-kas policy presence mismatch")

			_, hasMetricsServer := createdPolicies["metrics-server"]
			g.Expect(hasMetricsServer).To(Equal(tc.expectMetricsServer), "metrics-server policy presence mismatch")
		})
	}
}

func TestReconcilePlatformNetworkPolicies(t *testing.T) {
	testCases := []struct {
		name                string
		platformType        hyperv1.PlatformType
		kubevirtCredentials *hyperv1.KubevirtPlatformCredentials
		version             string
		expectPrivateRouter bool
		expectVirtLauncher  bool
		expectIngressOnly   bool
	}{
		{
			name:                "When platform is AWS, it should create private-router policy",
			platformType:        hyperv1.AWSPlatform,
			version:             "4.15.0",
			expectPrivateRouter: true,
		},
		{
			name:                "When platform is Azure, it should create private-router policy",
			platformType:        hyperv1.AzurePlatform,
			version:             "4.15.0",
			expectPrivateRouter: true,
		},
		{
			name:                "When platform is GCP, it should create private-router policy",
			platformType:        hyperv1.GCPPlatform,
			version:             "4.15.0",
			expectPrivateRouter: true,
		},
		{
			name:                "When platform is AWS with version < 4.14, it should create ingress-only private-router policy",
			platformType:        hyperv1.AWSPlatform,
			version:             "4.13.0",
			expectPrivateRouter: true,
			expectIngressOnly:   true,
		},
		{
			name:               "When platform is KubeVirt without credentials, it should create virt-launcher policy",
			platformType:       hyperv1.KubevirtPlatform,
			version:            "4.15.0",
			expectVirtLauncher: true,
		},
		{
			name:                "When platform is KubeVirt with credentials, it should create virt-launcher policy on external infra",
			platformType:        hyperv1.KubevirtPlatform,
			kubevirtCredentials: &hyperv1.KubevirtPlatformCredentials{},
			version:             "4.15.0",
			expectVirtLauncher:  true,
		},
		{
			name:                "When platform is IBMCloud, it should not create private-router or virt-launcher policies",
			platformType:        hyperv1.IBMCloudPlatform,
			version:             "4.15.0",
			expectPrivateRouter: false,
			expectVirtLauncher:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: tc.platformType},
					InfraID:  "test-infra",
				},
			}
			if tc.platformType == hyperv1.KubevirtPlatform {
				hcluster.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{
					Credentials: tc.kubevirtCredentials,
				}
			}

			managementClusterNetwork := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
					ServiceNetwork: []string{"172.30.0.0/16"},
				},
			}

			//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
			kubernetesEndpoint := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
				//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
				Subsets: []corev1.EndpointSubset{
					{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}},
				},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			g.Expect(configv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}
			if tc.kubevirtCredentials != nil {
				reconciler.KubevirtInfraClients = kvinfra.NewMockKubevirtInfraClientMap(fakeClient, "1.0.0", "1.30.0")
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			log := ctrl.Log.WithName("test")
			version := semver.MustParse(tc.version)

			err := reconciler.reconcilePlatformNetworkPolicies(t.Context(), log, createOrUpdate, hcluster, kubernetesEndpoint, managementClusterNetwork, version, controlPlaneNamespaceName)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasPrivateRouter := createdPolicies["private-router"]
			g.Expect(hasPrivateRouter).To(Equal(tc.expectPrivateRouter), "private-router policy presence mismatch")

			if tc.expectIngressOnly && hasPrivateRouter {
				policy := createdPolicies["private-router"]
				g.Expect(policy.Spec.PolicyTypes).To(Equal([]networkingv1.PolicyType{networkingv1.PolicyTypeIngress}))
				g.Expect(policy.Spec.Egress).To(BeEmpty())
			}

			_, hasVirtLauncher := createdPolicies["virt-launcher"]
			g.Expect(hasVirtLauncher).To(Equal(tc.expectVirtLauncher), "virt-launcher policy presence mismatch")
		})
	}
}

func TestReconcileServiceNetworkPolicies(t *testing.T) {
	testCases := []struct {
		name             string
		services         []hyperv1.ServicePublishingStrategyMapping
		expectedPolicies []string
		absentPolicies   []string
	}{
		{
			name: "When OAuth uses NodePort, it should create nodeport-oauth policy",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.OAuthServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
			},
			expectedPolicies: []string{"nodeport-oauth"},
			absentPolicies:   []string{"loadbalancer-oauth"},
		},
		{
			name: "When OAuth uses LoadBalancer, it should create loadbalancer-oauth policy",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.OAuthServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer},
				},
			},
			expectedPolicies: []string{"loadbalancer-oauth"},
			absentPolicies:   []string{"nodeport-oauth"},
		},
		{
			name: "When Ignition uses NodePort, it should create ignition and ignition-proxy policies",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
			},
			expectedPolicies: []string{"nodeport-ignition", "nodeport-ignition-proxy"},
		},
		{
			name: "When Ignition uses Route, it should not create ignition policies",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				},
			},
			absentPolicies: []string{"nodeport-ignition", "nodeport-ignition-proxy"},
		},
		{
			name: "When Konnectivity uses NodePort, it should create konnectivity and konnectivity-kas policies",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Konnectivity,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
			},
			expectedPolicies: []string{"nodeport-konnectivity", "nodeport-konnectivity-kas"},
		},
		{
			name: "When Konnectivity uses Route, it should not create konnectivity policies",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.Konnectivity,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
				},
			},
			absentPolicies: []string{"nodeport-konnectivity", "nodeport-konnectivity-kas"},
		},
		{
			name: "When multiple services use NodePort, it should create all corresponding policies",
			services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service:                   hyperv1.OAuthServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
				{
					Service:                   hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
				{
					Service:                   hyperv1.Konnectivity,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.NodePort, NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1"}},
				},
			},
			expectedPolicies: []string{"nodeport-oauth", "nodeport-ignition", "nodeport-ignition-proxy", "nodeport-konnectivity", "nodeport-konnectivity-kas"},
		},
		{
			name:           "When no services are specified, it should create no service policies",
			services:       []hyperv1.ServicePublishingStrategyMapping{},
			absentPolicies: []string{"nodeport-oauth", "loadbalancer-oauth", "nodeport-ignition", "nodeport-ignition-proxy", "nodeport-konnectivity", "nodeport-konnectivity-kas"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					Services: tc.services,
				},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			err := reconciler.reconcileServiceNetworkPolicies(t.Context(), createOrUpdate, hcluster, controlPlaneNamespaceName)
			g.Expect(err).ToNot(HaveOccurred())

			for _, expected := range tc.expectedPolicies {
				_, found := createdPolicies[expected]
				g.Expect(found).To(BeTrue(), "expected %s policy to be created", expected)
			}

			for _, absent := range tc.absentPolicies {
				_, found := createdPolicies[absent]
				g.Expect(found).To(BeFalse(), "expected %s policy to NOT be created", absent)
			}
		})
	}
}

func TestReconcileOAuthNetworkPolicies(t *testing.T) {
	testCases := []struct {
		name                    string
		serviceType             hyperv1.PublishingStrategyType
		expectNodePortOauth     bool
		expectLoadBalancerOauth bool
	}{
		{
			name:                    "When OAuth uses NodePort, it should create nodeport-oauth policy only",
			serviceType:             hyperv1.NodePort,
			expectNodePortOauth:     true,
			expectLoadBalancerOauth: false,
		},
		{
			name:                    "When OAuth uses LoadBalancer, it should create loadbalancer-oauth policy only",
			serviceType:             hyperv1.LoadBalancer,
			expectNodePortOauth:     false,
			expectLoadBalancerOauth: true,
		},
		{
			name:                    "When OAuth uses Route, it should create no OAuth policies",
			serviceType:             hyperv1.Route,
			expectNodePortOauth:     false,
			expectLoadBalancerOauth: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				},
			}
			svc := hyperv1.ServicePublishingStrategyMapping{
				Service:                   hyperv1.OAuthServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: tc.serviceType},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			err := reconciler.reconcileOAuthNetworkPolicies(t.Context(), createOrUpdate, hcluster, svc, controlPlaneNamespaceName)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasNodePort := createdPolicies["nodeport-oauth"]
			g.Expect(hasNodePort).To(Equal(tc.expectNodePortOauth), "nodeport-oauth policy presence mismatch")

			_, hasLoadBalancer := createdPolicies["loadbalancer-oauth"]
			g.Expect(hasLoadBalancer).To(Equal(tc.expectLoadBalancerOauth), "loadbalancer-oauth policy presence mismatch")
		})
	}
}

func TestReconcileIgnitionNetworkPolicies(t *testing.T) {
	testCases := []struct {
		name                string
		serviceType         hyperv1.PublishingStrategyType
		expectIgnition      bool
		expectIgnitionProxy bool
	}{
		{
			name:                "When Ignition uses NodePort, it should create both ignition and ignition-proxy policies",
			serviceType:         hyperv1.NodePort,
			expectIgnition:      true,
			expectIgnitionProxy: true,
		},
		{
			name:                "When Ignition uses Route, it should create no ignition policies",
			serviceType:         hyperv1.Route,
			expectIgnition:      false,
			expectIgnitionProxy: false,
		},
		{
			name:                "When Ignition uses LoadBalancer, it should create no ignition policies",
			serviceType:         hyperv1.LoadBalancer,
			expectIgnition:      false,
			expectIgnitionProxy: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				},
			}
			svc := hyperv1.ServicePublishingStrategyMapping{
				Service:                   hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: tc.serviceType},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			err := reconciler.reconcileIgnitionNetworkPolicies(t.Context(), createOrUpdate, hcluster, svc, controlPlaneNamespaceName)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasIgnition := createdPolicies["nodeport-ignition"]
			g.Expect(hasIgnition).To(Equal(tc.expectIgnition), "nodeport-ignition policy presence mismatch")

			_, hasProxy := createdPolicies["nodeport-ignition-proxy"]
			g.Expect(hasProxy).To(Equal(tc.expectIgnitionProxy), "nodeport-ignition-proxy policy presence mismatch")
		})
	}
}

func TestReconcileKonnectivityNetworkPolicies(t *testing.T) {
	testCases := []struct {
		name                  string
		serviceType           hyperv1.PublishingStrategyType
		expectKonnectivity    bool
		expectKonnectivityKAS bool
	}{
		{
			name:                  "When Konnectivity uses NodePort, it should create both konnectivity and konnectivity-kas policies",
			serviceType:           hyperv1.NodePort,
			expectKonnectivity:    true,
			expectKonnectivityKAS: true,
		},
		{
			name:                  "When Konnectivity uses Route, it should create no konnectivity policies",
			serviceType:           hyperv1.Route,
			expectKonnectivity:    false,
			expectKonnectivityKAS: false,
		},
		{
			name:                  "When Konnectivity uses LoadBalancer, it should create no konnectivity policies",
			serviceType:           hyperv1.LoadBalancer,
			expectKonnectivity:    false,
			expectKonnectivityKAS: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := "test-cp-ns"
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				},
			}
			svc := hyperv1.ServicePublishingStrategyMapping{
				Service:                   hyperv1.Konnectivity,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: tc.serviceType},
			}

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				netPol, ok := obj.(*networkingv1.NetworkPolicy)
				if !ok {
					t.Fatalf("unexpected object type: %T", obj)
				}
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				createdPolicies[netPol.Name] = netPol
				return controllerutil.OperationResultCreated, nil
			})

			err := reconciler.reconcileKonnectivityNetworkPolicies(t.Context(), createOrUpdate, hcluster, svc, controlPlaneNamespaceName)
			g.Expect(err).ToNot(HaveOccurred())

			_, hasKonnectivity := createdPolicies["nodeport-konnectivity"]
			g.Expect(hasKonnectivity).To(Equal(tc.expectKonnectivity), "nodeport-konnectivity policy presence mismatch")

			_, hasKonnectivityKAS := createdPolicies["nodeport-konnectivity-kas"]
			g.Expect(hasKonnectivityKAS).To(Equal(tc.expectKonnectivityKAS), "nodeport-konnectivity-kas policy presence mismatch")
		})
	}
}

func TestReconcileNetworkPolicies_LoadBalancerOauth(t *testing.T) {
	testCases := []struct {
		name          string
		hcluster      *hyperv1.HostedCluster
		hcp           *hyperv1.HostedControlPlane
		expectCreated bool
	}{
		{
			name: "When OAuth service uses LoadBalancer strategy, it should create loadbalancer-oauth network policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
					},
				},
			},
			expectCreated: true,
		},
		{
			name: "When OAuth service uses Route strategy, it should not create loadbalancer-oauth network policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.LoadBalancer}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
					},
				},
			},
			expectCreated: false,
		},
		{
			name: "When OAuth service uses NodePort strategy, it should not create loadbalancer-oauth network policy",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:     hyperv1.NodePort,
							NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1", Port: 31000},
						}},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: hyperv1.IBMCloudPlatform},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}},
						{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type:     hyperv1.NodePort,
							NodePort: &hyperv1.NodePortPublishingStrategy{Address: "10.0.0.1", Port: 31000},
						}},
					},
				},
			},
			expectCreated: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			controlPlaneNamespaceName := manifests.HostedControlPlaneNamespace(tc.hcluster.Namespace, tc.hcluster.Name)
			tc.hcp.Namespace = controlPlaneNamespaceName
			tc.hcp.Name = tc.hcluster.Name

			scheme := runtime.NewScheme()
			g.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(configv1.AddToScheme(scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(scheme)).To(Succeed())

			//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
			kubernetesEndpoint := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
				//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
				Subsets: []corev1.EndpointSubset{
					{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}},
				},
			}

			managementClusterNetwork := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
					ServiceNetwork: []string{"172.30.0.0/16"},
				},
			}

			objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
					if err := f(); err != nil {
						return controllerutil.OperationResultNone, err
					}
					createdNetworkPolicies[netPol.Name] = netPol
				}
				return controllerutil.OperationResultCreated, nil
			})

			ctx := context.Background()
			log := ctrl.Log.WithName("test")
			version := semver.MustParse("4.15.0")

			err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, tc.hcluster, tc.hcp, version, false)
			g.Expect(err).ToNot(HaveOccurred())

			_, policyCreated := createdNetworkPolicies["loadbalancer-oauth"]
			if tc.expectCreated {
				g.Expect(policyCreated).To(BeTrue(), "expected loadbalancer-oauth NetworkPolicy to be created")
			} else {
				g.Expect(policyCreated).To(BeFalse(), "expected loadbalancer-oauth NetworkPolicy to NOT be created")
			}
		})
	}
}

func TestFetchInfraClusterNetwork(t *testing.T) {
	t.Parallel()

	infraNetworkScheme := runtime.NewScheme()
	_ = configv1.Install(infraNetworkScheme)
	_ = corev1.AddToScheme(infraNetworkScheme)

	newTestCluster := func() *hyperv1.HostedCluster {
		return &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-cluster",
				Namespace:  "clusters",
				Generation: 1,
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "test-infra-id",
			},
		}
	}

	tests := []struct {
		name             string
		existingNetwork  *configv1.Network
		interceptorErr   error
		infraNamespace   string
		expectNetwork    bool
		expectErr        bool
		expectErrMsg     string
		expectCondStatus metav1.ConditionStatus
	}{
		{
			name: "When infra cluster network is readable, it should return the network object and set condition to true",
			existingNetwork: &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14"}},
					ServiceNetwork: []string{"172.30.0.0/16"},
				},
			},
			infraNamespace:   "test-infra-ns",
			expectNetwork:    true,
			expectCondStatus: metav1.ConditionTrue,
		},
		{
			name:             "When infra client gets a Forbidden error, it should return nil network without error and set condition to false",
			interceptorErr:   apierrors.NewForbidden(schema.GroupResource{Group: "config.openshift.io", Resource: "networks"}, "cluster", fmt.Errorf("forbidden")),
			infraNamespace:   "test-infra-ns",
			expectNetwork:    false,
			expectCondStatus: metav1.ConditionFalse,
		},
		{
			name:             "When infra client gets a NotFound error, it should return nil network without error and set condition to false",
			interceptorErr:   apierrors.NewNotFound(schema.GroupResource{Group: "config.openshift.io", Resource: "networks"}, "cluster"),
			infraNamespace:   "test-infra-ns",
			expectNetwork:    false,
			expectCondStatus: metav1.ConditionFalse,
		},
		{
			name:           "When infra client gets an unexpected error, it should propagate the error for retry",
			interceptorErr: fmt.Errorf("connection timeout"),
			infraNamespace: "test-infra-ns",
			expectErr:      true,
			expectErrMsg:   "failed to get infrastructure cluster network config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcluster := newTestCluster()
			log := ctrl.Log.WithName("test")

			builder := fake.NewClientBuilder().WithScheme(infraNetworkScheme)
			if tt.existingNetwork != nil {
				builder = builder.WithObjects(tt.existingNetwork)
			}
			if tt.interceptorErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						return tt.interceptorErr
					},
				})
			}
			infraClient := builder.Build()

			network, err := fetchInfraClusterNetwork(t.Context(), infraClient, tt.infraNamespace, hcluster, log)

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.expectErrMsg))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectNetwork {
				g.Expect(network).ToNot(BeNil())
				g.Expect(network.Spec.ClusterNetwork).ToNot(BeEmpty())
			} else {
				g.Expect(network).To(BeNil())
			}

			cond := findConditionByType(hcluster.Status.Conditions, string(hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC))
			if tt.expectCondStatus != "" {
				g.Expect(cond).ToNot(BeNil(), "expected condition %s to be set", hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC)
				g.Expect(cond.Status).To(Equal(tt.expectCondStatus))
			}
		})
	}
}

func findConditionByType(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
