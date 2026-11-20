package kubevirt

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	kubevirt := New(nil)
	fakeClient := fake.NewClientBuilder().Build()
	testNamespace := "testNamespace"
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "testInfraID",
		},
	}
	expectedResult := &capikubevirt.KubevirtCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      hcluster.Spec.InfraID,
			Annotations: map[string]string{
				hostedClusterAnnotation:    client.ObjectKeyFromObject(hcluster).String(),
				capiv1.ManagedByAnnotation: "external",
			},
		},
		Status: capikubevirt.KubevirtClusterStatus{
			Ready: true,
		},
	}
	testCases := []struct {
		name        string
		expectedErr error
	}{
		{
			name: "Happy flow",
		},
		{
			name:        "Expected err from func",
			expectedErr: errors.New("test error"),
		},
	}

	apiendpoint := hyperv1.APIEndpoint{}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fnCallsCount := 0
			createOrUpdateFN := func(
				ctx context.Context,
				c client.Client,
				obj client.Object,
				f controllerutil.MutateFn,
			) (controllerutil.OperationResult, error) {
				fnCallsCount++
				_ = f()
				return "", tc.expectedErr
			}
			result, err := kubevirt.ReconcileCAPIInfraCR(
				t.Context(),
				fakeClient,
				createOrUpdateFN,
				hcluster,
				testNamespace,
				apiendpoint)
			if fnCallsCount != 1 {
				t.Fatalf("Expected the provided function to be called once")
			}
			if tc.expectedErr != nil {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("ReconcileCAPIInfraCR: Expected to fail. gotErr: %v, expectedErr: %v", err, tc.expectedErr)
				}
			} else if err != nil {
				t.Fatalf("ReconcileCAPIInfraCR: Got unexpected error: %v (expectedErr: %v)", err, tc.expectedErr)
			} else {
				if !equality.Semantic.DeepEqual(expectedResult, result) {
					t.Error(cmp.Diff(expectedResult, result))
				}

			}
		})
	}
}

