package usercabundle

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testHCPNamespace = "master-cluster1"
	testHCPName      = "cluster1"
)

func TestReconcileUserCertCABundle(t *testing.T) {
	tests := map[string]struct {
		hcp                     *hyperv1.HostedControlPlane
		controlPlaneObjects     []client.Object
		guestObjects            []client.Object
		guestClientInterceptors interceptor.Funcs
		expectedData            map[string]string
		expectedError           string
		expectGuestConfigMap    bool
	}{
		"When AdditionalTrustBundle is set, it should create the guest user CA ConfigMap": {
			hcp:                  testHCP(true),
			controlPlaneObjects:  []client.Object{testControlPlaneUserCAConfigMap("newcert")},
			expectedData:         map[string]string{"ca-bundle.crt": "newcert"},
			expectGuestConfigMap: true,
		},
		"When the source user CA ConfigMap changes, it should update the guest user CA ConfigMap": {
			hcp:                 testHCP(true),
			controlPlaneObjects: []client.Object{testControlPlaneUserCAConfigMap("newcert")},
			guestObjects: []client.Object{&corev1.ConfigMap{
				ObjectMeta: manifests.UserCABundle().ObjectMeta,
				Data:       map[string]string{"ca-bundle.crt": "oldcert"},
			}},
			expectedData:         map[string]string{"ca-bundle.crt": "newcert"},
			expectGuestConfigMap: true,
		},
		"When AdditionalTrustBundle is removed, it should delete the guest user CA ConfigMap": {
			hcp: testHCP(false),
			guestObjects: []client.Object{&corev1.ConfigMap{
				ObjectMeta: manifests.UserCABundle().ObjectMeta,
				Data:       map[string]string{"ca-bundle.crt": "oldcert"},
			}},
		},
		"When AdditionalTrustBundle is absent and the guest ConfigMap is missing, it should return without error": {
			hcp: testHCP(false),
		},
		"When the source user CA ConfigMap is missing, it should return an error": {
			hcp:           testHCP(true),
			expectedError: "cannot get AdditionalTrustBundle ConfigMap",
		},
		"When creating the guest user CA ConfigMap fails, it should return an error": {
			hcp: testHCP(true),
			controlPlaneObjects: []client.Object{
				testControlPlaneUserCAConfigMap("newcert"),
			},
			guestClientInterceptors: interceptor.Funcs{
				Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
					return fmt.Errorf("simulated create error")
				},
			},
			expectedError: "simulated create error",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			r := newTestReconciler(test.controlPlaneObjects, test.guestObjects, test.guestClientInterceptors)

			err := r.reconcileUserCertCABundle(t.Context(), test.hcp)
			if test.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(test.expectedError)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			guestUserCABundle := manifests.UserCABundle()
			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
			if !test.expectGuestConfigMap {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(guestUserCABundle.Data).To(Equal(test.expectedData))
		})
	}
}

func TestSetup(t *testing.T) {
	g := NewWithT(t)

	err := Setup(t.Context(), &operator.HostedClusterConfigOperatorConfig{
		Manager:   &testManager{},
		CPCluster: &testCluster{},
		HCPName:   testHCPName,
		Namespace: testHCPNamespace,
	})

	g.Expect(err).NotTo(HaveOccurred())
}

