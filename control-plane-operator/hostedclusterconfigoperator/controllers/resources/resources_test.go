package resources

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ocm"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/registry"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	supportutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap/zaptest"
	"golang.org/x/crypto/bcrypt"
)

type testClient struct {
	client.Client
	createCount     int
	getErrorCount   int
	randomGetErrors bool
}

var randomSource = rand.New(rand.NewSource(time.Now().UnixNano()))

func (c *testClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCount++
	return c.Client.Create(ctx, obj, opts...)
}

var initialObjects = []client.Object{
	globalconfig.IngressConfig(),
	globalconfig.ImageConfig(),
	globalconfig.ProjectConfig(),
	globalconfig.BuildConfig(),
	globalconfig.ProxyConfig(),
	// Use a valid bcrypt hash of "test" (matching fakeKubeadminPasswordSecret) so the
	// CompareHashAndPassword check passes and avoids re-hashing on every reconcile.
	// MinCost keeps the tests fast (~0.1s vs ~4s with DefaultCost, ~10x worse with -race).
	&corev1.Secret{
		ObjectMeta: manifests.KubeadminPasswordHashSecret().ObjectMeta,
		Data: map[string][]byte{
			"kubeadmin": func() []byte {
				h, _ := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
				return h
			}(),
		},
	},
	manifests.NodeTuningClusterOperator(),
	manifests.NamespaceKubeSystem(),
	manifests.UserCABundle(),
	manifests.OpenShiftUserCABundle(),
	&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
	manifests.ValidatingAdmissionPolicy(kas.AdmissionPolicyNameConfig),
	manifests.ValidatingAdmissionPolicy(kas.AdmissionPolicyNameMirror),
	manifests.ValidatingAdmissionPolicy(kas.AdmissionPolicyNameICSP),
	manifests.ValidatingAdmissionPolicy(kas.AdmissionPolicyNameInfra),
	manifests.ValidatingAdmissionPolicy(kas.AdmissionPolicyNameNTOMirroredConfigs),
	manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", kas.AdmissionPolicyNameConfig)),
	manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", kas.AdmissionPolicyNameMirror)),
	manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", kas.AdmissionPolicyNameICSP)),
	manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", kas.AdmissionPolicyNameInfra)),

	fakeOperatorHub(),
	manifests.KASConnectionCheckerDeployment(),
	manifests.KASConnectionCheckerServiceAccount(),
	manifests.MetricsForwarderDeployment(),
	manifests.MetricsForwarderConfigMap(),
	manifests.MetricsForwarderServingCA(),
	manifests.MetricsForwarderPodMonitor(),
}

func shouldNotError(key client.ObjectKey) bool {
	for _, o := range initialObjects {
		if client.ObjectKeyFromObject(o).String() == key.String() {
			return true
		}
	}
	return false
}

func (c *testClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.randomGetErrors && !shouldNotError(key) {
		if randomSource.Int()%3 == 0 {
			c.getErrorCount++
			return fmt.Errorf("random error")
		}
	}
	return c.Client.Get(ctx, key, obj)
}

var cpObjects = []client.Object{
	fakeHCP(),
	fakeIngressCert(),
	fakePullSecret(),
	fakeControlPlaneKonnectivityCAConfigMap(),
	fakeKonnectivityAgentSecret(),
	fakeRootCASecret(),
	fakeOpenShiftAPIServerService(),
	fakeOpenShiftOAuthAPIServerService(),
	fakeKubeadminPasswordSecret(),
	fakeOAuthMasterCABundle(),
	fakePackageServerService(),
}

// TestReconcileErrorHandling verifies that the reconcile loop proceeds when
// errors occur.  The test uses a fake  client with a specific list of initial
// objects in order to establish a baseline number of expected client create
// calls.  Then, the test runs the reconcile loop another 100 times starting
// with the same list of initial objects each time but with the test client
// configured to inject random errors in response to client get calls; to pass
// the test, the reconcile loop must make a number of client create calls equal
// to the baseline plus the number of injected errors, the assumption being that
// each client get corresponds to a client create and that a failed get will
// prevent the corresponding client create call from being made.
//
// To prevent false positives in this test, any object for which the reconcile
// loop makes a client get call without a corresponding client create call must
// be included in the list of initial objects.  Error injection is suppressed
// for the initial objects.
func TestReconcileErrorHandling(t *testing.T) {
	// get initial number of creates with no get errors
	imageMetaDataProvider := fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{}
	ctx := logr.NewContext(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))
	errorExceptions := []string{
		"global pull secret syncer signaled to shutdown",
	}

	var totalCreates int
	{
		fakeClient := &testClient{
			Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).WithStatusSubresource(&configv1.Infrastructure{}).Build(),
		}
		uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build()

		r := &reconciler{
			client:                 fakeClient,
			uncachedClient:         uncachedClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Components: map[string]string{
					"cli": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake",
				},
			},
			ImageMetaDataProvider: &imageMetaDataProvider,
		}
		_, err := r.Reconcile(ctx, controllerruntime.Request{})
		if err != nil {
			for _, exception := range errorExceptions {
				if strings.Contains(err.Error(), exception) {
					continue
				}
				t.Fatalf("unexpected error: %v", err)
			}
		}
		totalCreates = fakeClient.createCount
	}

	// test with random get errors
	for i := 0; i < 100; i++ {
		fakeClient := &testClient{
			Client:          fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).WithStatusSubresource(&configv1.Infrastructure{}).Build(),
			randomGetErrors: true,
		}
		r := &reconciler{
			client:                 fakeClient,
			uncachedClient:         fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build(),
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
			releaseProvider: &fakereleaseprovider.FakeReleaseProvider{
				Components: map[string]string{
					"cli": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake",
				},
			},
			ImageMetaDataProvider: &imageMetaDataProvider,
		}
		_, err := r.Reconcile(ctx, controllerruntime.Request{})
		if err != nil {
			for _, exception := range errorExceptions {
				if strings.Contains(err.Error(), exception) {
					continue
				}
			}
			if totalCreates-fakeClient.getErrorCount != fakeClient.createCount {
				t.Fatalf("Unexpected number of creates: %d/%d with errors %d", fakeClient.createCount, totalCreates, fakeClient.getErrorCount)
			}
		}
	}
}

func TestReconcileOLM(t *testing.T) {
	t.Parallel()
	var errs []error
	hcp := fakeHCP()
	hcp.Namespace = "openshift-operator-lifecycle-manager"
	fakeCPService := manifests.OLMPackageServerControlPlaneService(hcp.Namespace)
	fakeCPService.Spec.ClusterIP = "172.30.108.248"
	rootCA := cpomanifests.RootCASecret(hcp.Namespace)
	ctx := t.Context()
	pullSecret := fakePullSecret()

	imageMetaDataProvider := fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{}

	testCases := []struct {
		name                string
		hcpClusterConfig    *hyperv1.ClusterConfiguration
		olmCatalogPlacement hyperv1.OLMCatalogPlacement
		want                *configv1.OperatorHubSpec
	}{
		{
			name:                "PlacementStrategy is management and no configuration provided",
			hcpClusterConfig:    nil,
			olmCatalogPlacement: hyperv1.ManagementOLMCatalogPlacement,
			want:                &configv1.OperatorHubSpec{},
		},
		{
			name: "PlacementStrategy is management and allDefaultSources disabled",
			hcpClusterConfig: &hyperv1.ClusterConfiguration{
				OperatorHub: &configv1.OperatorHubSpec{
					DisableAllDefaultSources: true,
				},
			},
			olmCatalogPlacement: hyperv1.ManagementOLMCatalogPlacement,
			want: &configv1.OperatorHubSpec{
				DisableAllDefaultSources: true,
			},
		},
		{
			name: "PlacementStrategy is management and allDefaultSources enabled",
			hcpClusterConfig: &hyperv1.ClusterConfiguration{
				OperatorHub: &configv1.OperatorHubSpec{
					DisableAllDefaultSources: false,
				},
			},
			olmCatalogPlacement: hyperv1.ManagementOLMCatalogPlacement,
			want: &configv1.OperatorHubSpec{
				DisableAllDefaultSources: false,
			},
		},
		{
			name:                "PlacementStrategy is guest and no configuration provided",
			hcpClusterConfig:    nil,
			olmCatalogPlacement: hyperv1.GuestOLMCatalogPlacement,
			want:                &configv1.OperatorHubSpec{},
		},
		{
			// We expect here the OperatorHub in guest to keep the already set value and
			// don't overwrite the value with the new one.
			name: "PlacementStrategy is guest and allDefaultSources disabled, the first reconciliation loop already happened",
			hcpClusterConfig: &hyperv1.ClusterConfiguration{
				OperatorHub: &configv1.OperatorHubSpec{
					DisableAllDefaultSources: true,
				},
			},
			olmCatalogPlacement: hyperv1.GuestOLMCatalogPlacement,
			want: &configv1.OperatorHubSpec{
				DisableAllDefaultSources: false,
			},
		},
		{
			name: "PlacementStrategy is guest and allDefaultSources enabled",
			hcpClusterConfig: &hyperv1.ClusterConfiguration{
				OperatorHub: &configv1.OperatorHubSpec{
					DisableAllDefaultSources: false,
				},
			},
			olmCatalogPlacement: hyperv1.GuestOLMCatalogPlacement,
			want: &configv1.OperatorHubSpec{
				DisableAllDefaultSources: false,
			},
		},
	}

	cpClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(rootCA, fakeCPService, hcp).
		Build()
	hcCLient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(rootCA, pullSecret).
		Build()

	r := &reconciler{
		client:                 hcCLient,
		cpClient:               cpClient,
		CreateOrUpdateProvider: &simpleCreateOrUpdater{},
		rootCA:                 "fake",
		ImageMetaDataProvider:  &imageMetaDataProvider,
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			errs = append(errs, r.reconcileOLM(ctx, hcp, pullSecret)...)
			hcp.Spec.Configuration = tc.hcpClusterConfig
			hcp.Spec.OLMCatalogPlacement = tc.olmCatalogPlacement
			errs = append(errs, r.reconcileOLM(ctx, hcp, pullSecret)...)
			g.Expect(errs).To(BeEmpty(), "unexpected errors")
			hcOpHub := manifests.OperatorHub()
			err := r.client.Get(ctx, client.ObjectKeyFromObject(hcOpHub), hcOpHub)
			g.Expect(err).To(BeNil(), "error checking HC OperatorHub")
			g.Expect(hcOpHub.Spec).To(Equal(*tc.want))
		})
	}
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c, obj, f)
}

func fakeHCP() *hyperv1.HostedControlPlane {
	hcp := manifests.HostedControlPlane("bar", "foo")
	hcp.Status.ControlPlaneEndpoint.Host = "server"
	hcp.Status.ControlPlaneEndpoint.Port = 1234
	hcp.Spec.PullSecret = corev1.LocalObjectReference{Name: "pull-secret"}
	hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64"
	return hcp
}

func fakeIngressCert() *corev1.Secret {
	s := cpomanifests.IngressCert("bar")
	s.Data = map[string][]byte{
		"tls.crt": []byte("12345"),
		"tls.key": []byte("12345"),
	}
	return s
}

func fakePullSecret() *corev1.Secret {
	s := manifests.PullSecret("bar")
	s.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(`{
		"auths": {
			"registry.redhat.io/redhat/": {
				"auth": "dXNlcm5hbWU6cGFzc3dvcmQ=",
				"email": "user@example.com"
			}
		}
	}`),
	}
	return s
}

func fakeControlPlaneKonnectivityCAConfigMap() *corev1.ConfigMap {
	cm := manifests.KonnectivityControlPlaneCAConfigMap("bar")
	cm.Data = map[string]string{
		"ca.crt": "tehca",
	}
	return cm
}

func fakeKonnectivityAgentSecret() *corev1.Secret {
	s := manifests.KonnectivityControlPlaneAgentSecret("bar")
	s.Data = map[string][]byte{
		"tls.crt": []byte("12345"),
		"tls.key": []byte("12345"),
	}
	return s
}

func fakeRootCASecret() *corev1.Secret {
	s := cpomanifests.RootCASecret("bar")
	s.Data = map[string][]byte{
		"ca.crt": []byte("12345"),
		"ca.key": []byte("12345"),
	}
	return s
}

func fakeOpenShiftAPIServerService() *corev1.Service {
	s := manifests.OpenShiftAPIServerService("bar")
	s.Spec.ClusterIP = "1.1.1.1"
	return s
}

func fakeOpenShiftOAuthAPIServerService() *corev1.Service {
	s := manifests.OpenShiftOAuthAPIServerService("bar")
	s.Spec.ClusterIP = "1.1.1.1"
	return s
}

func fakeKubeadminPasswordSecret() *corev1.Secret {
	s := manifests.KubeadminPasswordSecret("bar")
	s.Data = map[string][]byte{"password": []byte("test")}
	return s
}

func fakeOAuthMasterCABundle() *corev1.ConfigMap {
	s := cpomanifests.OpenShiftOAuthMasterCABundle("bar")
	s.Data = map[string]string{"ca.crt": "test"}
	return s
}

func fakePackageServerService() *corev1.Service {
	s := manifests.OLMPackageServerControlPlaneService("bar")
	s.Spec.ClusterIP = "1.1.1.1"
	return s
}