func TestReconcileSecretEncryption(t *testing.T) {
	kubevirt := New(nil)
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileSecretEncryption(t.Context(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileSecretEncryption failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}

func TestCAPIProviderPolicyRules(t *testing.T) {
	t.Run("When building the platform policy it should include the expected rules", func(t *testing.T) {
		kubevirt := New(nil)

		expectedRules := []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"services"},
				Verbs:     []string{rbacv1.VerbAll},
			},
			{
				APIGroups: []string{"kubevirt.io"},
				Resources: []string{"virtualmachineinstances", "virtualmachines"},
				Verbs:     []string{rbacv1.VerbAll},
			},
			{
				APIGroups: []string{"kubevirt.io"},
				Resources: []string{"virtualmachineinstancemigrations"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"cdi.kubevirt.io"},
				Resources: []string{"datavolumes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}

		rules := kubevirt.CAPIProviderPolicyRules()
		if !equality.Semantic.DeepEqual(expectedRules, rules) {
			t.Error(cmp.Diff(expectedRules, rules))
		}
	})

	t.Run("When upgrading an existing Role it should add VMIM creation", testCAPIProviderPolicyRulesUpdateExistingRole)
}

func testCAPIProviderPolicyRulesUpdateExistingRole(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add RBAC scheme: %v", err)
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	role := func(rules []rbacv1.PolicyRule) *rbacv1.Role {
		return &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capi-provider",
				Namespace: "test-namespace",
			},
			Rules: rules,
		}
	}

	oldRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"events", "secrets", "configmaps"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"kubevirt.io"},
			Resources: []string{"virtualmachineinstances", "virtualmachines"},
			Verbs:     []string{rbacv1.VerbAll},
		},
		{
			APIGroups: []string{"cdi.kubevirt.io"},
			Resources: []string{"datavolumes"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}

	provider := upsert.NewApplyProvider(false)
	if result, err := provider.ApplyManifest(t.Context(), fakeClient, role(oldRules)); err != nil {
		t.Fatalf("failed to create pre-upgrade Role: %v", err)
	} else if result != controllerutil.OperationResultCreated {
		t.Fatalf("expected pre-upgrade Role creation, got %s", result)
	}

	oldRole := &rbacv1.Role{}
	if err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(role(nil)), oldRole); err != nil {
		t.Fatalf("failed to get pre-upgrade Role: %v", err)
	}
	oldHash := oldRole.Annotations[upsert.DesiredStateHashAnnotation]
	if oldHash == "" {
		t.Fatal("pre-upgrade Role does not have a desired-state hash")
	}

	newRules := append([]rbacv1.PolicyRule{oldRules[0]}, New(nil).CAPIProviderPolicyRules()...)
	result, err := provider.ApplyManifest(t.Context(), fakeClient, role(newRules))
	if err != nil {
		t.Fatalf("failed to update Role: %v", err)
	}
	if result != controllerutil.OperationResultUpdated {
		t.Fatalf("expected Role update, got %s", result)
	}

	updatedRole := &rbacv1.Role{}
	if err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(role(nil)), updatedRole); err != nil {
		t.Fatalf("failed to get updated Role: %v", err)
	}
	newHash := updatedRole.Annotations[upsert.DesiredStateHashAnnotation]
	if newHash == "" {
		t.Fatal("updated Role does not have a desired-state hash")
	}
	if newHash == oldHash {
		t.Fatalf("expected desired-state hash to change during upgrade: old=%s new=%s", oldHash, newHash)
	}

	hasVMIMRule := false
	for _, rule := range updatedRole.Rules {
		if equality.Semantic.DeepEqual(rule.APIGroups, []string{"kubevirt.io"}) &&
			equality.Semantic.DeepEqual(rule.Resources, []string{"virtualmachineinstancemigrations"}) &&
			equality.Semantic.DeepEqual(rule.Verbs, []string{"create"}) {
			hasVMIMRule = true
			break
		}
	}
	if !hasVMIMRule {
		t.Fatalf("updated Role does not grant VMIM creation: %#v", updatedRole.Rules)
	}
}

func buildKubeVirtHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: tlsProfile,
				},
			},
		},
	}
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	defaultArgs := []string{
		"--namespace", "$(MY_NAMESPACE)",
		"--v=4",
		"--leader-elect=true",
	}

	defaultImage := "test-capi-image"

	customTLSProfile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				},
			},
		},
	}

	testCases := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		payloadVersion *semver.Version
		expectedArgs   []string
	}{
		{
			name:           "When HostedControlPlane is nil it should not append TLS args",
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.22 and HCP has TLS profile it should not append TLS args",
			hcp: buildKubeVirtHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.22.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.23 and HCP has Modern TLS profile it should append min-version only",
			hcp: buildKubeVirtHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:           "When version is 5.0 and HCP has custom TLS profile it should append custom TLS args",
			hcp:            buildKubeVirtHostedControlPlane(customTLSProfile),
			payloadVersion: ptr.To(semver.MustParse("5.0.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := New(tc.payloadVersion)
			spec, err := platform.CAPIProviderDeploymentSpec(&hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.ClusterAPIKubeVirtProviderImage: defaultImage,
					},
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			}, tc.hcp)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected deployment spec, got nil")
			}
			if len(spec.Template.Spec.Containers) == 0 {
				t.Fatal("expected at least 1 container, got 0")
			}

			var managerContainer *corev1.Container
			for i := range spec.Template.Spec.Containers {
				if spec.Template.Spec.Containers[i].Name == "manager" {
					managerContainer = &spec.Template.Spec.Containers[i]
					break
				}
			}
			if managerContainer == nil {
				t.Fatal("manager container not found")
			}

			if diff := cmp.Diff(managerContainer.Args, tc.expectedArgs); diff != "" {
				t.Errorf("args differ (-got +want):\n%s", diff)
			}
		})
	}
}
