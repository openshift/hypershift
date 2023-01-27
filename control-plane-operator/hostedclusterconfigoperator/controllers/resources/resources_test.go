package resources

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	corev1 "k8s.io/api/core/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	// Not running bcrypt hashing for the kubeadmin secret massively speeds up the tests, 4s vs 0.1s (and for -race its ~10x that)
	&corev1.Secret{
		ObjectMeta: manifests.KubeadminPasswordHashSecret().ObjectMeta,
		Data: map[string][]byte{
			"kubeadmin": []byte("something"),
		},
	},
	manifests.NodeTuningClusterOperator(),
	manifests.NamespaceKubeSystem(),
}

func shouldNotError(key client.ObjectKey) bool {
	for _, o := range initialObjects {
		if client.ObjectKeyFromObject(o).String() == key.String() {
			return true
		}
	}
	return false
}

func (c *testClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
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
	fakeKonnectivityAgentSecret(),
	fakeRootCASecret(),
	fakeOpenShiftAPIServerService(),
	fakeOpenShiftOAuthAPIServerService(),
	fakeKubeadminPasswordSecret(),
	fakeOAuthServingCert(),
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
	var totalCreates int
	{
		fakeClient := &testClient{
			Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).Build(),
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
		}
		_, err := r.Reconcile(context.Background(), controllerruntime.Request{})
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		totalCreates = fakeClient.createCount
	}

	// test with random get errors
	for i := 0; i < 100; i++ {
		fakeClient := &testClient{
			Client:          fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).Build(),
			randomGetErrors: true,
		}
		r := &reconciler{
			client:                 fakeClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
			releaseProvider:        &fakereleaseprovider.FakeReleaseProvider{},
		}
		r.Reconcile(context.Background(), controllerruntime.Request{})
		if totalCreates-fakeClient.getErrorCount != fakeClient.createCount {
			t.Fatalf("Unexpected number of creates: %d/%d with errors %d", fakeClient.createCount, totalCreates, fakeClient.getErrorCount)
		}
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
		corev1.DockerConfigJsonKey: []byte("data"),
	}
	return s
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
	s := manifests.RootCASecret("bar")
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

func fakeOAuthServingCert() *corev1.Secret {
	s := cpomanifests.OpenShiftOAuthServerCert("bar")
	s.Data = map[string][]byte{"tls.crt": []byte("test")}
	return s
}

func fakePackageServerService() *corev1.Service {
	s := manifests.OLMPackageServerControlPlaneService("bar")
	s.Spec.ClusterIP = "1.1.1.1"
	return s
}

