package powervs

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/powervs/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/blang/semver"
	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIInfraCR(t *testing.T) {
	t.Parallel()
	upsertError := errors.New("upsert failed")
	for _, testCase := range []struct {
		name        string
		upsertError error
	}{
		{name: "When upsert succeeds, it should configure the PowerVS cluster and restore its type metadata"},
		{name: "When upsert fails, it should return the original error and no cluster", upsertError: upsertError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "clusters"},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:    hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSPlatformSpec{ServiceInstanceID: "service-instance-id"},
					},
				},
			}
			controlPlaneNamespace := "clusters-example"
			endpoint := hyperv1.APIEndpoint{Host: "api.example.com", Port: 7443}
			var upsertedCluster *capipowervs.IBMPowerVSCluster
			createOrUpdate := func(_ context.Context, _ client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				var ok bool
				upsertedCluster, ok = object.(*capipowervs.IBMPowerVSCluster)
				if !ok {
					t.Fatalf("unexpected upsert object type: %T", object)
				}
				if upsertedCluster.Name != hostedCluster.Name || upsertedCluster.Namespace != controlPlaneNamespace {
					t.Fatalf("unexpected upsert object identity: %s/%s", upsertedCluster.Namespace, upsertedCluster.Name)
				}
				if diff := cmp.Diff(capipowervs.IBMPowerVSClusterSpec{ServiceInstanceID: "service-instance-id"}, upsertedCluster.Spec); diff != "" {
					t.Fatalf("unexpected initial cluster spec (-want +got): %s", diff)
				}
				if testCase.upsertError != nil {
					return controllerutil.OperationResultNone, testCase.upsertError
				}
				if err := mutate(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				upsertedCluster.TypeMeta = metav1.TypeMeta{}
				return controllerutil.OperationResultCreated, nil
			}
			object, err := (PowerVS{}).ReconcileCAPIInfraCR(t.Context(), nil, createOrUpdate, hostedCluster, controlPlaneNamespace, endpoint)
			if upsertedCluster == nil {
				t.Fatal("expected the upserter to receive a PowerVS cluster")
			}
			if testCase.upsertError != nil {
				if !errors.Is(err, upsertError) || object != nil {
					t.Fatalf("expected original upsert error and nil object, got %v and %v", err, object)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cluster, ok := object.(*capipowervs.IBMPowerVSCluster)
			if !ok || cluster != upsertedCluster {
				t.Fatalf("expected the upserted PowerVS cluster, got %T", object)
			}
			if cluster.Name != hostedCluster.Name || cluster.Namespace != controlPlaneNamespace {
				t.Fatalf("unexpected returned object identity: %s/%s", cluster.Namespace, cluster.Name)
			}
			if cluster.Annotations[capiv1.ManagedByAnnotation] != "external" || !cluster.Status.Ready {
				t.Fatalf("unexpected reconciled cluster: annotations %v, ready %t", cluster.Annotations, cluster.Status.Ready)
			}
			expectedSpec := capipowervs.IBMPowerVSClusterSpec{ServiceInstanceID: "service-instance-id"}
			expectedSpec.ControlPlaneEndpoint.Host = endpoint.Host
			expectedSpec.ControlPlaneEndpoint.Port = endpoint.Port
			if diff := cmp.Diff(expectedSpec, cluster.Spec); diff != "" {
				t.Fatalf("unexpected reconciled cluster spec (-want +got): %s", diff)
			}
			if cluster.Kind != "IBMPowerVSCluster" || cluster.APIVersion != capipowervs.GroupVersion.String() {
				t.Fatalf("expected restored PowerVS type metadata, got %+v", cluster.TypeMeta)
			}
		})
	}
}

func buildPowerVSHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
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
		"--provider-id-fmt=v2",
	}

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
			hcp: buildPowerVSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.22.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.23 and HCP has Modern TLS profile it should append min-version only",
			hcp: buildPowerVSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:           "When version is 5.0 and HCP has custom TLS profile it should append custom TLS args",
			hcp:            buildPowerVSHostedControlPlane(customTLSProfile),
			payloadVersion: ptr.To(semver.MustParse("5.0.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := New("", tc.payloadVersion)
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:    hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSPlatformSpec{},
					},
				},
			}
			spec, err := platform.CAPIProviderDeploymentSpec(hcluster, tc.hcp)

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