func fakeOperatorHub() *configv1.OperatorHub {
	return &configv1.OperatorHub{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func withICS(hcp *hyperv1.HostedControlPlane) *hyperv1.HostedControlPlane {
	hcpOriginal := hcp.DeepCopy()
	hcpOriginal.Spec.ImageContentSources = []hyperv1.ImageContentSource{
		{
			Source: "example.com/test",
			Mirrors: []string{
				// the number after test is in purpose to not fit to the source namespace name
				"mirror1.example.com/test1",
				"mirror2.example.com/test2",
			},
		},
		{
			Source: "sample.com/test",
			Mirrors: []string{
				"mirror1.sample.com/test1",
				"mirror2.sample.com/test2",
			},
		},
		{
			Source: "quay.io/test",
			Mirrors: []string{
				"mirror1.quay.io/test1",
				"mirror2.quay.io/test2",
			},
		},
	}

	return hcpOriginal
}

func TestReconcileKubeadminPasswordHashSecret(t *testing.T) {
	t.Parallel()
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"

	tests := map[string]struct {
		inputHCP                                 *hyperv1.HostedControlPlane
		inputObjects                             []client.Object
		existingHashSecret                       *corev1.Secret
		expectKubeadminPasswordHashSecretToExist bool
		expectHashPreserved                      bool
	}{
		"when kubeadminPasswordSecret exists the hash secret is created": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: manifests.KubeadminPasswordSecret(testNamespace).ObjectMeta,
					Data: map[string][]byte{
						"password": []byte(`adminpass`),
					},
				},
				&appsv1.Deployment{
					ObjectMeta: manifests.OAuthDeployment(testNamespace).ObjectMeta,
				},
			},
			expectKubeadminPasswordHashSecretToExist: true,
		},
		"When existing hash does not match the password it should regenerate the hash": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: manifests.KubeadminPasswordSecret(testNamespace).ObjectMeta,
					Data: map[string][]byte{
						"password": []byte(`adminpass`),
					},
				},
				&appsv1.Deployment{
					ObjectMeta: manifests.OAuthDeployment(testNamespace).ObjectMeta,
				},
			},
			existingHashSecret: &corev1.Secret{
				ObjectMeta: manifests.KubeadminPasswordHashSecret().ObjectMeta,
				Data: map[string][]byte{
					"kubeadmin": []byte("stale-non-matching-hash"),
				},
			},
			expectKubeadminPasswordHashSecretToExist: true,
		},
		"When hash already matches password it should not regenerate": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: manifests.KubeadminPasswordSecret(testNamespace).ObjectMeta,
					Data: map[string][]byte{
						"password": []byte(`adminpass`),
					},
				},
				&appsv1.Deployment{
					ObjectMeta: manifests.OAuthDeployment(testNamespace).ObjectMeta,
				},
			},
			existingHashSecret: &corev1.Secret{
				ObjectMeta: manifests.KubeadminPasswordHashSecret().ObjectMeta,
				Data: map[string][]byte{
					"kubeadmin": func() []byte {
						h, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.MinCost)
						return h
					}(),
				},
			},
			expectKubeadminPasswordHashSecretToExist: true,
			expectHashPreserved:                      true,
		},
		"when kubeadminPasswordSecret doesn't exist the hash secret is not created": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: manifests.OAuthDeployment(testNamespace).ObjectMeta,
				},
			},
			expectKubeadminPasswordHashSecretToExist: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			guestClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if test.existingHashSecret != nil {
				guestClientBuilder = guestClientBuilder.WithObjects(test.existingHashSecret)
			}
			r := &reconciler{
				client:                 guestClientBuilder.Build(),
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(test.inputObjects, test.inputHCP)...).Build(),
				hcpName:                testHCPName,
				hcpNamespace:           testNamespace,
			}
			err := r.reconcileKubeadminPasswordHashSecret(t.Context(), test.inputHCP)
			g.Expect(err).To(BeNil())
			if test.expectKubeadminPasswordHashSecretToExist {
				actualKubeAdminSecret := manifests.KubeadminPasswordHashSecret()
				err := r.client.Get(t.Context(), client.ObjectKeyFromObject(actualKubeAdminSecret), actualKubeAdminSecret)
				g.Expect(err).To(BeNil())
				g.Expect(actualKubeAdminSecret.Data["kubeadmin"]).ToNot(BeEmpty())
				if test.expectHashPreserved {
					g.Expect(actualKubeAdminSecret.Data["kubeadmin"]).To(Equal(test.existingHashSecret.Data["kubeadmin"]))
				}
				passwordSecret := manifests.KubeadminPasswordSecret(testNamespace)
				err = r.cpClient.Get(t.Context(), client.ObjectKeyFromObject(passwordSecret), passwordSecret)
				g.Expect(err).To(BeNil())
				g.Expect(bcrypt.CompareHashAndPassword(
					actualKubeAdminSecret.Data["kubeadmin"],
					passwordSecret.Data["password"],
				)).To(BeNil())
			} else {
				actualKubeAdminSecret := manifests.KubeadminPasswordHashSecret()
				err := r.client.Get(t.Context(), client.ObjectKeyFromObject(actualKubeAdminSecret), actualKubeAdminSecret)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
			actualOauthDeployment := manifests.OAuthDeployment(testNamespace)
			err = r.cpClient.Get(t.Context(), client.ObjectKeyFromObject(actualOauthDeployment), actualOauthDeployment)
			g.Expect(err).To(BeNil())
		})
	}
}

func TestReconcileUserCertCABundle(t *testing.T) {
	t.Parallel()
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"
	tests := map[string]struct {
		inputHCP              *hyperv1.HostedControlPlane
		inputObjects          []client.Object
		existingGuestObjects  []client.Object
		expectUserCAConfigMap bool
	}{
		"No AdditionalTrustBundle": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects:          []client.Object{},
			existingGuestObjects:  []client.Object{},
			expectUserCAConfigMap: false,
		},
		"AdditionalTrustBundle": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: cpomanifests.UserCAConfigMap(testNamespace).Name,
					},
				},
			},
			inputObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: cpomanifests.UserCAConfigMap(testNamespace).ObjectMeta,
					Data: map[string]string{
						"ca-bundle.crt": "acertxyz",
					},
				},
			},
			existingGuestObjects:  []client.Object{},
			expectUserCAConfigMap: true,
		},
		"AdditionalTrustBundle removed - should delete existing user-ca-bundle": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
			},
			inputObjects: []client.Object{},
			existingGuestObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: manifests.UserCABundle().ObjectMeta,
					Data: map[string]string{
						"ca-bundle.crt": "oldcertdata",
					},
				},
			},
			expectUserCAConfigMap: false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := &reconciler{
				client:                 fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.existingGuestObjects...).Build(),
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(test.inputObjects, test.inputHCP)...).Build(),
				hcpName:                testHCPName,
				hcpNamespace:           testNamespace,
			}
			err := r.reconcileUserCertCABundle(t.Context(), test.inputHCP)
			g.Expect(err).To(BeNil())
			guestUserCABundle := manifests.UserCABundle()
			if test.expectUserCAConfigMap {
				err := r.client.Get(t.Context(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
				g.Expect(err).To(BeNil())
				g.Expect(len(guestUserCABundle.Data["ca-bundle.crt"]) > 0).To(BeTrue())
			} else {
				err := r.client.Get(t.Context(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

var _ manifestReconciler = manifestAndReconcile[*rbacv1.ClusterRole]{}

func TestDestroyCloudResources(t *testing.T) {
	t.Parallel()
	originalConditionTime := time.Now().Add(-1 * time.Hour)
	fakeHostedControlPlane := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-namespace",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Conditions: []metav1.Condition{
					{
						Type:               string(hyperv1.CloudResourcesDestroyed),
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.Time{Time: originalConditionTime},
						Message:            "Not Done",
						Reason:             "NotDone",
					},
				},
			},
		}
	}

	verifyCleanupWebhook := func(g *WithT, c client.Client, hcp *hyperv1.HostedControlPlane) {
		wh := manifests.ResourceCreationBlockerWebhook()
		err := c.Get(t.Context(), client.ObjectKeyFromObject(wh), wh)
		g.Expect(err).ToNot(HaveOccurred())
		expected := manifests.ResourceCreationBlockerWebhook()
		reconcileCreationBlockerWebhook(expected, hcp)
		g.Expect(wh.Webhooks).To(BeEquivalentTo(expected.Webhooks))
	}

	managedImageRegistry := func() client.Object {
		config := manifests.Registry()
		config.Spec.ManagementState = "Managed"
		config.Status.Storage.ManagementState = "Managed"
		return config
	}

	verifyImageRegistryConfig := func(g *WithT, c, _ client.Client) {
		config := manifests.Registry()
		err := c.Get(t.Context(), client.ObjectKeyFromObject(config), config)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(config.Spec.ManagementState).To(Equal(operatorv1.Removed))
	}

	ingressController := func(name string) client.Object {
		return &operatorv1.IngressController{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "openshift-ingress-operator",
			},
		}
	}

	verifyIngressControllersRemoved := func(g *WithT, c, _ client.Client) {
		ingressControllers := &operatorv1.IngressControllerList{}
		err := c.List(t.Context(), ingressControllers)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(len(ingressControllers.Items)).To(Equal(0))
	}

	serviceLoadBalancer := func(name string) client.Object {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			},
		}
	}

	serviceLoadBalancerOwnedByIngressController := func(name string) client.Object {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   "default",
				Annotations: map[string]string{"ingresscontroller.operator.openshift.io/owning-ingresscontroller": "default"},
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
			},
		}
	}

	clusterIPService := func(name string) client.Object {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			},
		}
	}

	verifyServiceLoadBalancersRemoved := func(g *WithT, c client.Client) {
		services := &corev1.ServiceList{}
		err := c.List(t.Context(), services)
		g.Expect(err).ToNot(HaveOccurred())
		for _, svc := range services.Items {
			g.Expect(svc.Spec.Type).ToNot(Equal(corev1.ServiceTypeLoadBalancer))
		}
	}

	verifyServiceLoadBalancersOwnedByIngressControllerExists := func(name string, g *WithT, c client.Client) {
		service := serviceLoadBalancerOwnedByIngressController(name)
		err := c.Get(t.Context(), client.ObjectKeyFromObject(service), service)
		g.Expect(err).ToNot(HaveOccurred())
	}

	verifyServiceExists := func(name string, g *WithT, c client.Client) {
		service := clusterIPService(name)
		err := c.Get(t.Context(), client.ObjectKeyFromObject(service), service)
		g.Expect(err).ToNot(HaveOccurred())
	}

	pv := func(name string) client.Object {
		return &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
	}

	pvc := func(name string) client.Object {
		return &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
		}
	}

	pod := func(name string) client.Object {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{
					{
						Name: "pv",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "test",
							},
						},
					},
				},
			},
		}
	}

	verifyPVCsRemoved := func(g *WithT, c client.Client) {
		pvcs := &corev1.PersistentVolumeClaimList{}
		err := c.List(t.Context(), pvcs)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(len(pvcs.Items)).To(Equal(0))
	}

	verifyPodsRemoved := func(g *WithT, c client.Client) {
		pods := &corev1.PodList{}
		err := c.List(t.Context(), pods)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(len(pods.Items)).To(Equal(0))
	}

	verifyDoneCond := func(g *WithT, c client.Client) {
		hcp := fakeHostedControlPlane()
		err := c.Get(t.Context(), client.ObjectKeyFromObject(hcp), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
		g.Expect(cond).ToNot(BeNil())
	}

	verifyNotDoneCond := func(g *WithT, c client.Client) {
		hcp := fakeHostedControlPlane()
		err := c.Get(t.Context(), client.ObjectKeyFromObject(hcp), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	}

	tests := []struct {
		name             string
		existing         []client.Object
		existingUncached []client.Object
		verify           func(*WithT, client.Client, client.Client)
		verifyDoneCond   bool
	}{
		{
			name:           "no existing resources",
			verifyDoneCond: true,
		},
		{
			name: "image registry with storage",
			existing: []client.Object{
				managedImageRegistry(),
			},
			verify: verifyImageRegistryConfig,
		},
		{
			name: "existing ingress controller",
			existing: []client.Object{
				ingressController("default"),
				ingressController("foobar"),
			},
			verify: verifyIngressControllersRemoved,
		},
		{
			name: "existing service load balancers",
			existing: []client.Object{
				serviceLoadBalancer("foo"),
				serviceLoadBalancer("bar"),
				clusterIPService("baz"),
			},
			verify: func(g *WithT, c, _ client.Client) {
				verifyServiceLoadBalancersRemoved(g, c)
				verifyServiceExists("baz", g, c)
			},
			verifyDoneCond: true,
		},
		{
			name: "existing service load balancers owned by ingress controller",
			existing: []client.Object{
				serviceLoadBalancerOwnedByIngressController("bar"),
				clusterIPService("baz"),
			},
			verify: func(g *WithT, c, _ client.Client) {
				verifyServiceLoadBalancersOwnedByIngressControllerExists("bar", g, c)
				verifyServiceExists("baz", g, c)
			},
		},
		{
			name: "existing pv/pvc",
			existing: []client.Object{
				pv("foo"), pvc("foo"),
				pv("bar"), pvc("bar"),
			},
			existingUncached: []client.Object{
				pod("pod1"), pod("pod2"),
			},
			verify: func(g *WithT, c, uc client.Client) {
				verifyPVCsRemoved(g, c)
				verifyPodsRemoved(g, uc)
			},
		},
		{
			name: "existing everything",
			existing: []client.Object{
				managedImageRegistry(),
				ingressController("default"),
				ingressController("foobar"),
				serviceLoadBalancer("foo"),
				serviceLoadBalancer("bar"),
				clusterIPService("baz"),
				pv("foo"), pvc("foo"),
				pv("bar"), pvc("bar"),
			},
			existingUncached: []client.Object{
				pod("pod1"), pod("pod2"),
			},
			verify: func(g *WithT, c, uc client.Client) {
				verifyImageRegistryConfig(g, c, nil)
				verifyIngressControllersRemoved(g, c, nil)
				verifyServiceLoadBalancersRemoved(g, c)
				verifyServiceExists("baz", g, c)
				verifyPVCsRemoved(g, c)
				verifyPodsRemoved(g, uc)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			fakeHCP := fakeHostedControlPlane()
			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.existing...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.existingUncached...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			// Add KubeAPIServer deployment to cpClient so cleanup proceeds normally
			kasDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver",
					Namespace: fakeHCP.Namespace,
				},
			}
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeHCP, kasDeployment).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         uncachedClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				cleanupTracker:         supportutil.NewCleanupTracker(),
			}
			_, err := r.destroyCloudResources(t.Context(), fakeHCP)
			g.Expect(err).ToNot(HaveOccurred())
			verifyCleanupWebhook(g, guestClient, fakeHCP)
			if test.verify != nil {
				test.verify(g, guestClient, uncachedClient)
			}
			if test.verifyDoneCond {
				verifyDoneCond(g, cpClient)
			} else {
				verifyNotDoneCond(g, cpClient)
			}
		})
	}
}

