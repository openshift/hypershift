package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/zapr"
	configv1 "github.com/openshift/api/config/v1"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	utilpointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestWebhookAllowsHostedClusterReconcilerUpdates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		hostedCluster     *hyperv1.HostedCluster
		additionalObjects []crclient.Object
	}{
		{
			name: "None cluster on azure management cluster",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "none-cluster",
					Namespace: "some-ns",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			additionalObjects: []crclient.Object{
				&configv1.Infrastructure{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec:       configv1.InfrastructureSpec{PlatformSpec: configv1.PlatformSpec{Type: configv1.AzurePlatformType}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-ns"},
					Data:       map[string][]byte{".dockerconfigjson": []byte("something")},
				},
				&configv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "none-cluster"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.hostedCluster.Annotations = map[string]string{
				hyperv1.ControlPlaneOperatorImageAnnotation: "some-image",
			}

			mgr, err := ctrl.NewManager(&rest.Config{}, ctrl.Options{
				MetricsBindAddress: "0",
				MapperProvider: func(*rest.Config) (meta.RESTMapper, error) {
					return restmapper.NewDiscoveryRESTMapper(nil), nil
				},
				NewClient: func(cache.Cache, *rest.Config, crclient.Options, ...crclient.Object) (crclient.Client, error) {
					return &hostedClusterUpdateValidatingClient{
						Client: fake.NewClientBuilder().
							WithScheme(hyperapi.Scheme).
							WithObjects(append(tc.additionalObjects, tc.hostedCluster)...).
							Build(),
					}, nil
				},
				NewCache: func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
					return &informertest.FakeInformers{}, nil
				},
				Scheme: hyperapi.Scheme,
			})
			if err != nil {
				t.Fatalf("failed to construct manager: %v", err)
			}
			hostedClusterReconciler := &HostedClusterReconciler{
				Client:                        mgr.GetClient(),
				ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
				ImageMetadataProvider: imageMetadataProviderFunc(func(context.Context, string, []byte) (*dockerv1client.DockerImageConfig, error) {
					return &dockerv1client.DockerImageConfig{}, nil
				}),
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{},
			}
			if err := hostedClusterReconciler.SetupWithManager(mgr, upsert.New(true)); err != nil {
				t.Fatalf("failed to set up hostedClusterReconciler: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = log.IntoContext(ctx, zapr.NewLogger(zaptest.NewLogger(t)))

			if _, err := hostedClusterReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(tc.hostedCluster)}); err != nil {
				t.Errorf("failed to reconcile cluster: %v", err)
			}
		})
	}
}

type hostedClusterUpdateValidatingClient struct {
	crclient.Client
}

func (h *hostedClusterUpdateValidatingClient) Update(ctx context.Context, obj crclient.Object, opts ...crclient.UpdateOption) error {
	hcluster, isHcluster := obj.(*hyperv1.HostedCluster)
	if !isHcluster {
		return h.Client.Update(ctx, obj, opts...)
	}

	oldCluster := &hyperv1.HostedCluster{}
	if err := h.Client.Get(ctx, crclient.ObjectKeyFromObject(hcluster), oldCluster); err != nil {
		return fmt.Errorf("failed to validate hostedcluster update: failed to get old hosted cluster: %w", err)
	}

	if err := validateHostedClusterUpdate(hcluster.DeepCopy(), oldCluster.DeepCopy()); err != nil {
		return fmt.Errorf("update rejected by admission: %w", err)
	}

	return h.Client.Update(ctx, obj, opts...)
}

type imageMetadataProviderFunc func(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)

func (f imageMetadataProviderFunc) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f(ctx, imageRef, pullSecret)
}

func TestValidateHostedClusterUpdate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		old  *hyperv1.HostedCluster
		new  *hyperv1.HostedCluster

		expectError bool
	}{
		{
			name: "APIServer port was unset and gets set, allowed",
			old:  &hyperv1.HostedCluster{},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			expectError: false,
		},
		{
			name: "APIServer port remains unchanged, allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			expectError: false,
		},
		{
			name: "APIServer port gets updated, not allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(8443)}}},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			if err := validateHostedClusterUpdate(tc.new, tc.old); (err != nil) != tc.expectError {
				t.Errorf("expected error to be %t, was %t", tc.expectError, err != nil)
			}
		})
	}
}

func TestReconcileDeprecatedGlobalConfig(t *testing.T) {
	hc := &hyperv1.HostedCluster{}
	hc.Name = "fake-name"
	hc.Namespace = "fake-namespace"

	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			ClientCA: configv1.ConfigMapNameReference{
				Name: "fake-ca",
			},
		},
	}
	serializedAPIServer, err := util.SerializeResource(apiServer, hyperapi.Scheme)
	if err != nil {
		t.Fatalf("failed to serialize apiserver: %v", err)
	}
	hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
		Items: []runtime.RawExtension{
			{
				Raw: []byte(serializedAPIServer),
			},
		},
		ConfigMapRefs: []corev1.LocalObjectReference{
			{
				Name: "fake-ca",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(hc).
		Build()
	reconciler := &HostedClusterReconciler{
		Client: fakeClient,
	}

	originalSpec := hc.Spec.DeepCopy()
	if err := reconciler.reconcileDeprecatedGlobalConfig(context.Background(), hc); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	// Update fields if required.
	if !equality.Semantic.DeepEqual(&hc.Spec, originalSpec) {
		err := reconciler.Client.Update(context.Background(), hc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	updatedHc := &hyperv1.HostedCluster{}
	if err := fakeClient.Get(context.Background(), crclient.ObjectKeyFromObject(hc), updatedHc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedHc.Spec.Configuration == nil {
		t.Fatalf("unexpected nil configuration")
	}

	if len(updatedHc.Spec.Configuration.Items) > 0 {
		t.Errorf("non-empty deprecated configuration")
	}
	if len(updatedHc.Spec.Configuration.ConfigMapRefs) > 0 {
		t.Errorf("non-empty configmap refs")
	}
	if len(updatedHc.Spec.Configuration.SecretRefs) > 0 {
		t.Errorf("non-emtpy secret refs")
	}
	if !equality.Semantic.DeepEqual(&apiServer.Spec, updatedHc.Spec.Configuration.APIServer) {
		t.Errorf("unexpected apiserver spec: %#v", updatedHc.Spec.Configuration.APIServer)
	}
}
