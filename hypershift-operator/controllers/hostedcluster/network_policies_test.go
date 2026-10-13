package hostedcluster

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

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