func TestDestroyCloudResourcesWithKASUnavailable(t *testing.T) {
	t.Parallel()
	fakeHCP := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Status: hyperv1.HostedControlPlaneStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.CloudResourcesDestroyed),
					Status: metav1.ConditionFalse,
				},
			},
		},
	}

	tests := []struct {
		name                  string
		kasDeploymentExists   bool
		expectCleanupSkipped  bool
		expectFailureTracking bool
	}{
		{
			name:                 "KAS deployment not found - cleanup skipped",
			kasDeploymentExists:  false,
			expectCleanupSkipped: true,
		},
		{
			name:                 "KAS deployment exists - cleanup proceeds",
			kasDeploymentExists:  true,
			expectCleanupSkipped: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

			cpClientObjects := []client.Object{fakeHCP}
			if test.kasDeploymentExists {
				kasDeployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: fakeHCP.Namespace,
					},
				}
				cpClientObjects = append(cpClientObjects, kasDeployment)
			}

			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpClientObjects...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         uncachedClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				cleanupTracker:         supportutil.NewCleanupTracker(),
			}

			remaining, skipReason, err := r.ensureCloudResourcesDestroyed(t.Context(), fakeHCP)
			g.Expect(err).ToNot(HaveOccurred())

			if test.expectCleanupSkipped {
				// When KAS is unavailable, cleanup should be skipped with empty remaining
				g.Expect(remaining.Len()).To(Equal(0))
				g.Expect(skipReason).To(Equal("KubeAPIServerUnavailable"))
			} else {
				// When KAS is available, cleanup should proceed normally
				g.Expect(remaining.Len()).To(Equal(0))
				g.Expect(skipReason).To(BeEmpty())
			}
		})
	}
}

// mockNetError is a test helper that implements net.Error
type mockNetError struct {
	error
	timeout   bool
	temporary bool
}

func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return e.temporary }

func TestConnectionErrorTracking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		err                error
		expectedConnection bool
	}{
		{
			name:               "K8s timeout error",
			err:                apierrors.NewTimeoutError("request timeout", 5),
			expectedConnection: true,
		},
		{
			name:               "K8s server timeout error",
			err:                apierrors.NewServerTimeout(schema.GroupResource{Group: "", Resource: "pods"}, "get", 5),
			expectedConnection: true,
		},
		{
			name:               "K8s service unavailable error",
			err:                apierrors.NewServiceUnavailable("service unavailable"),
			expectedConnection: true,
		},
		{
			name: "net.Error with timeout",
			err: &mockNetError{
				error:   fmt.Errorf("connection timeout"),
				timeout: true,
			},
			expectedConnection: true,
		},
		{
			name: "net.Error temporary",
			err: &mockNetError{
				error:     fmt.Errorf("temporary network error"),
				temporary: true,
			},
			expectedConnection: true,
		},
		{
			name:               "wrapped net.Error",
			err:                fmt.Errorf("failed to connect: %w", &mockNetError{error: fmt.Errorf("connection refused"), timeout: false}),
			expectedConnection: true,
		},
		{
			name:               "other K8s error (not found)",
			err:                apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "test-pod"),
			expectedConnection: false,
		},
		{
			name:               "other error",
			err:                fmt.Errorf("permission denied"),
			expectedConnection: false,
		},
		{
			name:               "nil error",
			err:                nil,
			expectedConnection: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := isConnectionError(test.err)
			g.Expect(result).To(Equal(test.expectedConnection))
		})
	}
}

func TestListAccessor(t *testing.T) {
	t.Parallel()
	pod := func(name string) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "test-ns",
			},
		}
	}
	list := &corev1.PodList{
		Items: []corev1.Pod{
			pod("test1"),
			pod("test2"),
		},
	}

	a := listAccessor(list)
	g := NewGomegaWithT(t)
	g.Expect(a.len()).To(Equal(2))
	g.Expect(a.item(0).GetName()).To(Equal("test1"))
	g.Expect(a.item(1).GetName()).To(Equal("test2"))
}

func TestReconcileClusterVersion(t *testing.T) {
	t.Parallel()
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster-id",
		},
	}
	testOverrides := []configv1.ComponentOverride{
		{
			Kind:      "Pod",
			Group:     "",
			Name:      "test",
			Namespace: "default",
			Unmanaged: true,
		},
	}
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "some-other-id",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
					"foo",
					"bar",
				},
			},
			Channel: "fast",
			DesiredUpdate: &configv1.Update{
				Version: "4.12.5",
				Image:   "example.com/imagens/image:latest",
				Force:   true,
			},
			Upstream:  configv1.URL("https://upstream.example.com"),
			Overrides: testOverrides,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
	g := NewWithT(t)
	r := &reconciler{
		client:                 fakeClient,
		CreateOrUpdateProvider: &simpleCreateOrUpdater{},
	}
	err := r.reconcileClusterVersion(t.Context(), hcp)
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(clusterVersion.Spec.ClusterID).To(Equal(configv1.ClusterID("test-cluster-id")))
	expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			configv1.ClusterVersionCapabilityConsole,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			configv1.ClusterVersionCapabilityImageRegistry,
			configv1.ClusterVersionCapabilityIngress,
			configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityMarketplace,
			configv1.ClusterVersionCapabilityOpenShiftSamples,
		},
	}
	g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
	g.Expect(clusterVersion.Spec.DesiredUpdate).To(BeNil())
	g.Expect(clusterVersion.Spec.Overrides).To(Equal(testOverrides))
	g.Expect(clusterVersion.Spec.Channel).To(BeEmpty())
}

func TestReconcileClusterVersionWithDisabledCapabilities(t *testing.T) {
	t.Parallel()
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster-id",
			Capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{
					hyperv1.ImageRegistryCapability, hyperv1.OpenShiftSamplesCapability, hyperv1.InsightsCapability, hyperv1.ConsoleCapability, hyperv1.NodeTuningCapability, hyperv1.IngressCapability,
				},
			},
		},
	}
	testOverrides := []configv1.ComponentOverride{
		{
			Kind:      "Pod",
			Group:     "",
			Name:      "test",
			Namespace: "default",
			Unmanaged: true,
		},
	}
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "some-other-id",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
					"foo",
					"bar",
				},
			},
			Channel: "fast",
			DesiredUpdate: &configv1.Update{
				Version: "4.12.5",
				Image:   "example.com/imagens/image:latest",
				Force:   true,
			},
			Upstream:  configv1.URL("https://upstream.example.com"),
			Overrides: testOverrides,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
	g := NewWithT(t)
	r := &reconciler{
		client:                 fakeClient,
		CreateOrUpdateProvider: &simpleCreateOrUpdater{},
	}
	err := r.reconcileClusterVersion(t.Context(), hcp)
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())

	expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			//configv1.ClusterVersionCapabilityConsole,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			// configv1.ClusterVersionCapabilityImageRegistry,
			//configv1.ClusterVersionCapabilityIngress,
			//configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			//configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityMarketplace,
			// configv1.ClusterVersionCapabilityOpenShiftSamples,
		},
	}
	g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
}

func TestReconcileClusterVersionWithEnabledCapabilities(t *testing.T) {
	t.Parallel()
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster-id",
			Capabilities: &hyperv1.Capabilities{
				Enabled: []hyperv1.OptionalCapability{
					hyperv1.BaremetalCapability,
				},
			},
		},
	}
	testOverrides := []configv1.ComponentOverride{
		{
			Kind:      "Pod",
			Group:     "",
			Name:      "test",
			Namespace: "default",
			Unmanaged: true,
		},
	}
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: "some-other-id",
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
					"foo",
					"bar",
				},
			},
			Channel: "fast",
			DesiredUpdate: &configv1.Update{
				Version: "4.12.5",
				Image:   "example.com/imagens/image:latest",
				Force:   true,
			},
			Upstream:  configv1.URL("https://upstream.example.com"),
			Overrides: testOverrides,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(clusterVersion).Build()
	g := NewWithT(t)
	r := &reconciler{
		client:                 fakeClient,
		CreateOrUpdateProvider: &simpleCreateOrUpdater{},
	}
	err := r.reconcileClusterVersion(t.Context(), hcp)
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())

	expectedCapabilities := &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{
			configv1.ClusterVersionCapabilityBuild,
			configv1.ClusterVersionCapabilityCSISnapshot,
			configv1.ClusterVersionCapabilityCloudControllerManager,
			configv1.ClusterVersionCapabilityCloudCredential,
			configv1.ClusterVersionCapabilityConsole,
			configv1.ClusterVersionCapabilityDeploymentConfig,
			configv1.ClusterVersionCapabilityImageRegistry,
			configv1.ClusterVersionCapabilityIngress,
			configv1.ClusterVersionCapabilityInsights,
			configv1.ClusterVersionCapabilityMachineAPI,
			configv1.ClusterVersionCapabilityNodeTuning,
			configv1.ClusterVersionCapabilityOperatorLifecycleManager,
			configv1.ClusterVersionCapabilityOperatorLifecycleManagerV1,
			configv1.ClusterVersionCapabilityStorage,
			configv1.ClusterVersionCapabilityBaremetal,
			configv1.ClusterVersionCapabilityMarketplace,
			configv1.ClusterVersionCapabilityOpenShiftSamples,
		},
	}
	g.Expect(clusterVersion.Spec.Capabilities).To(Equal(expectedCapabilities))
}

func TestReconcileImageContentPolicyType(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                  string
		hcp                   *hyperv1.HostedControlPlane
		removeICSAndReconcile bool
	}{
		{
			name: "ICS with content, it should return an IDMS with the same content",
			hcp:  withICS(fakeHCP()),
		},
		{
			name: "ICS empty, is should return an empty IDMS",
			hcp:  fakeHCP(),
		},
		{
			name:                  "ICS And IDMS should be in sync always",
			hcp:                   withICS(fakeHCP()),
			removeICSAndReconcile: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hcp).Build()
			r := &reconciler{
				client:                 fakeClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}
			err := r.reconcileImageContentPolicyType(t.Context(), tc.hcp)
			g.Expect(err).ToNot(HaveOccurred())

			idms := globalconfig.ImageDigestMirrorSet()
			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)
			g.Expect(err).ToNot(HaveOccurred(), "error getting IDMS")

			// Same number of ICS and IDMS
			g.Expect(len(tc.hcp.Spec.ImageContentSources)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")

			if tc.hcp.Spec.ImageContentSources != nil {
				// Check if the ICS and IDMS have the same values
				compareICSAndIDMS(g, tc.hcp.Spec.ImageContentSources, idms)
			}

			if tc.removeICSAndReconcile {
				// Simulating a user updating the HCP and removing the ICS
				origHCP := tc.hcp.DeepCopy()
				origHCP.Spec.ImageContentSources = nil

				err = r.reconcileImageContentPolicyType(t.Context(), origHCP)
				g.Expect(err).ToNot(HaveOccurred())
				idms := globalconfig.ImageDigestMirrorSet()
				err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)
				g.Expect(err).ToNot(HaveOccurred(), "error getting IDMS")
				g.Expect(len(origHCP.Spec.ImageContentSources)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")
				compareICSAndIDMS(g, origHCP.Spec.ImageContentSources, idms)
			}
		})
	}
}

func compareICSAndIDMS(g *WithT, ics []hyperv1.ImageContentSource, idms *configv1.ImageDigestMirrorSet) {
	g.Expect(len(ics)).To(Equal(len(idms.Spec.ImageDigestMirrors)), "expecting equal values between IDMS and ICS")
	// Check if the ICS and IDMS have the same values
	for i, ics := range ics {
		g.Expect(ics.Source).To(Equal(idms.Spec.ImageDigestMirrors[i].Source))
		for j, mirrorics := range ics.Mirrors {
			g.Expect(mirrorics).To(Equal(string(idms.Spec.ImageDigestMirrors[i].Mirrors[j])))
		}
	}
}

func TestReconcileKASEndpoints(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		expectedPort int32
	}{
		{
			name: "When HC has hcp.spec.networking.apiServer.port set to 443, endpoint and slice should have port 443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port: ptr.To[int32](443),
						},
					},
				},
			},
			expectedPort: int32(443),
		},
		{
			name: "When HC has no hcp.spec.networking.apiServer.port set, endpoint and slice should have port 6443",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expectedPort: int32(6443),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &reconciler{
				client:                 fakeClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			err := r.reconcileKASEndpoints(t.Context(), tc.hcp)
			g.Expect(err).ToNot(HaveOccurred())

			//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
			endpoints := &corev1.Endpoints{}
			err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "kubernetes", Namespace: corev1.NamespaceDefault}, endpoints)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(endpoints.Subsets[0].Ports[0].Name).To(Equal("https"))
			g.Expect(endpoints.Subsets[0].Ports[0].Port).To(Equal(int32(tc.expectedPort)))

			endpointSlice := &discoveryv1.EndpointSlice{}
			err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "kubernetes", Namespace: corev1.NamespaceDefault}, endpointSlice)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(endpoints.Subsets[0].Ports[0].Name).To(Equal("https"))
			g.Expect(endpoints.Subsets[0].Ports[0].Port).To(Equal(int32(tc.expectedPort)))
		})
	}
}

