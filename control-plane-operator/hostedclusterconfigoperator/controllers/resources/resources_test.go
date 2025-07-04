package resources

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/kas"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/globalconfig"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	supportutil "github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap/zaptest"
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
	// Not running bcrypt hashing for the kubeadmin secret massively speeds up the tests, 4s vs 0.1s (and for -race its ~10x that)
	&corev1.Secret{
		ObjectMeta: manifests.KubeadminPasswordHashSecret().ObjectMeta,
		Data: map[string][]byte{
			"kubeadmin": []byte("something"),
		},
	},
	manifests.NodeTuningClusterOperator(),
	manifests.NamespaceKubeSystem(),
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
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
			releaseProvider:        &fakereleaseprovider.FakeReleaseProvider{},
			ImageMetaDataProvider:  &imageMetaDataProvider,
		}
		_, err := r.Reconcile(ctx, controllerruntime.Request{})
		if err != nil {
			t.Fatalf("unexpected: %v", err)
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
			releaseProvider:        &fakereleaseprovider.FakeReleaseProvider{},
			ImageMetaDataProvider:  &imageMetaDataProvider,
		}
		_, _ = r.Reconcile(ctx, controllerruntime.Request{})
		if totalCreates-fakeClient.getErrorCount != fakeClient.createCount {
			t.Fatalf("Unexpected number of creates: %d/%d with errors %d", fakeClient.createCount, totalCreates, fakeClient.getErrorCount)
		}
	}
}

func TestReconcileOLM(t *testing.T) {
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
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"

	tests := map[string]struct {
		inputHCP                                 *hyperv1.HostedControlPlane
		inputObjects                             []client.Object
		expectKubeadminPasswordHashSecretToExist bool
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
			r := &reconciler{
				client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
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
				g.Expect(len(actualKubeAdminSecret.Data["kubeadmin"]) > 0).To(BeTrue())
			} else {
				actualKubeAdminSecret := manifests.KubeadminPasswordHashSecret()
				err := r.client.Get(t.Context(), client.ObjectKeyFromObject(actualKubeAdminSecret), actualKubeAdminSecret)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}
			actualOauthDeployment := manifests.OAuthDeployment(testNamespace)
			err = r.cpClient.Get(t.Context(), client.ObjectKeyFromObject(actualOauthDeployment), actualOauthDeployment)
			g.Expect(err).To(BeNil())
		})
	}
}

func TestReconcileUserCertCABundle(t *testing.T) {
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"
	tests := map[string]struct {
		inputHCP              *hyperv1.HostedControlPlane
		inputObjects          []client.Object
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
			expectUserCAConfigMap: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := &reconciler{
				client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
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
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

var _ manifestReconciler = manifestAndReconcile[*rbacv1.ClusterRole]{}

func TestDestroyCloudResources(t *testing.T) {
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
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeHCP).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         uncachedClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
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

func TestListAccessor(t *testing.T) {
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
	}{
		{
			name: "copy kubelet config from control plane NS",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
		{
			name: "some CM already exist and some are not, expect HCCO to catch up",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(supportutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(supportutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
		},
		{
			name: "CM need to be deleted",
			hostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcpNamespace, kubeletConfig1),
			},
			existHostedControlPlaneObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
				makeKubeletConfigConfigMap(supportutil.ShortenName("foo", npName2, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
			},
			expectedHostedClusterObjects: []client.Object{
				makeKubeletConfigConfigMap(supportutil.ShortenName("bar", npName1, validation.LabelValueMaxLength), hcNamespace, kubeletConfig1),
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
				g.Expect(r.client.Get(t.Context(), client.ObjectKeyFromObject(obj), obj)).To(Succeed(), "failed to get %s", client.ObjectKeyFromObject(obj))
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

// TestReconcileOcmConfigChange checks if the OCM(Openshift Controller Manager)'s configuration has changed
// for the platforms Azure and AWS when the ImageRegistry Operator's managementState is set to Removed
func TestReconcileOcmConfigChange(t *testing.T) {
	registryConfig := &imageregistryv1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: imageregistryv1.ImageRegistrySpec{
			OperatorSpec: operatorv1.OperatorSpec{
				ManagementState: "Removed",
			},
		},
	}
	initialOcmConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager-config",
			Namespace: "bar",
		},
		Data: map[string]string{
			"config": "original-data",
		},
	}

	testCases := []struct {
		name                     string
		platformType             hyperv1.PlatformType
		expectConfigMapUnchanged bool
	}{
		{
			name:                     "OCM configuration remains unchanged for Azure platform",
			platformType:             hyperv1.AzurePlatform,
			expectConfigMapUnchanged: true,
		},
		{
			// OCM configuration should remain unchanged for AWS platform also after transitioning from
			// using managementState: Removed to disable the Image Registry in ROSA HCP
			name:                     "OCM configuration changes for AWS platform",
			platformType:             hyperv1.AWSPlatform,
			expectConfigMapUnchanged: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			// Create a context with a logger for the test
			ctx := logr.NewContext(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(initialObjects, registryConfig)...).WithStatusSubresource(&configv1.Infrastructure{}).Build()
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(cpObjects, initialOcmConfigMap)...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			r := &reconciler{
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
				client:                 fakeClient,
				uncachedClient:         fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects().Build(),
				cpClient:               cpClient,
				hcpName:                "foo",
				hcpNamespace:           "bar",
				releaseProvider:        &fakereleaseprovider.FakeReleaseProvider{},
				ImageMetaDataProvider:  &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
				platformType:           tc.platformType,
			}
			_, err := r.Reconcile(ctx, controllerruntime.Request{})
			g.Expect(err).NotTo(HaveOccurred())

			// Check if the OCM configuration has changed or not
			updatedOcmConfigMap := &corev1.ConfigMap{}
			err = cpClient.Get(ctx, types.NamespacedName{Name: "openshift-controller-manager-config", Namespace: "bar"}, updatedOcmConfigMap)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(reflect.DeepEqual(updatedOcmConfigMap.Data, initialOcmConfigMap.Data)).To(Equal(tc.expectConfigMapUnchanged))
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

			// Verify that CA configmaps and OIDC client secrets don't exist in hosted cluster before reconciliation
			if test.expectIssuerCAConfigMap {
				provider := test.inputHCP.Spec.Configuration.Authentication.OIDCProviders[0]
				caConfigMap := &corev1.ConfigMap{}
				err := hcClient.Get(ctx, client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      provider.Issuer.CertificateAuthority.Name,
				}, caConfigMap)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "CA configmap should not exist before reconciliation")
			}

			for _, secretName := range test.expectOIDCClientSecrets {
				clientSecret := &corev1.Secret{}
				err := hcClient.Get(ctx, client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      secretName,
				}, clientSecret)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue(), "OIDC client secret should not exist before reconciliation")
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
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}
			}
		})
	}
}
