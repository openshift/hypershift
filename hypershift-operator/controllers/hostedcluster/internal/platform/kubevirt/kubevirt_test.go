package kubevirt

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	kubevirt := Kubevirt{}
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

func TestReconcileCredentials(t *testing.T) {
	kubevirt := Kubevirt{}
	fakeClient := fake.NewClientBuilder().Build()
	fnCallsCount := 0
	createOrUpdateFN := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		fnCallsCount++
		return "", nil
	}
	hcluster := &hyperv1.HostedCluster{}
	err := kubevirt.ReconcileCredentials(t.Context(), fakeClient, createOrUpdateFN, hcluster, "controlPlanNamespace")
	if err != nil {
		t.Fatalf("ReconcileCredentials failed: %v", err)
	}
	if fnCallsCount > 0 {
		t.Fatalf("create or update func should not be called")
	}
}

func TestReconcileSecretEncryption(t *testing.T) {
	kubevirt := Kubevirt{}
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

// Helper function to create a HostedControlPlane with TLS profile for testing
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
		name         string
		hcp          *hyperv1.HostedControlPlane
		expectedArgs []string
	}{
		{
			name:         "When HostedControlPlane is nil it should not append TLS args",
			expectedArgs: defaultArgs,
		},
		{
			name: "When HostedControlPlane is provided with Modern TLS profile it should append min-version only",
			hcp: buildKubeVirtHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name: "When HostedControlPlane is provided with custom TLS profile it should append custom TLS args",
			hcp:  buildKubeVirtHostedControlPlane(customTLSProfile),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := Kubevirt{}
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