func TestReconcile(t *testing.T) {
	tests := map[string]struct {
		request              reconcile.Request
		controlPlaneObjects  []client.Object
		controlPlaneClient   client.Reader
		expectGuestConfigMap bool
		expectedError        string
	}{
		"When the target HostedControlPlane exists, it should reconcile the guest user CA ConfigMap": {
			request: reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: testHCPNamespace,
				Name:      testHCPName,
			}},
			controlPlaneObjects: []client.Object{
				testHCP(true),
				testControlPlaneUserCAConfigMap("newcert"),
			},
			expectGuestConfigMap: true,
		},
		"When the HostedControlPlane is missing, it should return without error": {
			request: reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: testHCPNamespace,
				Name:      testHCPName,
			}},
		},
		"When the request is for another HostedControlPlane, it should ignore the request": {
			request: reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: testHCPNamespace,
				Name:      "another-cluster",
			}},
		},
		"When fetching the HostedControlPlane fails, it should return an error": {
			request: reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: testHCPNamespace,
				Name:      testHCPName,
			}},
			controlPlaneClient: fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
						return fmt.Errorf("simulated control plane get error")
					},
				}).Build(),
			expectedError: "simulated control plane get error",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			cpClient := test.controlPlaneClient
			if cpClient == nil {
				cpClient = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(test.controlPlaneObjects...).Build()
			}
			r := &reconciler{
				client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				cpClient:               cpClient,
				CreateOrUpdateProvider: upsert.New(false),
				hcpName:                testHCPName,
				hcpNamespace:           testHCPNamespace,
			}

			_, err := r.Reconcile(t.Context(), test.request)
			if test.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(test.expectedError)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			guestUserCABundle := manifests.UserCABundle()
			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(guestUserCABundle), guestUserCABundle)
			if test.expectGuestConfigMap {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(guestUserCABundle.Data).To(Equal(map[string]string{"ca-bundle.crt": "newcert"}))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestAdditionalTrustBundleChangedPredicate(t *testing.T) {
	p := additionalTrustBundleChangedPredicate(testHCPNamespace, testHCPName)
	oldHCP := testHCP(false)
	newHCPWithBundle := testHCP(true)
	newHCPWithBundle.Labels = map[string]string{"changed": "true"}

	tests := map[string]struct {
		oldHCP   *hyperv1.HostedControlPlane
		newHCP   *hyperv1.HostedControlPlane
		expected bool
	}{
		"When the target HostedControlPlane gains an additional trust bundle, it should enqueue reconciliation": {
			oldHCP:   oldHCP,
			newHCP:   newHCPWithBundle,
			expected: true,
		},
		"When the target HostedControlPlane loses an additional trust bundle, it should enqueue reconciliation": {
			oldHCP:   testHCP(true),
			newHCP:   testHCP(false),
			expected: true,
		},
		"When only the target HostedControlPlane metadata changes, it should not enqueue reconciliation": {
			oldHCP: oldHCP,
			newHCP: func() *hyperv1.HostedControlPlane {
				result := oldHCP.DeepCopy()
				result.Labels = map[string]string{"changed": "true"}
				return result
			}(),
		},
		"When another HostedControlPlane changes, it should not enqueue reconciliation": {
			oldHCP: testHCP(false),
			newHCP: func() *hyperv1.HostedControlPlane {
				result := testHCP(true)
				result.Name = "another-cluster"
				return result
			}(),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			got := p.Update(event.TypedUpdateEvent[*hyperv1.HostedControlPlane]{
				ObjectOld: test.oldHCP,
				ObjectNew: test.newHCP,
			})
			g.Expect(got).To(Equal(test.expected))
		})
	}
}

func newTestReconciler(controlPlaneObjects, guestObjects []client.Object, guestClientInterceptors interceptor.Funcs) *reconciler {
	return &reconciler{
		client: fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(guestObjects...).
			WithInterceptorFuncs(guestClientInterceptors).
			Build(),
		cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(controlPlaneObjects...).Build(),
		CreateOrUpdateProvider: upsert.New(false),
		hcpName:                testHCPName,
		hcpNamespace:           testHCPNamespace,
	}
}

func testHCP(additionalTrustBundle bool) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testHCPName,
			Namespace: testHCPNamespace,
		},
	}
	if additionalTrustBundle {
		hcp.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{
			Name: cpomanifests.UserCAConfigMap(testHCPNamespace).Name,
		}
	}
	return hcp
}

func testControlPlaneUserCAConfigMap(data string) *corev1.ConfigMap {
	configMap := cpomanifests.UserCAConfigMap(testHCPNamespace)
	configMap.Data = map[string]string{"ca-bundle.crt": data}
	return configMap
}

type testManager struct {
	manager.Manager
}

func (m *testManager) GetCache() cache.Cache {
	return nil
}

func (m *testManager) GetClient() client.Client {
	return nil
}

func (m *testManager) GetControllerOptions() config.Controller {
	return config.Controller{}
}

func (m *testManager) Add(manager.Runnable) error {
	return nil
}

type testCluster struct {
	cluster.Cluster
}

func (c *testCluster) GetCache() cache.Cache {
	return nil
}

func (c *testCluster) GetClient() client.Client {
	return nil
}