func TestReconcileKubeletConfig(t *testing.T) {
	t.Parallel()
	hcpNamespace := "hostedcontrolplane-namespace"
	hcNamespace := "openshift-config-managed"
	npName1 := "nodepool-test1"
	npName2 := "nodepool-test2"
	kubeletConfig1 := `
    apiVersion: machineconfiguration.openshift.io/v1
    kind: KubeletConfig
    metadata:
      name: set-max-pods
    spec:
      kubeletConfig:
        maxPods: 100
`
	testCases := []struct {
		name                           string
		hostedControlPlaneObjects      []client.Object
		existHostedControlPlaneObjects []client.Object
		expectedHostedClusterObjects   []client.Object
		preservedObjects               []client.Object
	}{
		{
			name: "copy kubelet config from control plane NS",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
		{
			name: "some CM already exist and some are not, expect HCCO to catch up",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(netutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(netutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
		{
			name: "CM need to be deleted",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(netutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
		{
			name:                      "When source CM is transiently absent, it should not delete the mirrored guest-side CM",
			hostedControlPlaneObjects: []client.Object{},
			existHostedControlPlaneObjects: []client.Object{
				makeMirroredKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, npName1, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeMirroredKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, npName1, kubeletConfig1),
			},
		},
		{
			// Defensive: this path is only reachable for CMs created before NTOMirroredConfigLabel was introduced.
			name:                      "When source CM is absent and guest CM is not mirrored, it should be deleted",
			hostedControlPlaneObjects: []client.Object{},
			existHostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{},
		},
		{
			name: "When guest CM is immutable but not a KubeletConfig, it should not be deleted",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				// Immutable CM without KubeletConfigConfigMapLabel — should be left alone.
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unrelated-immutable-cm",
						Namespace: hcNamespace,
						Labels:    map[string]string{"some-other-label": "true"},
					},
					Immutable: ptr.To(true),
					Data:      map[string]string{"key": "value"},
				},
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			preservedObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unrelated-immutable-cm",
						Namespace: hcNamespace,
					},
				},
			},
		},
		{
			name: "When guest CM is immutable, it should be deleted and recreated as mutable",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				makeImmutableKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(netutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cpFakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hostedControlPlaneObjects...).Build()
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.existHostedControlPlaneObjects...).Build()
			r := &reconciler{
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				client:                 fakeClient,
				cpClient:               cpFakeClient,
			}
			g.Expect(r.reconcileKubeletConfig(t.Context())).To(Succeed())
			for _, obj := range tc.expectedHostedClusterObjects {
				actual := &corev1.ConfigMap{}
				g.Expect(r.client.Get(t.Context(), client.ObjectKeyFromObject(obj), actual)).To(Succeed(), "failed to get %s", client.ObjectKeyFromObject(obj))
				g.Expect(actual.Immutable).To(BeNil(), "recreated ConfigMap %s should be mutable", client.ObjectKeyFromObject(obj))
			}
			for _, obj := range tc.preservedObjects {
				actual := &corev1.ConfigMap{}
				g.Expect(r.client.Get(t.Context(), client.ObjectKeyFromObject(obj), actual)).To(Succeed(),
					"preserved object %s should still exist after reconcile", client.ObjectKeyFromObject(obj))
			}
			listOpts := []client.ListOption{
				client.InNamespace(hcNamespace),
				client.MatchingLabels{
					nodepool.KubeletConfigConfigMapLabel: "true",
				},
			}
			cmList := &corev1.ConfigMapList{}
			g.Expect(r.client.List(t.Context(), cmList, listOpts...)).To(Succeed(), "failed to list KubeletConfig ConfigMap")
			expectedLen := len(tc.expectedHostedClusterObjects)
			g.Expect(cmList.Items).To(HaveLen(expectedLen), "more ConfigMaps found then expected; got=%d want=%", len(cmList.Items), expectedLen)
		})
	}
}