func TestReconcileKubeadminPasswordHashSecret(t *testing.T) {
	testNamespace := "master-cluster1"
	testHCPName := "cluster1"
	tests := map[string]struct {
		inputHCP                                 *hyperv1.HostedControlPlane
		inputObjects                             []client.Object
		expectedOauthServerAnnotations           []string
		expectKubeadminPasswordHashSecretToExist bool
	}{
		"when kubeadminPasswordSecret exists the oauth server is annotated and the hash secret is created": {
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
			expectedOauthServerAnnotations: []string{
				SecretHashAnnotation,
			},
			expectKubeadminPasswordHashSecretToExist: true,
		},
		"when kubeadminPasswordSecret doesn't exist the oauth server is not annotated and the hash secret is not created": {
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
			expectedOauthServerAnnotations:           nil,
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
			err := r.reconcileKubeadminPasswordHashSecret(context.Background(), test.inputHCP)
			g.Expect(err).To(BeNil())
			if test.expectKubeadminPasswordHashSecretToExist {
				actualKubeAdminSecret := manifests.KubeadminPasswordHashSecret()
				err := r.client.Get(context.TODO(), client.ObjectKeyFromObject(actualKubeAdminSecret), actualKubeAdminSecret)
				g.Expect(err).To(BeNil())
				g.Expect(len(actualKubeAdminSecret.Data["kubeadmin"]) > 0).To(BeTrue())
			} else {
				actualKubeAdminSecret := manifests.KubeadminPasswordHashSecret()
				err := r.client.Get(context.TODO(), client.ObjectKeyFromObject(actualKubeAdminSecret), actualKubeAdminSecret)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}
			actualOauthDeployment := manifests.OAuthDeployment(testNamespace)
			err = r.cpClient.Get(context.TODO(), client.ObjectKeyFromObject(actualOauthDeployment), actualOauthDeployment)
			g.Expect(err).To(BeNil())
			if test.expectedOauthServerAnnotations == nil {
				g.Expect(actualOauthDeployment.Spec.Template.Annotations).To(BeNil())
			} else {
				for _, annotation := range test.expectedOauthServerAnnotations {
					g.Expect(len(actualOauthDeployment.Spec.Template.Annotations[annotation]) > 0).To(BeTrue())
				}
			}
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
						Name: manifests.ControlPlaneUserCABundle(testNamespace).Name,
					},
				},
			},
			inputObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: manifests.ControlPlaneUserCABundle(testNamespace).ObjectMeta,
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
			err := r.reconcileUserCertCABundle(context.Background(), test.inputHCP)
			g.Expect(err).To(BeNil())
			guestUserCABundle := manifests.UserCABundle()
			if test.expectUserCAConfigMap {
				err := r.client.Get(context.TODO(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
				g.Expect(err).To(BeNil())
				g.Expect(len(guestUserCABundle.Data["ca-bundle.crt"]) > 0).To(BeTrue())
			} else {
				err := r.client.Get(context.TODO(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

var _ manifestReconciler = manifestAndReconcile[*rbacv1.ClusterRole]{}

func TestDestroyCloudResources(t *testing.T) {

	fakeHostedControlPlane := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-namespace",
			},
		}
	}

	verifyCleanupWebhook := func(g *WithT, c client.Client) {
		wh := manifests.ResourceCreationBlockerWebhook()
		err := c.Get(context.Background(), client.ObjectKeyFromObject(wh), wh)
		g.Expect(err).ToNot(HaveOccurred())
		expected := manifests.ResourceCreationBlockerWebhook()
		reconcileCreationBlockerWebhook(expected)
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
		err := c.Get(context.Background(), client.ObjectKeyFromObject(config), config)
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
		err := c.List(context.Background(), ingressControllers)
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
		err := c.List(context.Background(), services)
		g.Expect(err).ToNot(HaveOccurred())
		for _, svc := range services.Items {
			g.Expect(svc.Spec.Type).ToNot(Equal(corev1.ServiceTypeLoadBalancer))
		}
	}

	verifyServiceExists := func(name string, g *WithT, c client.Client) {
		service := clusterIPService(name)
		err := c.Get(context.Background(), client.ObjectKeyFromObject(service), service)
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
		err := c.List(context.Background(), pvcs)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(len(pvcs.Items)).To(Equal(0))
	}

	verifyPodsRemoved := func(g *WithT, c client.Client) {
		pods := &corev1.PodList{}
		err := c.List(context.Background(), pods)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(len(pods.Items)).To(Equal(0))
	}

	verifyDoneCond := func(g *WithT, c client.Client) {
		hcp := fakeHostedControlPlane()
		err := c.Get(context.Background(), client.ObjectKeyFromObject(hcp), hcp)
		g.Expect(err).ToNot(HaveOccurred())
		cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed))
		g.Expect(cond).ToNot(BeNil())
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
			guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.existing...).Build()
			uncachedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.existingUncached...).Build()
			cpClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeHCP).Build()
			r := &reconciler{
				client:                 guestClient,
				uncachedClient:         uncachedClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}
			_, err := r.destroyCloudResources(context.Background(), fakeHCP)
			g.Expect(err).ToNot(HaveOccurred())
			verifyCleanupWebhook(g, guestClient)
			if test.verify != nil {
				test.verify(g, guestClient, uncachedClient)
			}
			if test.verifyDoneCond {
				verifyDoneCond(g, cpClient)
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
				Image:   "exmple.com/imagens/image:latest",
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
	err := r.reconcileClusterVersion(context.Background(), hcp)
	g.Expect(err).ToNot(HaveOccurred())
	err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(clusterVersion), clusterVersion)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(clusterVersion.Spec.ClusterID).To(Equal(configv1.ClusterID("test-cluster-id")))
	g.Expect(clusterVersion.Spec.Capabilities).To(BeNil())
	g.Expect(clusterVersion.Spec.DesiredUpdate).To(BeNil())
	g.Expect(clusterVersion.Spec.Overrides).To(Equal(testOverrides))
	g.Expect(clusterVersion.Spec.Channel).To(BeEmpty())
}