func TestBuildAWSWebIdentityCredentials(t *testing.T) {
	t.Parallel()
	type args struct {
		roleArn string
		region  string
	}
	type test struct {
		name    string
		args    args
		wantErr bool
		want    string
	}
	tests := []test{
		{
			name: "should fail if the role ARN is empty",
			args: args{
				roleArn: "",
				region:  "us-east-1",
			},
			wantErr: true,
		},
		{
			name:    "should fail if the region is empty",
			wantErr: true,
			args: args{
				roleArn: "arn:aws:iam::123456789012:role/some-role",
				region:  "",
			},
		},
		{
			name:    "should succeed and return the creds template populated with role arn and region otherwise",
			wantErr: false,
			args: args{
				roleArn: "arn:aws:iam::123456789012:role/some-role",
				region:  "us-east-1",
			},
			want: `[default]
role_arn = arn:aws:iam::123456789012:role/some-role
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
sts_regional_endpoints = regional
region = us-east-1
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := buildAWSWebIdentityCredentials(tt.args.roleArn, tt.args.region)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildAWSWebIdentityCredentials err = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if creds != tt.want {
				t.Errorf("expected creds:\n%s, but got:\n%s", tt.want, creds)
			}
		})
	}
}

func makeKubeletConfigConfigMap(name, namespace, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				nodepool.KubeletConfigConfigMapLabel: "true",
			},
		},
		Data: map[string]string{
			"config": data,
		},
	}
}

func makeMirroredKubeletConfigConfigMap(name, namespace, nodePoolName, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				nodepool.KubeletConfigConfigMapLabel: "true",
				nodepool.NTOMirroredConfigLabel:      "true",
				hyperv1.NodePoolLabel:                nodePoolName,
			},
		},
		Data: map[string]string{
			"config": data,
		},
	}
}

func makeImmutableKubeletConfigConfigMap(name, namespace, data string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				nodepool.KubeletConfigConfigMapLabel: "true",
			},
		},
		Immutable: ptr.To(true),
		Data: map[string]string{
			"config": data,
		},
	}
}

func TestReconcileAuthOIDC(t *testing.T) {
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"

	tests := map[string]struct {
		inputHCP                *hyperv1.HostedControlPlane
		inputCPObjects          []client.Object
		expectIssuerCAConfigMap bool
		expectOIDCClientSecrets []string
		expectErrors            bool
		expectedErrorMessages   []string
		setAROHCP               bool
	}{
		"when OAuth is enabled, should not copy OIDC resources": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: []configv1.IdentityProvider{
								{
									Name: "test-provider",
									IdentityProviderConfig: configv1.IdentityProviderConfig{
										Type: configv1.IdentityProviderTypeHTPasswd,
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects:          []client.Object{},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{},
			expectErrors:            false,
		},
		"when OAuth is disabled and no OIDC providers, should not copy anything": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
						},
					},
				},
			},
			inputCPObjects:          []client.Object{},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{},
			expectErrors:            false,
		},
		"when OAuth is disabled with OIDC provider with CA configmap, should copy CA": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
										CertificateAuthority: configv1.ConfigMapNameReference{
											Name: "oidc-ca-bundle",
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "oidc-ca-bundle",
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": "test-ca-certificate",
					},
				},
			},
			expectIssuerCAConfigMap: true,
			expectOIDCClientSecrets: []string{},
			expectErrors:            false,
		},
		"when OAuth is disabled with OIDC provider with OIDC clients, should copy client secrets": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "console-client-secret",
											},
										},
										{
											ComponentName:      "cli",
											ComponentNamespace: "openshift-authentication",
											ClientID:           "cli-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "cli-client-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "console-client-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("console-secret-value"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cli-client-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("cli-secret-value"),
					},
				},
			},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{"console-client-secret", "cli-client-secret"},
			expectErrors:            false,
		},
		"when OAuth is disabled with OIDC provider with both CA and client secrets, should copy both": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
										CertificateAuthority: configv1.ConfigMapNameReference{
											Name: "oidc-ca-bundle",
										},
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "console-client-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "oidc-ca-bundle",
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": "test-ca-certificate",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "console-client-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("console-secret-value"),
					},
				},
			},
			expectIssuerCAConfigMap: true,
			expectOIDCClientSecrets: []string{"console-client-secret"},
			expectErrors:            false,
		},
		"when OAuth is disabled with OIDC provider with confidential and public OIDC clients, should copy confidential client secret": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "console-client-secret",
											},
										},
										{
											ComponentName:      "cli",
											ComponentNamespace: "openshift-authentication",
											ClientID:           "cli-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "console-client-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("console-secret-value"),
					},
				},
			},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{"console-client-secret"},
			expectErrors:            false,
		},
		"when OAuth is disabled with OIDC provider with a hosted-cluster-sourced annotated client secret and ARO-HCP platform, should not copy the client secret": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Private: hyperv1.AzurePrivateSpec{
								Type: hyperv1.AzurePrivateTypeSwift,
								Swift: hyperv1.AzureSwiftSpec{
									PodNetworkInstance: "test-pni",
								},
							},
							AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
								AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
							},
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "console-client-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "console-client-secret",
						Namespace: testNamespace,
						Annotations: map[string]string{
							hyperv1.HostedClusterSourcedAnnotation: "true",
						},
					},
				},
			},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{},
			expectErrors:            false,
			setAROHCP:               true,
		},
		"when OAuth is disabled with OIDC provider and not ARO-HCP platform, setting hosted-cluster-sourced annotation on a client secret should not skip copying the secret": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "console-client-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "console-client-secret",
						Namespace: testNamespace,
						Annotations: map[string]string{
							hyperv1.HostedClusterSourcedAnnotation: "true",
						},
					},
					Data: map[string][]byte{
						"clientSecret": []byte("console-secret-value"),
					},
				},
			},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{"console-client-secret"},
			expectErrors:            false,
			setAROHCP:               false,
		},
		"when OAuth is disabled but CA configmap is missing, should return error": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
										CertificateAuthority: configv1.ConfigMapNameReference{
											Name: "missing-ca-bundle",
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects:          []client.Object{},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{},
			expectErrors:            true,
			expectedErrorMessages:   []string{"failed to get issuer CA configmap missing-ca-bundle"},
		},
		"when OAuth is disabled but client secret is missing, should return error": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "test-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://example.com",
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "missing-client-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects:          []client.Object{},
			expectIssuerCAConfigMap: false,
			expectOIDCClientSecrets: []string{},
			expectErrors:            true,
			expectedErrorMessages:   []string{"failed to get OIDCClient secret missing-client-secret"},
		},
		"when OAuth is disabled with multiple OIDC providers, should handle first provider only": {
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testHCPName,
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: &configv1.AuthenticationSpec{
							Type: configv1.AuthenticationTypeOIDC,
							OIDCProviders: []configv1.OIDCProvider{
								{
									Name: "first-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://first.example.com",
										CertificateAuthority: configv1.ConfigMapNameReference{
											Name: "first-ca-bundle",
										},
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "first-console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "first-console-secret",
											},
										},
									},
								},
								{
									Name: "second-oidc-provider",
									Issuer: configv1.TokenIssuer{
										URL: "https://second.example.com",
										CertificateAuthority: configv1.ConfigMapNameReference{
											Name: "second-ca-bundle",
										},
									},
									OIDCClients: []configv1.OIDCClientConfig{
										{
											ComponentName:      "console",
											ComponentNamespace: "openshift-console",
											ClientID:           "second-console-client",
											ClientSecret: configv1.SecretNameReference{
												Name: "second-console-secret",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			inputCPObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "first-ca-bundle",
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": "first-ca-certificate",
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "second-ca-bundle",
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": "second-ca-certificate",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "first-console-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("first-console-secret-value"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "second-console-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"clientSecret": []byte("second-console-secret-value"),
					},
				},
			},
			expectIssuerCAConfigMap: true,
			expectOIDCClientSecrets: []string{"first-console-secret"}, // Only first provider should be processed
			expectErrors:            false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()

			cpClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(test.inputCPObjects...).
				Build()

			hcClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				Build()

			r := &reconciler{
				client:                 hcClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			if test.setAROHCP {
				azureutil.SetAsAroHCPTest(t)
			}

			// Verify that CA configmaps and OIDC client secrets don't exist in hosted cluster before reconciliation
			if test.expectIssuerCAConfigMap {
				provider := test.inputHCP.Spec.Configuration.Authentication.OIDCProviders[0]
				caConfigMap := &corev1.ConfigMap{}
				err := hcClient.Get(ctx, client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      provider.Issuer.CertificateAuthority.Name,
				}, caConfigMap)
				g.Expect(err).To(HaveOccurred())
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "CA configmap should not exist before reconciliation")
			}

			for _, secretName := range test.expectOIDCClientSecrets {
				clientSecret := &corev1.Secret{}
				err := hcClient.Get(ctx, client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      secretName,
				}, clientSecret)
				g.Expect(err).To(HaveOccurred())
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "OIDC client secret should not exist before reconciliation")
			}

			err := r.reconcileAuthOIDC(ctx, test.inputHCP)

			if test.expectErrors {
				g.Expect(err).To(HaveOccurred())
				errorStr := err.Error()
				for _, expectedMsg := range test.expectedErrorMessages {
					g.Expect(errorStr).To(ContainSubstring(expectedMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			// Check if issuer CA configmap was copied to openshift-config namespace
			if test.expectIssuerCAConfigMap {
				if test.inputHCP.Spec.Configuration != nil &&
					test.inputHCP.Spec.Configuration.Authentication != nil &&
					len(test.inputHCP.Spec.Configuration.Authentication.OIDCProviders) > 0 {
					provider := test.inputHCP.Spec.Configuration.Authentication.OIDCProviders[0]
					caConfigMap := &corev1.ConfigMap{}
					err := hcClient.Get(ctx, client.ObjectKey{
						Namespace: ConfigNamespace,
						Name:      provider.Issuer.CertificateAuthority.Name,
					}, caConfigMap)
					g.Expect(err).ToNot(HaveOccurred())
					// Get expected CA certificate from the test case input objects
					expectedCA := ""
					for _, obj := range test.inputCPObjects {
						if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == provider.Issuer.CertificateAuthority.Name {
							expectedCA = cm.Data["ca-bundle.crt"]
							break
						}
					}
					g.Expect(caConfigMap.Data["ca-bundle.crt"]).To(Equal(expectedCA))
				}
			}

			// Check if OIDC client secrets were copied to openshift-config namespace
			for _, secretName := range test.expectOIDCClientSecrets {
				clientSecret := &corev1.Secret{}
				err := hcClient.Get(ctx, client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      secretName,
				}, clientSecret)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clientSecret.Data["clientSecret"]).ToNot(BeEmpty())
			}

			// Verify that unexpected resources were not created
			if !test.expectIssuerCAConfigMap &&
				test.inputHCP.Spec.Configuration != nil &&
				test.inputHCP.Spec.Configuration.Authentication != nil &&
				len(test.inputHCP.Spec.Configuration.Authentication.OIDCProviders) > 0 {
				provider := test.inputHCP.Spec.Configuration.Authentication.OIDCProviders[0]
				if provider.Issuer.CertificateAuthority.Name != "" {
					caConfigMap := &corev1.ConfigMap{}
					err := hcClient.Get(ctx, client.ObjectKey{
						Namespace: ConfigNamespace,
						Name:      provider.Issuer.CertificateAuthority.Name,
					}, caConfigMap)
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				}
			}
			// Verify that no unexpected client secrets were copied
			secretList := &corev1.SecretList{}
			err = hcClient.List(ctx, secretList, client.InNamespace(ConfigNamespace))
			g.Expect(err).ToNot(HaveOccurred())

			expectedSecrets := sets.New(test.expectOIDCClientSecrets...)

			for _, secret := range secretList.Items {
				if !expectedSecrets.Has(secret.Name) {
					t.Errorf("unexpected OIDC client secret copied: %s", secret.Name)
				}
			}
		})
	}
}

func newCondition(conditionType string, status metav1.ConditionStatus, reason, message string) *metav1.Condition {
	return &metav1.Condition{
		Type:    conditionType,
		Reason:  reason,
		Status:  status,
		Message: message,
	}
}

func Test_reconciler_reconcileDataPlaneConnectionAvailable(t *testing.T) {
	t.Parallel()
	newKonnectivityAgentPod := func(name string, phase corev1.PodPhase) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "kube-system",
				Labels:    map[string]string{"app": "konnectivity-agent"},
			},
			Status: corev1.PodStatus{
				Phase: phase,
			},
		}
	}
	newRunningNode := func(name string) corev1.Node {
		return corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: corev1.NodeSpec{
				Unschedulable: false,
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}
	}

	tests := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		wantErr           bool
		expectedCondition *metav1.Condition
		pods              []corev1.Pod
		nodes             []corev1.Node
		mockedGetPodLogs  func(context context.Context, clientet *clientset.Clientset, namespace, name, container string) ([]byte, error)
	}{
		{
			name:    "no worker nodes Condition Unknown",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionUnknown, hyperv1.DataPlaneConnectionNoWorkerNodesAvailableReason, "No worker nodes available"),
			nodes: []corev1.Node{}, // no Nodes
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return nil, nil
			},
		},
		{
			name:    "no konnectivity-agent PODs condition False",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionFalse, hyperv1.DataPlaneConnectionNoKonnectivityAgentPodsNotFoundReason, "Couldn't find any konnectivity-agent running in data plane"),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods:  []corev1.Pod{}, // no Pods
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return nil, nil
			},
		},
		{
			name:    "only one pending POD condition False",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionFalse, hyperv1.DataPlaneConnectionNoKonnectivityAgentPodsNotFoundReason, "Couldn't find any konnectivity-agent running in data plane"),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods:  []corev1.Pod{newKonnectivityAgentPod("konnectivity-agent-rdax", corev1.PodPending)},
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return nil, nil
			},
		},
		{
			name:    "one konnectivity-agent PODs running condition OK",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionTrue, hyperv1.AsExpectedReason, hyperv1.AllIsWellMessage),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods:  []corev1.Pod{newKonnectivityAgentPod("konnectivity-agent-rdax", corev1.PodRunning)},
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return []byte("this is the log my friend"), nil
			},
		},
		{
			name:    "may konnectivity-agent PODs only one running condition OK",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionTrue, hyperv1.AsExpectedReason, hyperv1.AllIsWellMessage),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods: []corev1.Pod{newKonnectivityAgentPod("konnectivity-agent-rdax1", corev1.PodPending),
				newKonnectivityAgentPod("konnectivity-agent-rdax2", corev1.PodPending),
				newKonnectivityAgentPod("konnectivity-agent-rdax3", corev1.PodRunning),
				newKonnectivityAgentPod("konnectivity-agent-rdax4", corev1.PodPending),
				newKonnectivityAgentPod("konnectivity-agent-rdax5", corev1.PodPending)},
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return []byte("this is the log my friend"), nil
			},
		},
		{
			name:    "one konnectivity-agent PODs running bad since error getting LOG",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionFalse, hyperv1.DataPlaneConnectionNoKonnectivityAgentPodsNotFoundReason,
				"failed to read konnectivity-agent logs from data plane"),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods:  []corev1.Pod{newKonnectivityAgentPod("konnectivity-agent-rdax", corev1.PodRunning)},
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return nil, fmt.Errorf("this time we fail")
			},
		},
		{
			name:    "one konnectivity-agent PODs running bad since no LOG", // unsure this is possible
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(string(hyperv1.DataPlaneConnectionAvailable),
				metav1.ConditionFalse, hyperv1.DataPlaneConnectionNoKonnectivityAgentPodsNotFoundReason,
				"failed to read konnectivity-agent logs from data plane"),
			nodes: []corev1.Node{newRunningNode("node1")},
			pods:  []corev1.Pod{newKonnectivityAgentPod("konnectivity-agent-rdax", corev1.PodRunning)},
			mockedGetPodLogs: func(context context.Context,
				clientet *clientset.Clientset,
				namespace, name,
				container string) ([]byte, error) {
				return nil, nil
			},
		},
	}
	log := zapr.NewLogger(zaptest.NewLogger(t))
	ctx := logr.NewContext(context.Background(), log)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r reconciler
			nodeList := &corev1.NodeList{
				Items: tt.nodes,
			}
			podList := &corev1.PodList{
				Items: tt.pods,
			}
			r.client = fake.NewClientBuilder().WithLists(nodeList).Build()
			r.uncachedClient = fake.NewClientBuilder().WithLists(podList).Build()
			r.cpClient = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.hcp).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			r.GetPodLogs = tt.mockedGetPodLogs

			gotErr := r.reconcileDataPlaneConnectionAvailable(ctx, tt.hcp, log)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("reconcileDataPlaneConnectionAvailable() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("reconcileDataPlaneConnectionAvailable() succeeded unexpectedly")
			}
			if tt.expectedCondition != nil {
				found := false
				for _, c := range tt.hcp.Status.Conditions {
					if tt.expectedCondition.Type == c.Type &&
						tt.expectedCondition.Message == c.Message &&
						tt.expectedCondition.Status == c.Status &&
						tt.expectedCondition.Reason == c.Reason {
						found = true
					}
				}
				if !found {
					t.Fatal("couldn't find expected condition")
				}
			}
		})
	}
}

func Test_reconciler_reconcileControlPlaneConnectionAvailable(t *testing.T) {
	t.Parallel()
	newConnectivityConfigMap := func(data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifests.KASConnectionCheckerConfigMapName,
				Namespace: manifests.KASConnectionCheckerNamespace,
			},
			Data: data,
		}
	}

	newReadyNode := func(name string) corev1.Node {
		return corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: corev1.NodeSpec{
				Unschedulable: false,
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		}
	}

	tests := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		wantErr           bool
		expectedCondition *metav1.Condition
		configMap         *corev1.ConfigMap
		nodes             []corev1.Node
	}{
		{
			name:    "When no worker nodes exist it should set condition to Unknown with NoWorkerNodesAvailable reason",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionUnknown,
				hyperv1.ControlPlaneConnectionNoWorkerNodesAvailableReason,
				"No worker nodes available to verify control plane connectivity",
			),
			configMap: nil,
			nodes:     []corev1.Node{},
		},
		{
			name:    "When ConfigMap does not exist it should set condition to Unknown with ConfigMapNotFound reason",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionUnknown,
				hyperv1.ControlPlaneConnectionConfigMapNotFoundReason,
				fmt.Sprintf("Connectivity check ConfigMap %s/%s not found; the hosted cluster config operator may not have reconciled it yet",
					manifests.KASConnectionCheckerNamespace, manifests.KASConnectionCheckerConfigMapName),
			),
			configMap: nil,
			nodes:     []corev1.Node{newReadyNode("node1")},
		},
		{
			name:    "When ConfigMap has no lastSucceeded key it should set condition to False with KASAccessFailed reason",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionFalse,
				hyperv1.ControlPlaneConnectionKASAccessFailedReason,
				"Data plane to control plane connection is not available: no successful connectivity check recorded",
			),
			configMap: newConnectivityConfigMap(map[string]string{}),
			nodes:     []corev1.Node{newReadyNode("node1")},
		},
		{
			name:    "When ConfigMap has empty lastSucceeded it should set condition to False with KASAccessFailed reason",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionFalse,
				hyperv1.ControlPlaneConnectionKASAccessFailedReason,
				"Data plane to control plane connection is not available: no successful connectivity check recorded",
			),
			configMap: newConnectivityConfigMap(map[string]string{"lastSucceeded": ""}),
			nodes:     []corev1.Node{newReadyNode("node1")},
		},
		{
			name:    "When lastSucceeded is recent it should set condition to True",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionTrue,
				hyperv1.AsExpectedReason,
				hyperv1.AllIsWellMessage,
			),
			configMap: newConnectivityConfigMap(map[string]string{
				"lastSucceeded": time.Now().UTC().Format(time.RFC3339),
			}),
			nodes: []corev1.Node{newReadyNode("node1")},
		},
		{
			name:    "When lastSucceeded is stale it should set condition to False with ConnectionCheckStale reason",
			hcp:     fakeHCP(),
			wantErr: false,
			expectedCondition: newCondition(
				string(hyperv1.ControlPlaneConnectionAvailable),
				metav1.ConditionFalse,
				hyperv1.ControlPlaneConnectionCheckStaleReason,
				"Data plane to control plane connection is not available: last successful check was at 2020-01-01T00:00:00Z, which is older than 5 minutes",
			),
			configMap: newConnectivityConfigMap(map[string]string{
				"lastSucceeded": "2020-01-01T00:00:00Z",
			}),
			nodes: []corev1.Node{newReadyNode("node1")},
		},
	}

	log := zapr.NewLogger(zaptest.NewLogger(t))
	ctx := logr.NewContext(context.Background(), log)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r reconciler

			// Build client with ConfigMap and nodes
			var objects []client.Object
			if tt.configMap != nil {
				objects = append(objects, tt.configMap)
			}
			nodeList := &corev1.NodeList{Items: tt.nodes}

			r.client = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithLists(nodeList).Build()
			r.cpClient = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.hcp).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

			gotErr := r.reconcileControlPlaneConnectionAvailable(ctx, tt.hcp)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("reconcileControlPlaneConnectionAvailable() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("reconcileControlPlaneConnectionAvailable() succeeded unexpectedly")
			}
			if tt.expectedCondition != nil {
				found := false
				for _, c := range tt.hcp.Status.Conditions {
					if tt.expectedCondition.Type == c.Type &&
						tt.expectedCondition.Message == c.Message &&
						tt.expectedCondition.Status == c.Status &&
						tt.expectedCondition.Reason == c.Reason {
						found = true
					}
				}
				if !found {
					t.Fatalf("couldn't find expected condition. Expected: %+v, Got: %+v", tt.expectedCondition, tt.hcp.Status.Conditions)
				}
			}
		})
	}
}

func verifyKASCheckerLabelsAndSelectors(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	if dep.Spec.Selector == nil || dep.Spec.Selector.MatchLabels["app"] != manifests.KASConnectionCheckerName {
		t.Error("Selector labels not set correctly")
	}
	if dep.Spec.Template.ObjectMeta.Labels["app"] != manifests.KASConnectionCheckerName {
		t.Error("Pod template labels not set correctly")
	}
}

func verifyKASCheckerContainerBasics(t *testing.T, dep *appsv1.Deployment, expectedImage string) corev1.Container {
	t.Helper()
	if len(dep.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(dep.Spec.Template.Spec.Containers))
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.Name != "connection-checker" {
		t.Errorf("Expected container name 'connection-checker', got %s", container.Name)
	}
	if container.Image != expectedImage {
		t.Errorf("Expected cli image %s, got %s", expectedImage, container.Image)
	}
	return container
}

func verifyKASCheckerScript(t *testing.T, container corev1.Container) {
	t.Helper()
	if len(container.Command) != 3 || container.Command[0] != "/bin/sh" || container.Command[1] != "-c" {
		t.Fatalf("Expected command [/bin/sh -c <script>], got %v", container.Command)
	}
	script := container.Command[2]
	if !strings.Contains(script, "curl") {
		t.Error("Check script should use curl")
	}
	if !strings.Contains(script, "kubernetes.default.svc") {
		t.Error("Check script should use kubernetes.default.svc for full data path testing")
	}
	if !strings.Contains(script, "/version") {
		t.Error("Check script should check /version endpoint")
	}
	if !strings.Contains(script, "sleep 60") {
		t.Error("Check script should sleep 60 seconds between checks")
	}
	if !strings.Contains(script, "PATCH") {
		t.Error("Check script should PATCH the ConfigMap on success")
	}
	if !strings.Contains(script, manifests.KASConnectionCheckerConfigMapName) {
		t.Errorf("Check script should reference ConfigMap name %s", manifests.KASConnectionCheckerConfigMapName)
	}
}

func verifyKASCheckerResources(t *testing.T, container corev1.Container) {
	t.Helper()
	expectedCPU := resource.MustParse("5m")
	expectedMemory := resource.MustParse("10Mi")
	if !container.Resources.Requests.Cpu().Equal(expectedCPU) {
		t.Errorf("Expected CPU request 5m, got %s", container.Resources.Requests.Cpu())
	}
	if !container.Resources.Requests.Memory().Equal(expectedMemory) {
		t.Errorf("Expected memory request 10Mi, got %s", container.Resources.Requests.Memory())
	}
}

func verifyKASCheckerPodSpec(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	if dep.Spec.Template.Spec.PriorityClassName != "system-node-critical" {
		t.Errorf("Expected PriorityClassName system-node-critical, got %s", dep.Spec.Template.Spec.PriorityClassName)
	}
	if dep.Spec.Template.Spec.AutomountServiceAccountToken == nil || !*dep.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Error("AutomountServiceAccountToken should be set to true")
	}
	if dep.Spec.Template.Spec.HostNetwork {
		t.Error("HostNetwork should be false (not set)")
	}
	if dep.Spec.Template.Spec.ServiceAccountName != manifests.KASConnectionCheckerName {
		t.Errorf("Expected ServiceAccountName %s, got %s", manifests.KASConnectionCheckerName, dep.Spec.Template.Spec.ServiceAccountName)
	}
}

func verifyKASCheckerTolerations(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	expectedTolerations := []corev1.Toleration{
		{
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:               "node.kubernetes.io/unreachable",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: ptr.To[int64](120),
		},
		{
			Key:               "node.kubernetes.io/not-ready",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: ptr.To[int64](120),
		},
	}
	if len(dep.Spec.Template.Spec.Tolerations) != len(expectedTolerations) {
		t.Fatalf("Expected %d tolerations, got %d", len(expectedTolerations), len(dep.Spec.Template.Spec.Tolerations))
	}
	for i, expected := range expectedTolerations {
		actual := dep.Spec.Template.Spec.Tolerations[i]
		if actual.Operator != expected.Operator || actual.Effect != expected.Effect || actual.Key != expected.Key || !reflect.DeepEqual(actual.TolerationSeconds, expected.TolerationSeconds) {
			t.Errorf("Toleration[%d] mismatch: got {Key:%q, Operator:%q, Effect:%q, TolerationSeconds:%v}, want {Key:%q, Operator:%q, Effect:%q, TolerationSeconds:%v}",
				i, actual.Key, actual.Operator, actual.Effect, actual.TolerationSeconds, expected.Key, expected.Operator, expected.Effect, expected.TolerationSeconds)
		}
	}
}

func verifyKASCheckerAnnotations(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	if dep.Spec.Template.ObjectMeta.Annotations["openshift.io/required-scc"] != "restricted-v2" {
		t.Errorf("Expected openshift.io/required-scc annotation 'restricted-v2', got %s", dep.Spec.Template.ObjectMeta.Annotations["openshift.io/required-scc"])
	}
}

func verifyKASCheckerReplicas(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Error("Replicas should be set to 3")
	}
}

func getKASCheckerDeployment(t *testing.T, c client.Client) *appsv1.Deployment {
	t.Helper()
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: manifests.KASConnectionCheckerName, Namespace: manifests.KASConnectionCheckerNamespace}, dep); err != nil {
		t.Fatalf("Deployment should exist: %v", err)
	}
	return dep
}

func Test_reconciler_reconcileKASConnectionCheckerDeployment(t *testing.T) {
	t.Parallel()
	const testCLIImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-test"

	tests := []struct {
		name               string
		hcp                *hyperv1.HostedControlPlane
		existingDeployment *appsv1.Deployment
		wantErr            bool
		validate           func(t *testing.T, c client.Client)
	}{
		{
			name:               "When Deployment does not exist it should create it with correct spec",
			hcp:                fakeHCP(),
			existingDeployment: nil,
			wantErr:            false,
			validate: func(t *testing.T, c client.Client) {
				dep := getKASCheckerDeployment(t, c)
				verifyKASCheckerReplicas(t, dep)
				verifyKASCheckerLabelsAndSelectors(t, dep)
				container := verifyKASCheckerContainerBasics(t, dep, testCLIImage)
				verifyKASCheckerScript(t, container)
				if container.ReadinessProbe != nil {
					t.Error("ReadinessProbe should not be set")
				}
				verifyKASCheckerPodSpec(t, dep)
				verifyKASCheckerResources(t, container)
				verifyKASCheckerTolerations(t, dep)
				verifyKASCheckerAnnotations(t, dep)

				cm := &corev1.ConfigMap{}
				if err := c.Get(context.Background(), client.ObjectKey{Name: manifests.KASConnectionCheckerConfigMapName, Namespace: manifests.KASConnectionCheckerNamespace}, cm); err != nil {
					t.Errorf("ConfigMap should be created: %v", err)
				}
			},
		},
		{
			name: "When platform is IBM Cloud it should use IBM Cloud specific endpoint in curl script",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			existingDeployment: nil,
			wantErr:            false,
			validate: func(t *testing.T, c client.Client) {
				dep := getKASCheckerDeployment(t, c)
				container := dep.Spec.Template.Spec.Containers[0]
				script := container.Command[2]
				if !strings.Contains(script, "/livez?exclude=etcd&exclude=log") {
					t.Errorf("Expected IBM Cloud endpoint in curl script, got script: %s", script)
				}
			},
		},
		{
			name: "When Deployment already exists it should update it",
			hcp:  fakeHCP(),
			existingDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      manifests.KASConnectionCheckerName,
					Namespace: manifests.KASConnectionCheckerNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "old-label",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "old-label",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "old-container",
									Image: "old-image",
								},
							},
						},
					},
				},
			},
			wantErr: false,
			validate: func(t *testing.T, c client.Client) {
				dep := getKASCheckerDeployment(t, c)
				if dep.Spec.Selector.MatchLabels["app"] != manifests.KASConnectionCheckerName {
					t.Error("Deployment should be updated with correct selector")
				}
				container := verifyKASCheckerContainerBasics(t, dep, testCLIImage)
				if container.ReadinessProbe != nil {
					t.Error("ReadinessProbe should not be set")
				}
				verifyKASCheckerReplicas(t, dep)
				if dep.Spec.Template.Spec.ServiceAccountName != manifests.KASConnectionCheckerName {
					t.Errorf("Expected ServiceAccountName %s, got %s", manifests.KASConnectionCheckerName, dep.Spec.Template.Spec.ServiceAccountName)
				}
				verifyKASCheckerAnnotations(t, dep)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r reconciler

			// Setup fake client with existing Deployment if provided
			var objects []client.Object
			if tt.existingDeployment != nil {
				objects = append(objects, tt.existingDeployment)
			}
			r.client = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			r.CreateOrUpdateProvider = &simpleCreateOrUpdater{}

			ctx := context.Background()
			err := r.reconcileKASConnectionCheckerDeployment(ctx, tt.hcp, testCLIImage)

			if (err != nil) != tt.wantErr {
				t.Errorf("reconcileKASConnectionCheckerDeployment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Verify ServiceAccount was created
			sa := &corev1.ServiceAccount{}
			saKey := client.ObjectKey{
				Name:      manifests.KASConnectionCheckerName,
				Namespace: manifests.KASConnectionCheckerNamespace,
			}
			if err := r.client.Get(ctx, saKey, sa); err != nil {
				t.Errorf("Failed to get ServiceAccount: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, r.client)
			}
		})
	}
}

func TestReconcileMetricsForwarder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		annotations     map[string]string
		monitoring      hyperv1.MonitoringSpec
		existingObjects []client.Object
		expectCleanup   bool
	}{
		{
			name:        "When metrics forwarding mode is not set, it should delete existing resources",
			annotations: map[string]string{},
			existingObjects: []client.Object{
				manifests.MetricsForwarderDeployment(),
				manifests.MetricsForwarderConfigMap(),
				manifests.MetricsForwarderServingCA(),
				manifests.MetricsForwarderPodMonitor(),
			},
			expectCleanup: true,
		},
		{
			name:        "When DisableMonitoringServices is set, it should delete existing resources",
			annotations: map[string]string{hyperv1.DisableMonitoringServices: "true"},
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeForward,
				},
			},
			existingObjects: []client.Object{
				manifests.MetricsForwarderDeployment(),
				manifests.MetricsForwarderConfigMap(),
				manifests.MetricsForwarderServingCA(),
				manifests.MetricsForwarderPodMonitor(),
			},
			expectCleanup: true,
		},
		{
			name:            "When metrics forwarding mode is not set and no resources exist, it should succeed",
			annotations:     map[string]string{},
			existingObjects: nil,
			expectCleanup:   true,
		},
		{
			name: "When metrics forwarding mode is None, it should delete existing resources",
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeNone,
				},
			},
			existingObjects: []client.Object{
				manifests.MetricsForwarderDeployment(),
				manifests.MetricsForwarderConfigMap(),
				manifests.MetricsForwarderServingCA(),
				manifests.MetricsForwarderPodMonitor(),
			},
			expectCleanup: true,
		},
		{
			name: "When metrics forwarding mode is Forward, it should not delete resources",
			monitoring: hyperv1.MonitoringSpec{
				MetricsForwarding: hyperv1.MetricsForwardingSpec{
					Mode: hyperv1.MetricsForwardingModeForward,
				},
			},
			existingObjects: []client.Object{
				manifests.MetricsForwarderDeployment(),
				manifests.MetricsForwarderConfigMap(),
				manifests.MetricsForwarderServingCA(),
				manifests.MetricsForwarderPodMonitor(),
			},
			expectCleanup: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.existingObjects...).Build()
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &reconciler{
				client:                 guestClient,
				cpClient:               cpClient,
				hcpNamespace:           "test-ns",
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test",
					Namespace:   "test-ns",
					Annotations: tt.annotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Monitoring: tt.monitoring,
				},
			}

			err := r.reconcileMetricsForwarder(t.Context(), hcp, nil)
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectCleanup {
				deployment := manifests.MetricsForwarderDeployment()
				g.Expect(apierrors.IsNotFound(guestClient.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment))).To(BeTrue(), "deployment should be deleted")

				cm := manifests.MetricsForwarderConfigMap()
				g.Expect(apierrors.IsNotFound(guestClient.Get(t.Context(), client.ObjectKeyFromObject(cm), cm))).To(BeTrue(), "configmap should be deleted")

				servingCA := manifests.MetricsForwarderServingCA()
				g.Expect(apierrors.IsNotFound(guestClient.Get(t.Context(), client.ObjectKeyFromObject(servingCA), servingCA))).To(BeTrue(), "serving CA should be deleted")

				podMonitor := manifests.MetricsForwarderPodMonitor()
				g.Expect(apierrors.IsNotFound(guestClient.Get(t.Context(), client.ObjectKeyFromObject(podMonitor), podMonitor))).To(BeTrue(), "pod monitor should be deleted")
			} else {
				deployment := manifests.MetricsForwarderDeployment()
				g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment)).To(Succeed(), "deployment should be preserved")

				cm := manifests.MetricsForwarderConfigMap()
				g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(cm), cm)).To(Succeed(), "configmap should be preserved")

				servingCA := manifests.MetricsForwarderServingCA()
				g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(servingCA), servingCA)).To(Succeed(), "serving CA should be preserved")

				podMonitor := manifests.MetricsForwarderPodMonitor()
				g.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(podMonitor), podMonitor)).To(Succeed(), "pod monitor should be preserved")
			}
		})
	}
}

func Test_namespacedNamePredicateFunc(t *testing.T) {
	predicate := namespacedNamePredicateFunc("my-hcp-namespace", "pull-secret")

	tests := []struct {
		name   string
		object client.Object
		want   bool
	}{
		{
			name: "When namespace and name match it should return true",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "my-hcp-namespace", Name: "pull-secret"},
			},
			want: true,
		},
		{
			name: "When namespace differs it should return false",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "other-namespace", Name: "pull-secret"},
			},
			want: false,
		},
		{
			name: "When name differs it should return false",
			object: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "my-hcp-namespace", Name: "other-secret"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(predicate(tt.object)).To(Equal(tt.want))
		})
	}
}

func TestReconcileDeletion(t *testing.T) {
	log := zapr.NewLogger(zaptest.NewLogger(t))

	tests := []struct {
		name               string
		hcp                *hyperv1.HostedControlPlane
		existingObjects    []client.Object
		interceptorFuncs   *interceptor.Funcs
		expectVAPDeleted   bool
		expectVAPBDeleted  bool
		expectCloudCleanup bool
		expectError        bool
		errSubstr          string
	}{
		{
			name: "When platform is Azure, it should delete the registry management state VAP and binding",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			existingObjects: []client.Object{
				manifests.ValidatingAdmissionPolicy(registry.AdmissionPolicyNameManagementState),
				manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState)),
			},
			expectVAPDeleted:  true,
			expectVAPBDeleted: true,
		},
		{
			name: "When platform is AWS, it should not delete registry admission resources",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			existingObjects: []client.Object{
				manifests.ValidatingAdmissionPolicy(registry.AdmissionPolicyNameManagementState),
				manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState)),
			},
			expectVAPDeleted:  false,
			expectVAPBDeleted: false,
		},
		{
			name: "When platform is Azure and no VAP exists, it should not error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			existingObjects:   nil,
			expectVAPDeleted:  true,
			expectVAPBDeleted: true,
		},
		{
			name: "When cleanup cloud resources annotation is set and CVO is scaled down, it should trigger cloud cleanup",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.CleanupCloudResourcesAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.CVOScaledDown),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expectCloudCleanup: true,
		},
		{
			name: "When cleanup annotation is not set, it should not trigger cloud cleanup",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
			},
			expectCloudCleanup: false,
		},
		{
			name: "When cleanup annotation is true but CVO is not scaled down, it should not trigger cloud cleanup",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.CleanupCloudResourcesAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.CVOScaledDown),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expectCloudCleanup: false,
		},
		{
			name: "When Delete fails for the VAP binding on Azure, it should return a wrapped error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			existingObjects: []client.Object{
				manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState)),
			},
			interceptorFuncs: &interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if obj.GetName() == fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState) {
						return fmt.Errorf("API server unavailable")
					}
					return c.Delete(ctx, obj, opts...)
				},
			},
			expectError: true,
			errSubstr:   "failed to delete ValidatingAdmissionPolicyBinding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			guestClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.existingObjects...)
			if tt.interceptorFuncs != nil {
				guestClientBuilder = guestClientBuilder.WithInterceptorFuncs(*tt.interceptorFuncs)
			}
			guestClient := guestClientBuilder.Build()
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.hcp).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				cleanupTracker:         supportutil.NewCleanupTracker(),
			}

			if tt.expectCloudCleanup {
				// Add KAS deployment for cloud cleanup to proceed
				kasDeployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: tt.hcp.Namespace,
					},
				}
				g.Expect(cpClient.Create(t.Context(), kasDeployment)).To(Succeed())
			}

			result, err := r.reconcileDeletion(t.Context(), log, tt.hcp)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errSubstr != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
				}
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectVAPDeleted {
				vap := manifests.ValidatingAdmissionPolicy(registry.AdmissionPolicyNameManagementState)
				getErr := guestClient.Get(t.Context(), client.ObjectKeyFromObject(vap), vap)
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "VAP should be deleted or not found")
			}

			if tt.expectVAPBDeleted {
				vapb := manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState))
				getErr := guestClient.Get(t.Context(), client.ObjectKeyFromObject(vapb), vapb)
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "VAPB should be deleted or not found")
			}

			if !tt.expectVAPDeleted && len(tt.existingObjects) > 0 {
				vap := manifests.ValidatingAdmissionPolicy(registry.AdmissionPolicyNameManagementState)
				getErr := guestClient.Get(t.Context(), client.ObjectKeyFromObject(vap), vap)
				g.Expect(getErr).ToNot(HaveOccurred(), "VAP should still exist for non-Azure platforms")
			}

			if !tt.expectVAPBDeleted && len(tt.existingObjects) > 0 {
				vapb := manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", registry.AdmissionPolicyNameManagementState))
				getErr := guestClient.Get(t.Context(), client.ObjectKeyFromObject(vapb), vapb)
				g.Expect(getErr).ToNot(HaveOccurred(), "VAPB should still exist for non-Azure platforms")
			}

			if tt.expectCloudCleanup {
				// When cloud cleanup is triggered, verify it ran by checking the CloudResourcesDestroyed condition was set
				// The condition is set by destroyCloudResources regardless of whether resources remain
				condition := meta.FindStatusCondition(tt.hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
				g.Expect(condition).ToNot(BeNil(), "CloudResourcesDestroyed condition should be set when cleanup is triggered")
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue), "CloudResourcesDestroyed should be true when all resources are cleaned up")
				g.Expect(condition.Reason).ToNot(BeEmpty(), "CloudResourcesDestroyed condition should have a reason")
			}

			if !tt.expectCloudCleanup {
				g.Expect(result.RequeueAfter).To(Equal(time.Duration(0)), "should not requeue when cloud cleanup is not triggered")
			}
		})
	}
}

func TestReconcilePlatformSpecificResources(t *testing.T) {
	log := zapr.NewLogger(zaptest.NewLogger(t))
	ctx := logr.NewContext(t.Context(), log)

	tests := []struct {
		name          string
		platformType  hyperv1.PlatformType
		expectErrors  bool
		verifyObjects func(*WithT, client.Client)
	}{
		{
			name:         "When platform is AWS, it should reconcile AWS identity webhook resources",
			platformType: hyperv1.AWSPlatform,
			verifyObjects: func(g *WithT, c client.Client) {
				// AWS identity webhook creates a mutating webhook config, service account, etc.
				// Verify at least one of the expected resources exists
				saList := &corev1.ServiceAccountList{}
				err := c.List(ctx, saList)
				g.Expect(err).ToNot(HaveOccurred())
			},
		},
		{
			name:         "When platform is None, it should not create any platform-specific resources",
			platformType: hyperv1.NonePlatform,
			verifyObjects: func(g *WithT, c client.Client) {
				// No platform-specific resources expected
			},
		},
		{
			name:         "When platform is KubeVirt, it should not create AWS or Azure resources",
			platformType: hyperv1.KubevirtPlatform,
			verifyObjects: func(g *WithT, c client.Client) {
				// KubeVirt is not in the switch; no platform resources should be created
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tt.platformType,
					},
				},
			}

			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &reconciler{
				client:                 guestClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				platformType:           tt.platformType,
			}

			// Use a nil releaseImage for platforms that don't need one (None, KubeVirt)
			// For AWS, the reconcileAWSIdentityWebhook doesn't use releaseImage
			errs := r.reconcilePlatformSpecificResources(t.Context(), log, hcp, nil)

			if tt.expectErrors {
				g.Expect(errs).ToNot(BeEmpty())
			} else {
				g.Expect(errs).To(BeEmpty())
			}

			if tt.verifyObjects != nil {
				tt.verifyObjects(g, guestClient)
			}
		})
	}
}

func TestReconcileClusterRecovery(t *testing.T) {
	log := zapr.NewLogger(zaptest.NewLogger(t))

	tests := []struct {
		name             string
		hcp              *hyperv1.HostedControlPlane
		existingErrs     []error
		uncachedObjects  []client.Object
		expectError      bool
		expectRequeue    bool
		expectCondition  bool
		conditionStatus  metav1.ConditionStatus
		conditionMessage string
	}{
		{
			name: "When no restore annotation exists, it should return immediately without error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
			},
			expectError:     false,
			expectRequeue:   false,
			expectCondition: false,
		},
		{
			name: "When restore annotation exists and monitoring stack is ready, it should set condition to true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.HostedClusterRestoredFromBackupAnnotation: "true",
					},
				},
			},
			uncachedObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          1,
						AvailableReplicas: 1,
					},
				},
			},
			expectError:      false,
			expectRequeue:    false,
			expectCondition:  true,
			conditionStatus:  metav1.ConditionTrue,
			conditionMessage: "Hosted cluster recovery finished",
		},
		{
			name: "When restore annotation exists and monitoring stack is not ready, it should requeue after 120 seconds",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.HostedClusterRestoredFromBackupAnnotation: "true",
					},
				},
			},
			uncachedObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          2,
						AvailableReplicas: 0,
					},
				},
			},
			expectError:      false,
			expectRequeue:    true,
			expectCondition:  true,
			conditionStatus:  metav1.ConditionFalse,
			conditionMessage: "Hosted cluster recovery not finished yet",
		},
		{
			name: "When restore annotation exists and monitoring stack does not exist, it should return aggregate error with existing errors",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.HostedClusterRestoredFromBackupAnnotation: "true",
					},
				},
			},
			existingErrs:    []error{fmt.Errorf("previous error")},
			uncachedObjects: nil,
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.hcp).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.uncachedObjects...).Build()

			r := &reconciler{
				client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				uncachedClient:         uncachedClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			result, err := r.reconcileClusterRecovery(t.Context(), log, tt.hcp, tt.existingErrs)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectRequeue {
				g.Expect(result.RequeueAfter).To(Equal(120*time.Second), "should requeue after 120 seconds when recovery is not finished")
			} else {
				g.Expect(result.RequeueAfter).To(Equal(time.Duration(0)), "should not requeue when recovery is finished or not applicable")
			}

			if tt.expectCondition {
				updatedHCP := &hyperv1.HostedControlPlane{}
				g.Expect(cpClient.Get(t.Context(), client.ObjectKeyFromObject(tt.hcp), updatedHCP)).To(Succeed())

				cond := meta.FindStatusCondition(updatedHCP.Status.Conditions, string(hyperv1.HostedClusterRestoredFromBackup))
				g.Expect(cond).ToNot(BeNil(), "recovery condition should be set")
				g.Expect(cond.Status).To(Equal(tt.conditionStatus))
				g.Expect(cond.Message).To(Equal(tt.conditionMessage))
				g.Expect(cond.Reason).To(Equal(hyperv1.RecoveryFinishedReason))
			}
		})
	}
}

func TestCleanupLegacyResources(t *testing.T) {
	log := zapr.NewLogger(zaptest.NewLogger(t))

	tests := []struct {
		name                    string
		clusterVersion          *configv1.ClusterVersion
		releaseVersion          string
		existingUncachedObjects []client.Object
		expectDNSDeploymentGone bool
		expectErrorCount        int
	}{
		{
			name: "When cluster version is not updated, it should skip cleanup",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.15.0"},
				},
			},
			releaseVersion: "4.16.0",
			existingUncachedObjects: []client.Object{
				manifests.DNSOperatorDeployment(),
			},
			expectDNSDeploymentGone: false,
		},
		{
			name: "When cluster version matches release, it should delete DNS operator deployment",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.16.0"},
				},
			},
			releaseVersion: "4.16.0",
			existingUncachedObjects: []client.Object{
				manifests.DNSOperatorDeployment(),
			},
			expectDNSDeploymentGone: true,
		},
		{
			name: "When cluster version matches but DNS deployment does not exist, it should not error",
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.16.0"},
				},
			},
			releaseVersion:          "4.16.0",
			existingUncachedObjects: nil,
			expectDNSDeploymentGone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Reset the sync.Once variables so each test case runs independently
			deleteDNSOperatorDeploymentOnce = sync.Once{}
			deleteCVORemovedResourcesOnce = sync.Once{}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
			}

			var guestObjects []client.Object
			if tt.clusterVersion != nil {
				guestObjects = append(guestObjects, tt.clusterVersion)
			}
			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(guestObjects...).Build()
			uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.existingUncachedObjects...).Build()

			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         uncachedClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			fakeReleaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: tt.releaseVersion},
				},
			}

			var errs []error
			r.cleanupLegacyResources(t.Context(), log, hcp, fakeReleaseImage, &errs)

			g.Expect(errs).To(HaveLen(tt.expectErrorCount), "unexpected error count")

			dnsDeployment := manifests.DNSOperatorDeployment()
			getErr := uncachedClient.Get(t.Context(), client.ObjectKeyFromObject(dnsDeployment), dnsDeployment)
			if tt.expectDNSDeploymentGone {
				g.Expect(apierrors.IsNotFound(getErr)).To(BeTrue(), "DNS operator deployment should be deleted")
			} else {
				g.Expect(getErr).ToNot(HaveOccurred(), "DNS operator deployment should still exist")
			}
		})
	}
}

func TestIsAllowedWebhookUrl(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		disallowedUrls []string
		url            string
		expected       bool
	}{
		{
			name:           "When URL contains a disallowed substring it should return false",
			disallowedUrls: []string{"https://etcd-client"},
			url:            "https://etcd-client:2379",
			expected:       false,
		},
		{
			name:           "When URL matches a fully qualified disallowed URL it should return false",
			disallowedUrls: []string{"https://etcd-client.ns.svc"},
			url:            "https://etcd-client.ns.svc:2379/path",
			expected:       false,
		},
		{
			name:           "When URL does not match any disallowed URL it should return true",
			disallowedUrls: []string{"https://etcd-client"},
			url:            "https://external.example.com",
			expected:       true,
		},
		{
			name:           "When disallowed list is empty it should return true",
			disallowedUrls: []string{},
			url:            "https://anything",
			expected:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := isAllowedWebhookUrl(tt.disallowedUrls, tt.url)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestEnsureGuestAdmissionWebhooksAreValid(t *testing.T) {
	t.Parallel()
	const hcpNamespace = "test-hcp-namespace"

	tests := []struct {
		name               string
		cpServices         []corev1.Service
		guestObjects       []client.Object
		expectWebhookGone  string
		expectWebhookAlive string
	}{
		{
			name: "When validating webhook targets a CP service it should delete the webhook",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "test-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "test.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://etcd-client:2379")},
						},
					},
				},
			},
			expectWebhookGone: "test-validating-webhook",
		},
		{
			name: "When validating webhook targets an allowed CP service it should preserve the webhook",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "allowed-service",
						Namespace: hcpNamespace,
						Labels:    map[string]string{hyperv1.AllowGuestWebhooksServiceLabel: "true"},
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "preserved-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "preserved.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://allowed-service:8443")},
						},
					},
				},
			},
			expectWebhookAlive: "preserved-validating-webhook",
		},
		{
			name: "When mutating webhook targets a CP service it should delete the webhook",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.MutatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "test-mutating-webhook"},
					Webhooks: []admissionregistrationv1.MutatingWebhook{
						{
							Name:         "mutating.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://kube-apiserver:6443")},
						},
					},
				},
			},
			expectWebhookGone: "test-mutating-webhook",
		},
		{
			name: "When webhook targets an external URL it should preserve the webhook",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "external-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "external.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://external.example.com")},
						},
					},
				},
			},
			expectWebhookAlive: "external-validating-webhook",
		},
		{
			name: "When webhook uses Service reference instead of URL it should preserve the webhook",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "service-ref-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name: "service.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{
								Service: &admissionregistrationv1.ServiceReference{
									Name:      "my-webhook-service",
									Namespace: "default",
								},
							},
						},
					},
				},
			},
			expectWebhookAlive: "service-ref-webhook",
		},
		{
			name: "When validating webhook has mixed allowed and disallowed URLs it should delete the entire configuration",
			cpServices: []corev1.Service{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-client",
						Namespace: hcpNamespace,
					},
				},
			},
			guestObjects: []client.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{
					ObjectMeta: metav1.ObjectMeta{Name: "mixed-validating-webhook"},
					Webhooks: []admissionregistrationv1.ValidatingWebhook{
						{
							Name:         "allowed.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://external.example.com")},
						},
						{
							Name:         "disallowed.webhook.io",
							ClientConfig: admissionregistrationv1.WebhookClientConfig{URL: ptr.To("https://etcd-client:2379")},
						},
					},
				},
			},
			expectWebhookGone: "mixed-validating-webhook",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()

			cpObjects := make([]client.Object, 0, len(tt.cpServices))
			for i := range tt.cpServices {
				cpObjects = append(cpObjects, &tt.cpServices[i])
			}

			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).Build()
			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tt.guestObjects...).Build()

			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				cpClient:               cpClient,
				hcpNamespace:           hcpNamespace,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			err := r.ensureGuestAdmissionWebhooksAreValid(ctx)
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectWebhookGone != "" {
				for _, obj := range tt.guestObjects {
					key := client.ObjectKey{Name: tt.expectWebhookGone}
					switch obj.(type) {
					case *admissionregistrationv1.ValidatingWebhookConfiguration:
						err := guestClient.Get(ctx, key, &admissionregistrationv1.ValidatingWebhookConfiguration{})
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
							"ValidatingWebhookConfiguration %q should have been deleted", tt.expectWebhookGone)
					case *admissionregistrationv1.MutatingWebhookConfiguration:
						err := guestClient.Get(ctx, key, &admissionregistrationv1.MutatingWebhookConfiguration{})
						g.Expect(apierrors.IsNotFound(err)).To(BeTrue(),
							"MutatingWebhookConfiguration %q should have been deleted", tt.expectWebhookGone)
					default:
						t.Fatalf("unexpected object type %T in guestObjects for expectWebhookGone check", obj)
					}
				}
			}

			if tt.expectWebhookAlive != "" {
				for _, obj := range tt.guestObjects {
					key := client.ObjectKey{Name: tt.expectWebhookAlive}
					switch obj.(type) {
					case *admissionregistrationv1.ValidatingWebhookConfiguration:
						g.Expect(guestClient.Get(ctx, key, &admissionregistrationv1.ValidatingWebhookConfiguration{})).To(Succeed(),
							"ValidatingWebhookConfiguration %q should still exist", tt.expectWebhookAlive)
					case *admissionregistrationv1.MutatingWebhookConfiguration:
						g.Expect(guestClient.Get(ctx, key, &admissionregistrationv1.MutatingWebhookConfiguration{})).To(Succeed(),
							"MutatingWebhookConfiguration %q should still exist", tt.expectWebhookAlive)
					default:
						t.Fatalf("unexpected object type %T in guestObjects for expectWebhookAlive check", obj)
					}
				}
			}
		})
	}
}

func TestIsServiceAccountPullSecretsControllerDisabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		controllers []string
		expected    bool
	}{
		{
			name:        "When controllers is nil, it should return false",
			controllers: nil,
			expected:    false,
		},
		{
			name:        "When controllers is empty, it should return false",
			controllers: []string{},
			expected:    false,
		},
		{
			name:        "When controller is disabled, it should return true",
			controllers: []string{"*", "-openshift.io/serviceaccount-pull-secrets"},
			expected:    true,
		},
		{
			name:        "When controllers has other entries but not the disabled one, it should return false",
			controllers: []string{"*", "-some-other-controller"},
			expected:    false,
		},
		{
			name:        "When only the wildcard is present, it should return false",
			controllers: []string{"*"},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isServiceAccountPullSecretsControllerDisabled(tt.controllers)).To(Equal(tt.expected))
		})
	}
}

func TestReconcileRegistryAndIngress_ServiceAccountPullSecretsController(t *testing.T) {
	t.Parallel()

	hcpNamespace := "test-hcp-ns"

	serializeOCMConfig := func(t *testing.T, controllers []string) string {
		t.Helper()
		config := &openshiftcpv1.OpenShiftControllerManagerConfig{
			Controllers: controllers,
		}
		data, err := k8sutil.SerializeResource(config, api.Scheme)
		if err != nil {
			t.Fatalf("failed to serialize OCM config: %v", err)
		}
		return data
	}

	tests := []struct {
		name                   string
		platformType           hyperv1.PlatformType
		managementState        operatorv1.ManagementState
		existingOCMControllers []string
		hasExistingOCMConfig   bool
		expectedControllers    []string
	}{
		{
			name:                   "When managementState is Removed, it should disable serviceaccount-pull-secrets controller",
			platformType:           hyperv1.AWSPlatform,
			managementState:        operatorv1.Removed,
			existingOCMControllers: nil,
			hasExistingOCMConfig:   true,
			expectedControllers:    []string{"*", disabledServiceAccountPullSecretsController},
		},
		{
			name:                   "When managementState changes from Removed to Managed, it should re-enable serviceaccount-pull-secrets controller",
			platformType:           hyperv1.AWSPlatform,
			managementState:        operatorv1.Managed,
			existingOCMControllers: []string{"*", disabledServiceAccountPullSecretsController},
			hasExistingOCMConfig:   true,
			expectedControllers:    []string{"*"},
		},
		{
			name:                   "When managementState is Managed and controller is already enabled, it should not change controllers",
			platformType:           hyperv1.AWSPlatform,
			managementState:        operatorv1.Managed,
			existingOCMControllers: []string{"*"},
			hasExistingOCMConfig:   true,
			expectedControllers:    []string{"*"},
		},
		{
			name:                   "When platform is IBMCloud, it should not modify OCM config regardless of managementState",
			platformType:           hyperv1.IBMCloudPlatform,
			managementState:        operatorv1.Removed,
			existingOCMControllers: nil,
			hasExistingOCMConfig:   true,
			expectedControllers:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			registryConfig := manifests.Registry()
			registryConfig.Spec.ManagementState = tt.managementState

			guestClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(registryConfig).
				Build()

			ocmConfigMap := cpomanifests.OpenShiftControllerManagerConfig(hcpNamespace)
			if tt.hasExistingOCMConfig {
				ocmConfigMap.Data = map[string]string{}
				if tt.existingOCMControllers != nil {
					ocmConfigMap.Data[ocm.ConfigKey] = serializeOCMConfig(t, tt.existingOCMControllers)
				} else {
					ocmConfigMap.Data[ocm.ConfigKey] = serializeOCMConfig(t, nil)
				}
			}

			cpClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(ocmConfigMap).
				Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tt.platformType,
					},
				},
			}

			r := &reconciler{
				client:                 guestClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				platformType:           tt.platformType,
				hcpNamespace:           hcpNamespace,
			}

			log := zapr.NewLogger(zaptest.NewLogger(t))
			errs := r.reconcileRegistryAndIngress(t.Context(), hcp, log)
			for _, e := range errs {
				g.Expect(e.Error()).ToNot(ContainSubstring("openshift-controller-manager config"), "unexpected OCM config error: %v", e)
			}

			resultConfigMap := cpomanifests.OpenShiftControllerManagerConfig(hcpNamespace)
			err := cpClient.Get(t.Context(), client.ObjectKeyFromObject(resultConfigMap), resultConfigMap)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get OCM ConfigMap")

			if tt.expectedControllers == nil {
				config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
				if configStr, exists := resultConfigMap.Data[ocm.ConfigKey]; exists && len(configStr) > 0 {
					err := k8sutil.DeserializeResource(configStr, config, api.Scheme)
					g.Expect(err).ToNot(HaveOccurred(), "failed to deserialize OCM config")
				}
				g.Expect(config.Controllers).To(BeNil(), "controllers should remain nil for excluded platform")
			} else {
				config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
				configStr, exists := resultConfigMap.Data[ocm.ConfigKey]
				g.Expect(exists).To(BeTrue(), "OCM config should exist")
				err := k8sutil.DeserializeResource(configStr, config, api.Scheme)
				g.Expect(err).ToNot(HaveOccurred(), "failed to deserialize OCM config")
				g.Expect(config.Controllers).To(Equal(tt.expectedControllers))
			}
		})
	}
}

func TestReconcileConfigOperatorReconciliationCondition(t *testing.T) {
	testCases := []struct {
		name                   string
		reconcileErr           error
		existingCondition      *metav1.Condition
		expectedConditionState metav1.ConditionStatus
		expectedReason         string
		expectedMessage        string
	}{
		{
			name:                   "When reconciliation succeeds it should set condition to True",
			reconcileErr:           nil,
			expectedConditionState: metav1.ConditionTrue,
			expectedReason:         hyperv1.AsExpectedReason,
			expectedMessage:        hyperv1.AllIsWellMessage,
		},
		{
			name:                   "When reconciliation fails it should set condition to False with error message",
			reconcileErr:           fmt.Errorf("failed to reconcile crds: connection refused"),
			expectedConditionState: metav1.ConditionFalse,
			expectedReason:         hyperv1.ReconcileErrorReason,
			expectedMessage:        "failed to reconcile crds: connection refused",
		},
		{
			name:         "When reconciliation recovers from error it should transition condition to True",
			reconcileErr: nil,
			existingCondition: &metav1.Condition{
				Type:    string(hyperv1.ConfigOperatorReconciliationSucceeded),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.ReconcileErrorReason,
				Message: "previous error",
			},
			expectedConditionState: metav1.ConditionTrue,
			expectedReason:         hyperv1.AsExpectedReason,
			expectedMessage:        hyperv1.AllIsWellMessage,
		},
		{
			name:         "When reconciliation fails after success it should transition condition to False",
			reconcileErr: fmt.Errorf("failed to reconcile namespaces: context deadline exceeded"),
			existingCondition: &metav1.Condition{
				Type:    string(hyperv1.ConfigOperatorReconciliationSucceeded),
				Status:  metav1.ConditionTrue,
				Reason:  hyperv1.AsExpectedReason,
				Message: hyperv1.AllIsWellMessage,
			},
			expectedConditionState: metav1.ConditionFalse,
			expectedReason:         hyperv1.ReconcileErrorReason,
			expectedMessage:        "failed to reconcile namespaces: context deadline exceeded",
		},
		{
			name:                   "When error message exceeds max length it should be truncated",
			reconcileErr:           fmt.Errorf("%s", strings.Repeat("a", 2000)),
			expectedConditionState: metav1.ConditionFalse,
			expectedReason:         hyperv1.ReconcileErrorReason,
			expectedMessage:        strings.Repeat("a", maxConditionMessageLength-3) + "...",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := fakeHCP()
			hcp.Generation = 7
			if tc.existingCondition != nil {
				meta.SetStatusCondition(&hcp.Status.Conditions, *tc.existingCondition)
			}

			cpClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(hcp).
				WithStatusSubresource(&hyperv1.HostedControlPlane{}).
				Build()

			r := &reconciler{
				cpClient:     cpClient,
				hcpName:      hcp.Name,
				hcpNamespace: hcp.Namespace,
			}

			ctx := logr.NewContext(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
			err := r.reconcileConfigOperatorReconciliationCondition(ctx, hcp, tc.reconcileErr)
			g.Expect(err).ToNot(HaveOccurred())

			updatedHCP := &hyperv1.HostedControlPlane{}
			err = cpClient.Get(ctx, client.ObjectKeyFromObject(hcp), updatedHCP)
			g.Expect(err).ToNot(HaveOccurred())

			condition := meta.FindStatusCondition(updatedHCP.Status.Conditions, string(hyperv1.ConfigOperatorReconciliationSucceeded))
			g.Expect(condition).ToNot(BeNil(), "ConfigOperatorReconciliationSucceeded condition should be present")
			g.Expect(condition.Status).To(Equal(tc.expectedConditionState))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			g.Expect(condition.Message).To(Equal(tc.expectedMessage))
			g.Expect(condition.ObservedGeneration).To(Equal(hcp.Generation))
		})
	}
}

func TestReconcileIngressControllerKubevirtHTTPRoute(t *testing.T) {
	tests := []struct {
		name            string
		baseDomainPT    bool
		httpNodePort    int32
		httpsNodePort   int32
		expectHTTPRoute bool
	}{
		{
			name:            "When KubeVirt HCP has baseDomainPassthrough enabled, it should create the HTTP passthrough route on the infra client",
			baseDomainPT:    true,
			httpNodePort:    30080,
			httpsNodePort:   30443,
			expectHTTPRoute: true,
		},
		{
			name:            "When KubeVirt HCP does not have baseDomainPassthrough enabled, it should not create the HTTP passthrough route",
			baseDomainPT:    false,
			expectHTTPRoute: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			const (
				hcpName      = "test-hcp"
				hcpNamespace = "test-ns"
				generateID   = "abc123"
			)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hcpName,
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "infra-id",
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							BaseDomainPassthrough: ptr.To(tt.baseDomainPT),
							GenerateID:            generateID,
						},
					},
				},
			}

			nodePortService := manifests.IngressDefaultIngressNodePortService()
			nodePortService.Spec.Ports = []corev1.ServicePort{
				{Port: 443, NodePort: tt.httpsNodePort, TargetPort: intstr.FromInt(int(tt.httpsNodePort))},
				{Port: 80, NodePort: tt.httpNodePort, TargetPort: intstr.FromInt(int(tt.httpNodePort))},
			}

			ingressCert := cpomanifests.IngressCert(hcpNamespace)
			ingressCert.Data = map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")}

			guestClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(nodePortService, manifests.IngressDefaultIngressController()).
				Build()
			cpClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(ingressCert).
				Build()
			infraClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				Build()

			r := &reconciler{
				client:                 guestClient,
				cpClient:               cpClient,
				kubevirtInfraClient:    infraClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			ctx := logr.NewContext(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
			errs := r.reconcileIngressController(ctx, hcp)
			g.Expect(errs).To(BeNil(), "reconcileIngressController should succeed without errors")

			httpRouteName := fmt.Sprintf("%s-%s", manifests.IngressDefaultIngressPassthroughHTTPRouteName, generateID)
			httpRoute := &routev1.Route{}
			err := infraClient.Get(t.Context(), client.ObjectKey{Namespace: hcpNamespace, Name: httpRouteName}, httpRoute)
			if tt.expectHTTPRoute {
				g.Expect(err).ToNot(HaveOccurred(), "HTTP passthrough route should exist on the infra client")
				g.Expect(httpRoute.Spec.TLS).To(BeNil(), "HTTP route should have no TLS configuration")
				g.Expect(string(httpRoute.Spec.WildcardPolicy)).To(Equal(string(routev1.WildcardPolicySubdomain)))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "HTTP passthrough route should not exist when baseDomainPassthrough is disabled")
			}
		})
	}
}

func TestEnsureIngressControllersRemovedHTTPRoute(t *testing.T) {
	tests := []struct {
		name                   string
		baseDomainPT           bool
		preCreateRoute         bool
		expectHTTPRouteDeleted bool
	}{
		{
			name:                   "When KubeVirt HCP has baseDomainPassthrough enabled, it should delete the HTTP passthrough route during cleanup",
			baseDomainPT:           true,
			preCreateRoute:         true,
			expectHTTPRouteDeleted: true,
		},
		{
			name:                   "When baseDomainPassthrough is enabled but the HTTP route does not exist, cleanup should be idempotent",
			baseDomainPT:           true,
			preCreateRoute:         false,
			expectHTTPRouteDeleted: true,
		},
		{
			name:                   "When baseDomainPassthrough is disabled, it should not attempt to delete the HTTP passthrough route",
			baseDomainPT:           false,
			preCreateRoute:         true,
			expectHTTPRouteDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			const (
				hcpName      = "test-hcp"
				hcpNamespace = "test-ns"
				generateID   = "abc123"
			)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hcpName,
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "infra-id",
					DNS:     hyperv1.DNSSpec{BaseDomain: "example.com"},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							BaseDomainPassthrough: ptr.To(tt.baseDomainPT),
							GenerateID:            generateID,
						},
					},
					Capabilities: &hyperv1.Capabilities{},
				},
			}

			// An IngressController must exist so the function proceeds past the early-return guard.
			ic := &operatorv1.IngressController{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default",
					Namespace: "openshift-ingress-operator",
				},
			}

			guestClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(ic).
				Build()
			uncachedClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				Build()

			httpRouteName := fmt.Sprintf("%s-%s", manifests.IngressDefaultIngressPassthroughHTTPRouteName, generateID)

			infraBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tt.preCreateRoute {
				infraBuilder = infraBuilder.WithObjects(&routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: httpRouteName, Namespace: hcpNamespace}})
			}
			infraClient := infraBuilder.Build()

			ctx := logr.NewContext(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
			r := &reconciler{
				client:              guestClient,
				uncachedClient:      uncachedClient,
				kubevirtInfraClient: infraClient,
			}

			_, cleanupErr := r.ensureIngressControllersRemoved(ctx, hcp)
			g.Expect(cleanupErr).To(BeNil(), "ensureIngressControllersRemoved should not return an error")

			httpRoute := &routev1.Route{}
			err := infraClient.Get(t.Context(), client.ObjectKey{Namespace: hcpNamespace, Name: httpRouteName}, httpRoute)
			if tt.expectHTTPRouteDeleted {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "HTTP passthrough route should be deleted from infra client when baseDomainPassthrough is enabled")
			} else {
				g.Expect(err).ToNot(HaveOccurred(), "HTTP passthrough route should still exist on infra client when baseDomainPassthrough is disabled")
			}
		})
	}
}

func TestIngressDefaultIngressPassthroughHTTPRouteManifest(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
	}{
		{
			name:      "When namespace is provided, it should return a Route with the correct namespace set",
			namespace: "test-namespace",
		},
		{
			name:      "When an empty namespace is provided, it should return a Route with an empty namespace",
			namespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			route := manifests.IngressDefaultIngressPassthroughHTTPRoute(tt.namespace)
			g.Expect(route).ToNot(BeNil())
			g.Expect(route.Namespace).To(Equal(tt.namespace))
		})
	}
}
