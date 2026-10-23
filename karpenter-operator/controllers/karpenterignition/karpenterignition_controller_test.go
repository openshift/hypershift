package karpenterignition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/api/util/ipnet"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/images"
	"github.com/openshift/hypershift/support/k8sutil"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/stream-metadata-go/stream"
	"github.com/coreos/stream-metadata-go/stream/rhcos"
	"github.com/go-logr/logr/testr"
	"github.com/vincent-petithory/dataurl"
	"go.uber.org/mock/gomock"
)

const (
	testNamespace           = "clusters-test"
	testIgnitionEndpoint    = "https://ignition.example.com"
	testNodeClassName       = "default"
	configVersionAnnotation = "hypershift.openshift.io/nodeClassCurrentConfigVersion"
)

// Keep platform fixtures fixed so these cases isolate NodeClass GVK handling.
var nodeClassGVKTestCases = map[string]struct {
	gvk schema.GroupVersionKind
}{
	"When the AWS GVK is configured, it should preserve reconciliation behavior": {
		gvk: schema.GroupVersionKind{
			Group:   "karpenter.hypershift.openshift.io",
			Version: "v1",
			Kind:    "OpenshiftEC2NodeClass",
		},
	},
	"When the Azure GVK is configured, it should preserve reconciliation behavior": {
		gvk: schema.GroupVersionKind{
			Group:   "karpenter.hypershift.openshift.io",
			Version: "v1alpha1",
			Kind:    "OpenShiftAzureNodeClass",
		},
	},
	"When a newer Azure GVK is configured, it should preserve reconciliation behavior": {
		gvk: schema.GroupVersionKind{
			Group:   "karpenter.hypershift.openshift.io",
			Version: "v1beta1",
			Kind:    "OpenShiftAzureNodeClass",
		},
	},
}

// fakeVersionResolver implements releaseinfo.VersionResolver for testing.
type fakeVersionResolver struct {
	image       string
	err         error
	calls       int
	lastChannel string
}

func (f *fakeVersionResolver) Resolve(_ context.Context, version, channel string) (string, error) {
	f.calls++
	f.lastChannel = channel
	return f.image, f.err
}

func toNodeClass(t *testing.T, nc *hyperkarpenterv1.OpenshiftEC2NodeClass) *nodeClass {
	t.Helper()
	g := NewWithT(t)
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(nc)
	g.Expect(err).NotTo(HaveOccurred())

	result := &unstructured.Unstructured{Object: object}
	result.SetGroupVersionKind(nc.GroupVersionKind())
	return &nodeClass{object: result}
}

func TestReconcile(t *testing.T) {
	for name, tc := range nodeClassGVKTestCases {
		t.Run(name, func(t *testing.T) {
			testReconcile(t, tc.gvk)
		})
	}
}

func testReconcile(t *testing.T, nodeClassGVK schema.GroupVersionKind) {
	t.Helper()
	g := NewWithT(t)
	scheme := api.Scheme

	mockCtrl := gomock.NewController(t)
	mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
	mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

	fakeImageMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result: &dockerv1client.DockerImageConfig{
			Config: &docker10.DockerConfig{
				Labels: map[string]string{
					// Skip HAProxy setup for sake of testing
					"io.openshift.hypershift.control-plane-operator-skips-haproxy": "true",
				},
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
			InfraID:      "test-infra",
			ClusterID:    "test-cluster-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					Region: "us-east-1",
				},
			},
			Networking: hyperv1.ClusterNetworking{
				ServiceNetwork: []hyperv1.ServiceNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
				},
			},
			PullSecret: corev1.LocalObjectReference{
				Name: "pull-secret",
			},
			AutoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: hyperv1.ProvisionerKarpenter,
					Karpenter: hyperv1.KarpenterConfig{
						Platform: hyperv1.AWSPlatform,
					},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			Version: "4.17.0",
			VersionStatus: &hyperv1.ClusterVersionStatus{
				Desired: configv1.Release{
					Version: "4.17.0",
				},
				History: []configv1.UpdateHistory{
					{
						State:          configv1.CompletedUpdate,
						Version:        "4.17.0",
						Image:          "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
						CompletionTime: &metav1.Time{Time: time.Now()},
					},
				},
			},
			KubeConfig: &hyperv1.KubeconfigSecretRef{
				Name: "admin-kubeconfig",
			},
		},
	}

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
		},
	}

	kubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin-kubeconfig",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://api.test-cluster.example.com:6443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`),
		},
	}

	nodeClass := newNodeClass(nodeClassGVK).object
	nodeClass.SetName(testNodeClassName)
	// The fake client's metadata patches turn an absent status into null.
	nodeClass.Object["status"] = map[string]interface{}{}

	coreConfig1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-ignition-config-1",
			Namespace: testNamespace,
			Labels: map[string]string{
				"hypershift.openshift.io/core-ignition-config": "true",
			},
		},
	}
	coreConfig2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-ignition-config-2",
			Namespace: testNamespace,
			Labels: map[string]string{
				"hypershift.openshift.io/core-ignition-config": "true",
			},
		},
	}

	ignitionServerCACert := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-server-ca-cert",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			"tls.crt": []byte("fake-ca-cert"),
		},
	}

	fakeManagementClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(hcp, pullSecret, kubeconfigSecret, coreConfig1, coreConfig2, ignitionServerCACert).
		Build()

	fakeGuestClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(nodeClass).
		WithStatusSubresource(nodeClass).
		Build()

	r := &KarpenterIgnitionReconciler{
		ManagementClient:        fakeManagementClient,
		GuestClient:             fakeGuestClient,
		NodeClassGVK:            nodeClassGVK,
		ReleaseProvider:         mockedReleaseProvider,
		VersionResolver:         &fakeVersionResolver{},
		ImageMetadataProvider:   fakeImageMetadataProvider,
		HypershiftOperatorImage: "test-hypershift-operator-image",
		IgnitionEndpoint:        testIgnitionEndpoint,
		Namespace:               testNamespace,
		CreateOrUpdateProvider:  upsert.New(false),
	}

	ctx := log.IntoContext(t.Context(), testr.New(t))

	// Part 1: Test initial secrets creation for a single nodeclass
	_, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: testNodeClassName},
	})
	g.Expect(err).NotTo(HaveOccurred())

	expectedNodePoolName := karpenterutil.KarpenterNodePoolName(nodeClass)
	tokenPrefix := "token-" + expectedNodePoolName + "-"
	userDataPrefix := "user-data-" + expectedNodePoolName + "-"

	secretList := &corev1.SecretList{}
	err = fakeManagementClient.List(ctx, secretList, client.InNamespace(testNamespace))
	g.Expect(err).NotTo(HaveOccurred())

	var initialTokenSecretName, initialUserDataSecretName string
	for _, secret := range secretList.Items {
		if strings.HasPrefix(secret.Name, tokenPrefix) {
			initialTokenSecretName = secret.Name
			g.Expect(secret.Data).To(HaveKey("token"))
			g.Expect(secret.Annotations).To(HaveKey(k8sutil.HostedClusterAnnotation), "token secret should have HostedClusterAnnotation")
			g.Expect(secret.Labels).To(HaveKeyWithValue(karpenterutil.ManagedByKarpenterLabel, "true"), "token secret should have ManagedByKarpenterLabel")
		}
		if strings.HasPrefix(secret.Name, userDataPrefix) {
			initialUserDataSecretName = secret.Name
			g.Expect(secret.Data).To(HaveKey("value"))
			g.Expect(secret.Labels).To(HaveKey(hyperkarpenterv1.UserDataAMILabel), "user-data secret should have UserDataAMILabel")
			g.Expect(secret.Labels).To(HaveKeyWithValue(karpenterutil.ManagedByKarpenterLabel, "true"), "user-data secret should have ManagedByKarpenterLabel")
		}
	}
	g.Expect(initialTokenSecretName).NotTo(BeEmpty(), "token secret with prefix %q should be created", tokenPrefix)
	g.Expect(initialUserDataSecretName).NotTo(BeEmpty(), "user-data secret with prefix %q should be created", userDataPrefix)

	taintConfigMap := &corev1.ConfigMap{}
	err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: karpenterutil.KarpenterTaintConfigMapName, Namespace: testNamespace}, taintConfigMap)
	g.Expect(err).NotTo(HaveOccurred(), "taint ConfigMap referenced by the ignition config should be created")
	g.Expect(taintConfigMap.Data).To(HaveKey("config"))

	// Part 2: Test config hash change and a second nodeclass

	// Get the initial config version from the nodeclass annotation
	err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, nodeClass)
	g.Expect(err).NotTo(HaveOccurred())
	initialConfigVersion := nodeClass.GetAnnotations()[configVersionAnnotation]
	g.Expect(initialConfigVersion).NotTo(BeEmpty(), "config version annotation should be set")

	// Change the pull secret reference to trigger a config hash change
	newPullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret-v2",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
		},
	}
	err = fakeManagementClient.Create(ctx, newPullSecret)
	g.Expect(err).NotTo(HaveOccurred())

	err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: "test-hcp", Namespace: testNamespace}, hcp)
	g.Expect(err).NotTo(HaveOccurred())
	hcp.Spec.PullSecret.Name = "pull-secret-v2"
	err = fakeManagementClient.Update(ctx, hcp)
	g.Expect(err).NotTo(HaveOccurred())

	// Create a second nodeclass
	secondNodeClassName := "some-other-nodeclass"
	nodeClass2 := newNodeClass(nodeClassGVK).object
	nodeClass2.SetName(secondNodeClassName)
	nodeClass2.Object["status"] = map[string]interface{}{}
	err = fakeGuestClient.Create(ctx, nodeClass2)
	g.Expect(err).NotTo(HaveOccurred())

	// Second reconcile for first nodeclass
	_, err = r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: testNodeClassName},
	})
	g.Expect(err).NotTo(HaveOccurred())

	// First reconcile for second nodeclass
	_, err = r.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: secondNodeClassName},
	})
	g.Expect(err).NotTo(HaveOccurred())

	// Get the updated config version for first nodeclass
	err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, nodeClass)
	g.Expect(err).NotTo(HaveOccurred())
	updatedConfigVersion := nodeClass.GetAnnotations()[configVersionAnnotation]
	g.Expect(updatedConfigVersion).NotTo(BeEmpty())
	g.Expect(updatedConfigVersion).NotTo(Equal(initialConfigVersion), "config version should change when config is updated")

	// Verify second nodeclass also got its config version
	err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: secondNodeClassName}, nodeClass2)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(nodeClass2.GetAnnotations()[configVersionAnnotation]).NotTo(BeEmpty(), "second nodeclass should have config version")

	// Verify all secrets were created
	err = fakeManagementClient.List(ctx, secretList, client.InNamespace(testNamespace))
	g.Expect(err).NotTo(HaveOccurred())

	// Count all token and user-data secrets
	secondNodePoolName := karpenterutil.KarpenterNodePoolName(nodeClass2)
	secondTokenPrefix := "token-" + secondNodePoolName + "-"
	secondUserDataPrefix := "user-data-" + secondNodePoolName + "-"

	var tokenSecrets, userDataSecrets []string
	var newTokenSecretName, newUserDataSecretName string
	var secondNodeClassTokenFound, secondNodeClassUserDataFound bool

	for _, secret := range secretList.Items {
		// First nodeclass secrets
		if strings.HasPrefix(secret.Name, tokenPrefix) {
			tokenSecrets = append(tokenSecrets, secret.Name)
			if secret.Name != initialTokenSecretName {
				newTokenSecretName = secret.Name
			}
		}
		if strings.HasPrefix(secret.Name, userDataPrefix) {
			userDataSecrets = append(userDataSecrets, secret.Name)
			if secret.Name != initialUserDataSecretName {
				newUserDataSecretName = secret.Name
			}
		}
		// Second nodeclass secrets
		if strings.HasPrefix(secret.Name, secondTokenPrefix) {
			tokenSecrets = append(tokenSecrets, secret.Name)
			secondNodeClassTokenFound = true
		}
		if strings.HasPrefix(secret.Name, secondUserDataPrefix) {
			userDataSecrets = append(userDataSecrets, secret.Name)
			secondNodeClassUserDataFound = true
		}
	}

	// First nodeclass should have new secrets after config change
	g.Expect(newTokenSecretName).NotTo(BeEmpty(), "new token secret with updated hash should be created")
	g.Expect(newUserDataSecretName).NotTo(BeEmpty(), "new user-data secret with updated hash should be created")
	g.Expect(newTokenSecretName).NotTo(Equal(initialTokenSecretName), "token secret name should change")
	g.Expect(newUserDataSecretName).NotTo(Equal(initialUserDataSecretName), "user-data secret name should change")

	// Second nodeclass should have its secrets
	g.Expect(secondNodeClassTokenFound).To(BeTrue(), "second nodeclass should have token secret")
	g.Expect(secondNodeClassUserDataFound).To(BeTrue(), "second nodeclass should have user-data secret")

	// Total: 5 secrets (3 token + 2 user-data)
	//
	// Token secrets behavior (from token.go cleanupOutdated):
	//   Old token secrets are NOT deleted - they get an expiration timestamp set via
	//   setExpirationTimestampOnToken() for the token secret controller to clean up later.
	//   Result: 3 token secrets (old + new for first nodeclass, + 1 for second)
	//
	// User-data secrets behavior (from token.go cleanupOutdated):
	//   For non-AWS platforms, old user-data secrets are deleted immediately.
	//   Since our in-memory NodePool has no Platform set, it's treated as non-AWS.
	//   Result: 2 user-data secrets (only current version per nodeclass)
	// If that were ever to change, we need to come back here and update the test to have 3 user-data secrets instead.
	// https://github.com/openshift/hypershift/blob/825484eb33d14b4ab849b428d134582320655fcf/hypershift-operator/controllers/nodepool/token.go#L197
	g.Expect(len(tokenSecrets)).To(Equal(3), "should have 3 token secrets, got: %v", tokenSecrets)
	g.Expect(len(userDataSecrets)).To(Equal(2), "should have 2 user-data secrets, got: %v", userDataSecrets)
}

func TestReconcileVersionResolution(t *testing.T) {
	scheme := api.Scheme

	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				InfraID:      "test-infra",
				ClusterID:    "test-cluster-id",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						Region: "us-east-1",
					},
				},
				Networking: hyperv1.ClusterNetworking{
					ServiceNetwork: []hyperv1.ServiceNetworkEntry{
						{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
					},
				},
				PullSecret: corev1.LocalObjectReference{
					Name: "pull-secret",
				},
				AutoNode: hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: hyperv1.KarpenterConfig{
							Platform: hyperv1.AWSPlatform,
						},
					},
				},
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Version: "4.17.0",
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "4.17.0",
					},
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							Version:        "4.17.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
							CompletionTime: &metav1.Time{Time: time.Now()},
						},
					},
				},
				KubeConfig: &hyperv1.KubeconfigSecretRef{
					Name: "admin-kubeconfig",
				},
			},
		}
	}

	baseManagementObjects := func(hcp *hyperv1.HostedControlPlane) []client.Object {
		return []client.Object{
			hcp,
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: testNamespace},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: testNamespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://api.test-cluster.example.com:6443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`),
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "core-ignition-config-1", Namespace: testNamespace,
					Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "core-ignition-config-2", Namespace: testNamespace,
					Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "set-karpenter-taint", Namespace: testNamespace,
				},
				Data: map[string]string{"config": ""},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "ignition-server-ca-cert", Namespace: testNamespace},
				Data:       map[string][]byte{"tls.crt": []byte("fake-ca-cert")},
			},
		}
	}

	fakeImageMetadataProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Result: &dockerv1client.DockerImageConfig{
			Config: &docker10.DockerConfig{
				Labels: map[string]string{
					"io.openshift.hypershift.control-plane-operator-skips-haproxy": "true",
				},
			},
		},
	}

	for name, tc := range nodeClassGVKTestCases {
		t.Run(name, func(t *testing.T) {
			t.Run("When version is set, it should call resolver and use resolved image", func(t *testing.T) {
				g := NewWithT(t)
				mockCtrl := gomock.NewController(t)
				mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
				resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:resolved123"
				mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

				hcp := baseHCP()
				resolver := &fakeVersionResolver{image: resolvedImage}

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName("version-test")
				nodeClass.Object["spec"] = map[string]interface{}{"version": "4.17.0"}
				nodeClass.Object["status"] = map[string]interface{}{}

				fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
				fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(nodeClass).
					WithStatusSubresource(nodeClass).
					Build()

				r := &KarpenterIgnitionReconciler{
					ManagementClient:        fakeManagementClient,
					GuestClient:             fakeGuestClient,
					NodeClassGVK:            tc.gvk,
					ReleaseProvider:         mockedReleaseProvider,
					VersionResolver:         resolver,
					ImageMetadataProvider:   fakeImageMetadataProvider,
					HypershiftOperatorImage: "test-hypershift-operator-image",
					IgnitionEndpoint:        testIgnitionEndpoint,
					Namespace:               testNamespace,
				}

				ctx := log.IntoContext(t.Context(), testr.New(t))
				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: "version-test"},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resolver.calls).To(Equal(1), "resolver should be called once")

				// Verify status was updated with resolved image
				updatedNC := newNodeClass(tc.gvk)
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "version-test"}, updatedNC.object)
				g.Expect(err).NotTo(HaveOccurred())
				status, err := updatedNC.status()
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(status.ReleaseImage).To(Equal(resolvedImage))
				skewCondition := findCondition(status.Conditions, hyperkarpenterv1.ConditionTypeSupportedVersionSkew)
				g.Expect(skewCondition).NotTo(BeNil())
				g.Expect(skewCondition.Status).To(Equal(metav1.ConditionTrue))
			})

			t.Run("When token reconciliation fails, it should persist version skew status and return the token error", func(t *testing.T) {
				g := NewWithT(t)
				tokenErr := fmt.Errorf("release lookup failed")
				mockedReleaseProvider := releaseinfo.NewMockProvider(gomock.NewController(t))
				mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, tokenErr)

				hcp := baseHCP()
				nc := newNodeClass(tc.gvk)
				nc.object.SetName("token-error-test")
				nc.object.Object["status"] = map[string]interface{}{}
				managementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
				guestClient := fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(nc.object).WithStatusSubresource(nc.object).Build()
				r := &KarpenterIgnitionReconciler{
					ManagementClient: managementClient,
					GuestClient:      guestClient,
					NodeClassGVK:     tc.gvk,
					ReleaseProvider:  mockedReleaseProvider,
					Namespace:        testNamespace,
				}

				ctx := log.IntoContext(t.Context(), testr.New(t))
				// Keep the resolved version unchanged so only the skew condition needs patching.
				g.Expect(r.updateVersionStatus(ctx, nc, hcp.Spec.ReleaseImage, "4.17.0", nil, nil)).To(Succeed())
				initialStatus, err := nc.status()
				g.Expect(err).NotTo(HaveOccurred())

				_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(nc.object)})
				g.Expect(err).To(MatchError(tokenErr))
				g.Expect(err.Error()).To(ContainSubstring("failed to build config generator"))

				updated := newNodeClass(tc.gvk)
				g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(nc.object), updated.object)).To(Succeed())
				status, err := updated.status()
				g.Expect(err).NotTo(HaveOccurred())
				skewCondition := findCondition(status.Conditions, hyperkarpenterv1.ConditionTypeSupportedVersionSkew)
				g.Expect(skewCondition).NotTo(BeNil())
				g.Expect(skewCondition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(skewCondition.Reason).To(Equal(hyperkarpenterv1.ConditionReasonVersionNotSpecified))
				g.Expect(findCondition(status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)).To(
					Equal(findCondition(initialStatus.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)))
			})

			t.Run("When version is not set, it should not call resolver and use HCP release image", func(t *testing.T) {
				g := NewWithT(t)
				mockCtrl := gomock.NewController(t)
				mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
				mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

				hcp := baseHCP()
				resolver := &fakeVersionResolver{image: "should-not-be-used"}

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName("no-version-test")
				nodeClass.Object["status"] = map[string]interface{}{}

				fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
				fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(nodeClass).
					WithStatusSubresource(nodeClass).
					Build()

				r := &KarpenterIgnitionReconciler{
					ManagementClient:        fakeManagementClient,
					GuestClient:             fakeGuestClient,
					NodeClassGVK:            tc.gvk,
					ReleaseProvider:         mockedReleaseProvider,
					VersionResolver:         resolver,
					ImageMetadataProvider:   fakeImageMetadataProvider,
					HypershiftOperatorImage: "test-hypershift-operator-image",
					IgnitionEndpoint:        testIgnitionEndpoint,
					Namespace:               testNamespace,
				}

				ctx := log.IntoContext(t.Context(), testr.New(t))
				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: "no-version-test"},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resolver.calls).To(Equal(0), "resolver should not be called when version is not set")

				// Verify status.releaseImage matches the HCP release image when version is not set
				updatedNC := newNodeClass(tc.gvk)
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "no-version-test"}, updatedNC.object)
				g.Expect(err).NotTo(HaveOccurred())
				status, err := updatedNC.status()
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(status.ReleaseImage).To(Equal(hcp.Spec.ReleaseImage))
			})

			t.Run("When version resolution fails, it should return error", func(t *testing.T) {
				g := NewWithT(t)
				mockCtrl := gomock.NewController(t)
				mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)

				hcp := baseHCP()
				resolver := &fakeVersionResolver{err: fmt.Errorf("Cincinnati API unavailable")}

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName("fail-version-test")
				nodeClass.Object["spec"] = map[string]interface{}{"version": "4.17.0"}
				nodeClass.Object["status"] = map[string]interface{}{}

				fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
				fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(nodeClass).
					WithStatusSubresource(nodeClass).
					Build()

				r := &KarpenterIgnitionReconciler{
					ManagementClient:        fakeManagementClient,
					GuestClient:             fakeGuestClient,
					NodeClassGVK:            tc.gvk,
					ReleaseProvider:         mockedReleaseProvider,
					VersionResolver:         resolver,
					ImageMetadataProvider:   fakeImageMetadataProvider,
					HypershiftOperatorImage: "test-hypershift-operator-image",
					IgnitionEndpoint:        testIgnitionEndpoint,
					Namespace:               testNamespace,
				}

				ctx := log.IntoContext(t.Context(), testr.New(t))
				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: "fail-version-test"},
				})
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("Cincinnati API unavailable"))

				// Verify VersionResolved condition is set to False
				updatedNC := newNodeClass(tc.gvk)
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "fail-version-test"}, updatedNC.object)
				g.Expect(err).NotTo(HaveOccurred())
				status, err := updatedNC.status()
				g.Expect(err).NotTo(HaveOccurred())
				versionCondition := findCondition(status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)
				g.Expect(versionCondition).NotTo(BeNil(), "VersionResolved condition should be set")
				g.Expect(versionCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(versionCondition.Reason).To(Equal("ResolutionFailed"))

			})

			t.Run("When channel is set, it should pass HCP channel to resolver", func(t *testing.T) {
				g := NewWithT(t)
				mockCtrl := gomock.NewController(t)
				mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
				resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:fast123"
				mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

				hcp := baseHCP()
				hcp.Spec.Channel = "fast-4.17"
				resolver := &fakeVersionResolver{image: resolvedImage}

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName("hcp-channel-test")
				nodeClass.Object["spec"] = map[string]interface{}{"version": "4.17.0"}
				nodeClass.Object["status"] = map[string]interface{}{}

				fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
				fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(nodeClass).
					WithStatusSubresource(nodeClass).
					Build()

				r := &KarpenterIgnitionReconciler{
					ManagementClient:        fakeManagementClient,
					GuestClient:             fakeGuestClient,
					NodeClassGVK:            tc.gvk,
					ReleaseProvider:         mockedReleaseProvider,
					VersionResolver:         resolver,
					ImageMetadataProvider:   fakeImageMetadataProvider,
					HypershiftOperatorImage: "test-hypershift-operator-image",
					IgnitionEndpoint:        testIgnitionEndpoint,
					Namespace:               testNamespace,
				}

				ctx := log.IntoContext(t.Context(), testr.New(t))
				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: "hcp-channel-test"},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resolver.lastChannel).To(Equal("fast-4.17"), "resolver should receive HCP channel prefix combined with version")
			})
		})
	}

	newAzureRelease := func(version string, image rhcos.AzureMarketplaceImage) *releaseinfo.ReleaseImage {
		release := testutils.InitReleaseImageOrDie(version)
		release.StreamMetadata = &stream.Stream{
			Architectures: map[string]stream.Arch{
				"x86_64": {
					RHELCoreOSExtensions: &rhcos.Extensions{
						Marketplace: &rhcos.Marketplace{
							Azure: &rhcos.AzureMarketplace{
								NoPurchasePlan: &rhcos.AzureMarketplaceImages{Gen2: &image},
							},
						},
					},
				},
			},
		}
		return release
	}
	testCases := map[string]struct {
		version      string
		releaseImage string
		image        rhcos.AzureMarketplaceImage
	}{
		"When an Azure NodeClass pins a release, it should generate ignition and Marketplace annotations from that release": {
			version:      "4.20.0",
			releaseImage: "quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64",
			image: rhcos.AzureMarketplaceImage{
				Publisher: "azureopenshift",
				Offer:     "aro4",
				SKU:       "aro_420-v2",
				Version:   "420.9.20250701",
			},
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := baseHCP()
			hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.21.0-x86_64"
			hcp.Spec.Channel = "stable-4.21"
			hcp.Spec.Platform = hyperv1.PlatformSpec{
				Type:  hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{Location: "eastus"},
			}
			hcp.Spec.AutoNode.Provisioner.Karpenter.Platform = hyperv1.AzurePlatform
			hcp.Status.Version = "4.21.0"
			hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
				Desired: configv1.Release{Version: "4.21.0", Image: hcp.Spec.ReleaseImage},
				History: []configv1.UpdateHistory{{
					State:   configv1.CompletedUpdate,
					Version: "4.21.0",
					Image:   hcp.Spec.ReleaseImage,
				}},
			}

			mockedReleaseProvider := releaseinfo.NewMockProvider(gomock.NewController(t))
			mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), tc.releaseImage, gomock.Any()).
				Return(newAzureRelease(tc.version, tc.image), nil)
			mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), hcp.Spec.ReleaseImage, gomock.Any()).
				Return(newAzureRelease("4.21.0", rhcos.AzureMarketplaceImage{
					Publisher: "azureopenshift",
					Offer:     "aro4",
					SKU:       "aro_421-v2",
					Version:   "421.9.20250801",
				}), nil).AnyTimes()

			azureGVK := schema.GroupVersionKind{
				Group:   "karpenter.hypershift.openshift.io",
				Version: "v1alpha1",
				Kind:    "OpenShiftAzureNodeClass",
			}
			nodeClass := newNodeClass(azureGVK).object
			nodeClass.SetName("azure-version-test")
			g.Expect(unstructured.SetNestedField(nodeClass.Object, tc.version, "spec", "version")).To(Succeed())
			// The fake client's metadata patches turn an absent status into null.
			nodeClass.Object["status"] = map[string]interface{}{}

			managementClient := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(baseManagementObjects(hcp)...).Build()
			guestClient := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(nodeClass).WithStatusSubresource(nodeClass).Build()
			resolver := &fakeVersionResolver{image: tc.releaseImage}
			r := &KarpenterIgnitionReconciler{
				ManagementClient:        managementClient,
				GuestClient:             guestClient,
				NodeClassGVK:            azureGVK,
				ReleaseProvider:         mockedReleaseProvider,
				VersionResolver:         resolver,
				ImageMetadataProvider:   fakeImageMetadataProvider,
				HypershiftOperatorImage: "test-hypershift-operator-image",
				IgnitionEndpoint:        testIgnitionEndpoint,
				Namespace:               testNamespace,
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resolver.calls).To(Equal(1))

			secrets := &corev1.SecretList{}
			g.Expect(managementClient.List(ctx, secrets, client.InNamespace(testNamespace),
				client.MatchingLabels{karpenterutil.ManagedByKarpenterLabel: "true"})).To(Succeed())
			g.Expect(secrets.Items).To(HaveLen(2))
			var tokenSecret, userDataSecret *corev1.Secret
			for i := range secrets.Items {
				secret := &secrets.Items[i]
				if strings.HasPrefix(secret.Name, "token-") {
					tokenSecret = secret
				}
				if strings.HasPrefix(secret.Name, "user-data-") {
					userDataSecret = secret
				}
			}
			g.Expect(tokenSecret).NotTo(BeNil())
			g.Expect(tokenSecret.Data["release"]).To(Equal([]byte(tc.releaseImage)))
			g.Expect(tokenSecret.Data["release-version"]).To(Equal([]byte(tc.version)))
			g.Expect(userDataSecret).NotTo(BeNil())
			g.Expect(userDataSecret.Data["value"]).NotTo(BeEmpty())
			g.Expect(userDataSecret.Annotations).To(HaveKeyWithValue("hypershift.openshift.io/nodePool",
				testNamespace+"/"+karpenterutil.KarpenterNodePoolName(nodeClass)))
			for key, value := range map[string]string{
				"hypershift.openshift.io/azure-marketplace-publisher": tc.image.Publisher,
				"hypershift.openshift.io/azure-marketplace-offer":     tc.image.Offer,
				"hypershift.openshift.io/azure-marketplace-sku":       tc.image.SKU,
				"hypershift.openshift.io/azure-marketplace-version":   tc.image.Version,
			} {
				g.Expect(userDataSecret.Annotations).To(HaveKeyWithValue(key, value))
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
				Networking: hyperv1.ClusterNetworking{
					ServiceNetwork: []hyperv1.ServiceNetworkEntry{
						{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
					},
				},
				Channel: "stable-4.17",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Version: "4.17.0",
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "4.17.0",
					},
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							Version:        "4.17.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
							CompletionTime: &metav1.Time{Time: time.Now()},
						},
					},
				},
			},
		}
	}

	baseHostedCluster := func(hcp *hyperv1.HostedControlPlane) *hyperv1.HostedCluster {
		hc, err := hostedClusterFromHCP(hcp, testIgnitionEndpoint)
		if err != nil {
			t.Fatal(err)
		}
		return hc
	}

	t.Run("When version is empty it should return HCP release image", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		resolver := &fakeVersionResolver{image: "should-not-be-called"}

		r := &KarpenterIgnitionReconciler{VersionResolver: resolver}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		image, err := r.resolveReleaseImage(ctx, hcp, "")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(image).To(Equal(hcp.Spec.ReleaseImage))
		g.Expect(resolver.calls).To(Equal(0), "resolver should not be called when version is empty")
	})

	t.Run("When version is set it should resolve and return release image", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:abc123"
		resolver := &fakeVersionResolver{image: resolvedImage}

		r := &KarpenterIgnitionReconciler{VersionResolver: resolver}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		image, err := r.resolveReleaseImage(ctx, hcp, "4.17.0")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(image).To(Equal(resolvedImage))
		g.Expect(resolver.calls).To(Equal(1))
		g.Expect(resolver.lastChannel).To(Equal("stable-4.17"))
	})

	t.Run("When version is invalid semver it should return error", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		hc := baseHostedCluster(hcp)

		err := validateVersion(hc, "not-a-version", "")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse OpenshiftEC2NodeClass version"))
	})

	t.Run("When version is below minimum supported it should return error", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		hcp.Status.Version = "4.17.0"
		hc := baseHostedCluster(hcp)

		// 4.13.0 is below the minimum supported version (4.14.0)
		err := validateVersion(hc, "4.13.0", "")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("minimum version supported"))
	})

	t.Run("When version exceeds allowed skew it should return skew error without failing", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		hcp.Status.Version = "4.20.0"
		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.20.0"},
			History: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					Version:        "4.20.0",
					CompletionTime: &metav1.Time{Time: time.Now()},
				},
			},
		}
		hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64"
		hc := baseHostedCluster(hcp)
		resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:skewed"
		resolver := &fakeVersionResolver{image: resolvedImage}

		r := &KarpenterIgnitionReconciler{VersionResolver: resolver}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		// validateVersion should pass (4.16.0 is valid, just outside skew policy)
		g.Expect(validateVersion(hc, "4.16.0", "")).To(Succeed())

		// 4.16.0 is 4 minor versions behind 4.20.0, exceeding the n-3 skew policy
		image, err := r.resolveReleaseImage(ctx, hcp, "4.16.0")
		g.Expect(err).NotTo(HaveOccurred(), "resolveReleaseImage should not return a hard error for skew")
		g.Expect(image).To(Equal(resolvedImage))
		g.Expect(resolver.calls).To(Equal(1), "resolver should still be called despite skew")

		skewErr := detectVersionSkew(hc, "4.16.0")
		g.Expect(skewErr).To(HaveOccurred())
		g.Expect(skewErr.Error()).To(ContainSubstring("minor version"))
	})

	t.Run("When resolver fails it should return error", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		resolver := &fakeVersionResolver{err: fmt.Errorf("Cincinnati API unavailable")}

		r := &KarpenterIgnitionReconciler{VersionResolver: resolver}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		_, err := r.resolveReleaseImage(ctx, hcp, "4.17.0")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("Cincinnati API unavailable"))
	})

	t.Run("When HCP version cannot be parsed it should return error", func(t *testing.T) {
		g := NewWithT(t)
		hcp := baseHCP()
		hcp.Status.Version = "invalid"
		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "invalid"},
			History: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					Version:        "invalid",
					CompletionTime: &metav1.Time{Time: time.Now()},
				},
			},
		}
		hc := baseHostedCluster(hcp)

		err := validateVersion(hc, "4.17.0", "")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to parse HostedCluster version"))
	})
}

func TestUpdateVersionStatus(t *testing.T) {
	scheme := api.Scheme

	newNodeClass := func(name string, version string) *hyperkarpenterv1.OpenshiftEC2NodeClass {
		return &hyperkarpenterv1.OpenshiftEC2NodeClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "karpenter.hypershift.openshift.io/v1",
				Kind:       "OpenshiftEC2NodeClass",
			},
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Version: version,
			},
		}
	}

	t.Run("When version is not set it should set VersionNotSpecified condition and releaseImage", func(t *testing.T) {
		g := NewWithT(t)
		nc := newNodeClass("no-version", "")
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nc).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{GuestClient: fakeGuestClient}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		hcpImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"
		err := r.updateVersionStatus(ctx, toNodeClass(t, nc), hcpImage, "4.17.0", nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "no-version"}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Status.ReleaseImage).To(Equal(hcpImage))
		g.Expect(updated.Status.Version).To(Equal("4.17.0"))

		cond := findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Reason).To(Equal("VersionNotSpecified"))
	})

	t.Run("When version is set and resolution succeeds it should set VersionResolved condition", func(t *testing.T) {
		g := NewWithT(t)
		nc := newNodeClass("with-version", "4.17.0")
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nc).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{GuestClient: fakeGuestClient}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:abc123"
		err := r.updateVersionStatus(ctx, toNodeClass(t, nc), resolvedImage, "4.17.0", nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "with-version"}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Status.ReleaseImage).To(Equal(resolvedImage))
		g.Expect(updated.Status.Version).To(Equal("4.17.0"))

		cond := findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Reason).To(Equal("VersionResolved"))
		g.Expect(cond.Message).To(ContainSubstring("4.17.0"))
		g.Expect(cond.Message).To(ContainSubstring(resolvedImage))
	})

	t.Run("When version is set and resolution fails it should set ResolutionFailed condition", func(t *testing.T) {
		g := NewWithT(t)
		nc := newNodeClass("fail-version", "4.17.0")
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nc).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{GuestClient: fakeGuestClient}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		resolveErr := fmt.Errorf("Cincinnati API unavailable")
		err := r.updateVersionStatus(ctx, toNodeClass(t, nc), "", "", resolveErr, nil)
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "fail-version"}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Status.ReleaseImage).To(BeEmpty())

		cond := findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal("ResolutionFailed"))
		g.Expect(cond.Message).To(ContainSubstring("Cincinnati API unavailable"))
		g.Expect(findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeSupportedVersionSkew)).To(BeNil())
	})

	testCases := []struct {
		name           string
		initialSkewErr error
		skewErr        error
		expectedStatus metav1.ConditionStatus
		expectedReason string
	}{
		{
			name:           "When only version skew becomes unsupported, it should persist the condition",
			skewErr:        fmt.Errorf("version skew is unsupported"),
			expectedStatus: metav1.ConditionFalse,
			expectedReason: hyperkarpenterv1.ConditionReasonUnsupportedSkew,
		},
		{
			name:           "When only version skew becomes supported, it should persist the condition",
			initialSkewErr: fmt.Errorf("version skew is unsupported"),
			expectedStatus: metav1.ConditionTrue,
			expectedReason: hyperkarpenterv1.ConditionReasonAsExpected,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nc := newNodeClass("skew-only", "4.17.0")
			guestClient := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(nc).WithStatusSubresource(nc).Build()
			r := &KarpenterIgnitionReconciler{GuestClient: guestClient}
			ctx := log.IntoContext(t.Context(), testr.New(t))
			resolvedImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"

			wrapped := toNodeClass(t, nc)
			g.Expect(r.updateVersionStatus(ctx, wrapped, resolvedImage, "4.17.0", nil, &tc.initialSkewErr)).To(Succeed())
			g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(nc), wrapped.object)).To(Succeed())
			initialStatus, err := wrapped.status()
			g.Expect(err).NotTo(HaveOccurred())
			initialVersionCondition := findCondition(initialStatus.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)
			g.Expect(initialVersionCondition).NotTo(BeNil())

			g.Expect(r.updateVersionStatus(ctx, wrapped, resolvedImage, "4.17.0", nil, &tc.skewErr)).To(Succeed())
			updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(nc), updated)).To(Succeed())
			skewCondition := findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeSupportedVersionSkew)
			g.Expect(skewCondition).NotTo(BeNil())
			g.Expect(skewCondition.Status).To(Equal(tc.expectedStatus))
			g.Expect(skewCondition.Reason).To(Equal(tc.expectedReason))
			g.Expect(findCondition(updated.Status.Conditions, hyperkarpenterv1.ConditionTypeVersionResolved)).To(Equal(initialVersionCondition))
			g.Expect(updated.Status.ReleaseImage).To(Equal(resolvedImage))
			g.Expect(updated.Status.Version).To(Equal("4.17.0"))
		})
	}

	t.Run("When condition has not changed it should not patch", func(t *testing.T) {
		g := NewWithT(t)
		nc := newNodeClass("no-change", "")
		statusPatches := 0
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nc).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					statusPatches++
					return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
				},
			}).
			Build()

		r := &KarpenterIgnitionReconciler{GuestClient: fakeGuestClient}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		hcpImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"

		// First call sets both conditions.
		var skewErr error
		wrapped := toNodeClass(t, nc)
		err := r.updateVersionStatus(ctx, wrapped, hcpImage, "4.17.0", nil, &skewErr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(statusPatches).To(Equal(1))

		// Re-fetch to get the updated object with the condition already set
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "no-change"}, wrapped.object)
		g.Expect(err).NotTo(HaveOccurred())

		// Second call with same inputs should not error (no-op patch skipped)
		err = r.updateVersionStatus(ctx, wrapped, hcpImage, "4.17.0", nil, &skewErr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(statusPatches).To(Equal(1), "unchanged conditions should not trigger another status patch")
	})
}

func TestCurrentClusterRelease(t *testing.T) {
	completedTime1 := metav1.Now()
	completedTime2 := metav1.NewTime(completedTime1.Add(time.Hour))

	testCases := []struct {
		name            string
		hostedCluster   *hyperv1.HostedCluster
		expectedImage   string
		expectedVersion string
		expectedRequeue bool
	}{
		{
			name: "When there is a single completed entry, it should return that entry's version and image",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.17.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "4.17.0",
		},
		{
			name: "When upgrade is in progress, it should return completed entry, not the partial",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "",
								Image:   "quay.io/release:4.18.0",
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are multiple completed entries, it should return the first (newest per API ordering contract)",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.18.0",
								Image:          "quay.io/release:4.18.0",
								CompletionTime: &completedTime2,
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.18.0",
			expectedVersion: "4.18.0",
		},
		{
			name: "When there is a partial entry and a completed entry, it should return the completed entry",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.18.0",
								Image:   "quay.io/release:4.18.0",
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are no completed entries and exactly one Partial entry, it should fall back to the Partial entry",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.17.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.17.0",
								Image:   "quay.io/release:4.17.0",
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are no completed entries and multiple Partial entries, it should return the first Partial entry",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.18.0",
								Image:   "quay.io/release:4.18.0",
							},
							{
								State:   configv1.PartialUpdate,
								Version: "4.17.0",
								Image:   "quay.io/release:4.17.0",
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.18.0",
			expectedVersion: "4.18.0",
		},
		{
			name: "When Spec image differs from only Partial entry during upgrade, it should return Partial image not Spec",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.17.0",
								Image:   "quay.io/release:4.17.0",
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "4.17.0",
		},
		{
			name: "When multi-upgrade has 2 Completed and 1 Partial, it should return latest Completed not the Partial",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.19.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.19.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.19.0",
								Image:   "quay.io/release:4.19.0",
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.18.0",
								Image:          "quay.io/release:4.18.0",
								CompletionTime: &completedTime2,
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.18.0",
			expectedVersion: "4.18.0",
		},
		{
			name: "When first Completed entry has nil CompletionTime, it is still returned (ordering is trusted over timestamps)",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.18.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.18.0",
								Image:   "quay.io/release:4.18.0",
								// nil CompletionTime — still returned because it's first
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/release:4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/release:4.18.0",
			expectedVersion: "4.18.0",
		},
		{
			name: "When history is empty, it should signal requeue",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.17.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{},
					},
				},
			},
			expectedRequeue: true,
		},
		{
			name: "When Version is nil, it should return Spec.Release.Image and empty version",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Release: hyperv1.Release{Image: "quay.io/release:4.17.0"},
				},
				Status: hyperv1.HostedClusterStatus{
					Version: nil,
				},
			},
			expectedImage:   "quay.io/release:4.17.0",
			expectedVersion: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			image, version, requeue := currentClusterRelease(tc.hostedCluster)
			g.Expect(requeue).To(Equal(tc.expectedRequeue))
			if !tc.expectedRequeue {
				g.Expect(image).To(Equal(tc.expectedImage))
				g.Expect(version).To(Equal(tc.expectedVersion))
			}
		})
	}
}

func TestHostedClusterFromHCP(t *testing.T) {
	completedTime := metav1.Now()

	testCases := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		expectVersionNil     bool
		expectedDesired      string
		expectedHistoryLen   int
		expectedReleaseImage string
	}{
		{
			name: "When ControlPlaneVersion is empty, it should leave Status.Version nil",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				},
				Status: hyperv1.HostedControlPlaneStatus{},
			},
			expectVersionNil:     true,
			expectedReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
		},
		{
			name: "When ControlPlaneVersion is populated, it should convert history onto the hosted cluster",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{
						Desired: configv1.Release{Version: "4.17.0", Image: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"},
						History: []hyperv1.ControlPlaneUpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								Image:          "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
								CompletionTime: completedTime,
							},
						},
					},
				},
			},
			expectVersionNil:     false,
			expectedDesired:      "4.17.0",
			expectedHistoryLen:   1,
			expectedReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hc, err := hostedClusterFromHCP(tc.hcp, testIgnitionEndpoint)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(hc.Spec.Release.Image).To(Equal(tc.expectedReleaseImage))
			g.Expect(hc.Status.IgnitionEndpoint).To(Equal(testIgnitionEndpoint))

			if tc.expectVersionNil {
				g.Expect(hc.Status.Version).To(BeNil())
			} else {
				g.Expect(hc.Status.Version).NotTo(BeNil())
				g.Expect(hc.Status.Version.Desired.Version).To(Equal(tc.expectedDesired))
				g.Expect(hc.Status.Version.History).To(HaveLen(tc.expectedHistoryLen))
			}
		})
	}

	t.Run("When ControlPlaneVersion is empty, currentClusterRelease should fall back to spec release image", func(t *testing.T) {
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
			},
		}

		hc, err := hostedClusterFromHCP(hcp, testIgnitionEndpoint)
		g.Expect(err).NotTo(HaveOccurred())

		image, version, requeue := currentClusterRelease(hc)
		g.Expect(requeue).To(BeFalse())
		g.Expect(image).To(Equal(hcp.Spec.ReleaseImage))
		g.Expect(version).To(BeEmpty())
	})

	t.Run("During CP upgrade, currentClusterRelease should return old completed release", func(t *testing.T) {
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{
					Desired: configv1.Release{
						Version: "4.18.0",
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
					},
					History: []hyperv1.ControlPlaneUpdateHistory{
						{State: configv1.PartialUpdate, Version: "4.18.0", Image: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64"},
						{State: configv1.CompletedUpdate, Version: "4.17.0", Image: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64", CompletionTime: completedTime},
					},
				},
			},
		}

		hc, err := hostedClusterFromHCP(hcp, testIgnitionEndpoint)
		g.Expect(err).NotTo(HaveOccurred())

		image, version, requeue := currentClusterRelease(hc)
		g.Expect(requeue).To(BeFalse())
		g.Expect(image).To(Equal("quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"))
		g.Expect(version).To(Equal("4.17.0"))
	})

	t.Run("After CP upgrade completes, currentClusterRelease should return new release", func(t *testing.T) {
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{
					Desired: configv1.Release{
						Version: "4.18.0",
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
					},
					History: []hyperv1.ControlPlaneUpdateHistory{
						{State: configv1.CompletedUpdate, Version: "4.18.0", Image: "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64", CompletionTime: completedTime},
						{State: configv1.CompletedUpdate, Version: "4.17.0", Image: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64", CompletionTime: completedTime},
					},
				},
			},
		}

		hc, err := hostedClusterFromHCP(hcp, testIgnitionEndpoint)
		g.Expect(err).NotTo(HaveOccurred())

		image, version, requeue := currentClusterRelease(hc)
		g.Expect(requeue).To(BeFalse())
		g.Expect(image).To(Equal("quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64"))
		g.Expect(version).To(Equal("4.18.0"))
	})
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i, c := range conditions {
		if c.Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func TestReconcileKubeletConfigMapOrphanCleanup(t *testing.T) {
	scheme := api.Scheme

	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				AutoNode: hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: hyperv1.KarpenterConfig{
							Platform: hyperv1.AWSPlatform,
						},
					},
				},
			},
		}
	}

	kubeletConfig := hyperkarpenterv1.KubeletConfiguration{
		MaxPods: 500,
	}

	t.Run("When spec.kubelet is set it should add the finalizer and create the ConfigMap", func(t *testing.T) {
		g := NewWithT(t)

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: kubeletConfig,
			},
		}
		hcp := baseHCP()

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		// Add the finalizer as Reconcile() would
		original := nodeClass.DeepCopy()
		nodeClass.Finalizers = append(nodeClass.Finalizers, kubeletConfigFinalizer)
		err = fakeGuestClient.Patch(ctx, nodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
		g.Expect(err).NotTo(HaveOccurred())

		// Verify ConfigMap was created
		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred())

		// Verify finalizer is present on the NodeClass
		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).To(ContainElement(kubeletConfigFinalizer))
	})

	t.Run("When spec.kubelet is nil and finalizer is present it should remove the finalizer", func(t *testing.T) {
		g := NewWithT(t)

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:       testNodeClassName,
				Finalizers: []string{kubeletConfigFinalizer},
			},
			// Spec.Kubelet is nil
		}
		hcp := baseHCP()

		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build(),
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))

		// Simulate the finalizer removal branch in Reconcile(): kubelet is nil, finalizer present
		original := nodeClass.DeepCopy()
		nodeClass.Finalizers = nil
		err := r.GuestClient.Patch(ctx, nodeClass, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).NotTo(ContainElement(kubeletConfigFinalizer))
	})

	t.Run("When spec.kubelet is nil and finalizer is absent it should not add the finalizer", func(t *testing.T) {
		g := NewWithT(t)

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			// Spec.Kubelet is nil, no finalizer
		}
		hcp := baseHCP()

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).NotTo(ContainElement(kubeletConfigFinalizer))
	})

	// The following tests drive the full Reconcile() path to verify finalizer and
	// kubelet ConfigMap behavior end-to-end, rather than calling sub-functions directly.

	fullHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
				InfraID:      "test-infra",
				ClusterID:    "test-cluster-id",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						Region: "us-east-1",
					},
				},
				Networking: hyperv1.ClusterNetworking{
					ServiceNetwork: []hyperv1.ServiceNetworkEntry{
						{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
					},
				},
				PullSecret: corev1.LocalObjectReference{
					Name: "pull-secret",
				},
				AutoNode: hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: hyperv1.KarpenterConfig{
							Platform: hyperv1.AWSPlatform,
						},
					},
				},
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Version: "4.17.0",
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "4.17.0",
					},
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							Version:        "4.17.0",
							Image:          "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
							CompletionTime: &metav1.Time{Time: time.Now()},
						},
					},
				},
				KubeConfig: &hyperv1.KubeconfigSecretRef{
					Name: "admin-kubeconfig",
				},
			},
		}
	}

	fullManagementObjects := func(hcp *hyperv1.HostedControlPlane, extra ...client.Object) []client.Object {
		objs := []client.Object{
			hcp,
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: testNamespace},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: testNamespace},
				Data: map[string][]byte{
					"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://api.test-cluster.example.com:6443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
    namespace: default
  name: cluster
current-context: cluster
kind: Config`),
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "core-ignition-config-1", Namespace: testNamespace,
					Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "core-ignition-config-2", Namespace: testNamespace,
					Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"},
				},
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "set-karpenter-taint", Namespace: testNamespace,
				},
				Data: map[string]string{"config": ""},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "ignition-server-ca-cert", Namespace: testNamespace},
				Data:       map[string][]byte{"tls.crt": []byte("fake-ca-cert")},
			},
		}
		return append(objs, extra...)
	}

	fullReconciler := func(t *testing.T, hcp *hyperv1.HostedControlPlane, nodeClass *unstructured.Unstructured, extraMgmt ...client.Object) (*KarpenterIgnitionReconciler, client.Client) {
		t.Helper()
		mockCtrl := gomock.NewController(t)
		mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
		mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

		fakeImgProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
			Result: &dockerv1client.DockerImageConfig{
				Config: &docker10.DockerConfig{
					Labels: map[string]string{
						"io.openshift.hypershift.control-plane-operator-skips-haproxy": "true",
					},
				},
			},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(fullManagementObjects(hcp, extraMgmt...)...).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(nodeClass).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:        fakeManagementClient,
			GuestClient:             fakeGuestClient,
			NodeClassGVK:            nodeClass.GroupVersionKind(),
			ReleaseProvider:         mockedReleaseProvider,
			VersionResolver:         &fakeVersionResolver{},
			ImageMetadataProvider:   fakeImgProvider,
			HypershiftOperatorImage: "test-hypershift-operator-image",
			IgnitionEndpoint:        testIgnitionEndpoint,
			Namespace:               testNamespace,
			CreateOrUpdateProvider:  upsert.New(false),
		}
		return r, fakeGuestClient
	}

	for name, tc := range nodeClassGVKTestCases {
		t.Run(name, func(t *testing.T) {
			t.Run("When Reconcile runs with spec.kubelet set, it should add the finalizer", func(t *testing.T) {
				g := NewWithT(t)

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName(testNodeClassName)
				nodeClass.Object["status"] = map[string]interface{}{}
				nodeClass.Object["spec"] = map[string]interface{}{
					"kubelet": map[string]interface{}{"maxPods": int64(500)},
				}

				r, fakeGuestClient := fullReconciler(t, fullHCP(), nodeClass)
				ctx := log.IntoContext(t.Context(), testr.New(t))

				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: testNodeClassName},
				})
				g.Expect(err).NotTo(HaveOccurred())

				updated := newNodeClass(tc.gvk).object
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updated.GetFinalizers()).To(ContainElement(kubeletConfigFinalizer))

				cm := &corev1.ConfigMap{}
				err = r.ManagementClient.Get(ctx, client.ObjectKey{
					Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
					Namespace: testNamespace,
				}, cm)
				g.Expect(err).NotTo(HaveOccurred())
			})

			t.Run("When Reconcile runs with spec.kubelet cleared, it should remove the finalizer and delete the ConfigMap", func(t *testing.T) {
				g := NewWithT(t)

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName(testNodeClassName)
				nodeClass.SetFinalizers([]string{kubeletConfigFinalizer})
				nodeClass.Object["status"] = map[string]interface{}{}

				existingCM := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
						Namespace: testNamespace,
					},
					Data: map[string]string{"config": "old-data"},
				}

				r, fakeGuestClient := fullReconciler(t, fullHCP(), nodeClass, existingCM)
				ctx := log.IntoContext(t.Context(), testr.New(t))

				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: testNodeClassName},
				})
				g.Expect(err).NotTo(HaveOccurred())

				updated := newNodeClass(tc.gvk).object
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updated.GetFinalizers()).NotTo(ContainElement(kubeletConfigFinalizer))

				cm := &corev1.ConfigMap{}
				err = r.ManagementClient.Get(ctx, client.ObjectKey{
					Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
					Namespace: testNamespace,
				}, cm)
				g.Expect(err).To(HaveOccurred(), "ConfigMap should be deleted")
			})

			t.Run("When Reconcile runs with arbitrary kubelet fields, it should preserve them in the ConfigMap", func(t *testing.T) {
				g := NewWithT(t)

				kubeletJSON := `{"maxPods": 500, "registryPullQPS": 5, "containerLogMaxSize": "50Mi"}`
				var kubeletCfg map[string]interface{}
				g.Expect(json.Unmarshal([]byte(kubeletJSON), &kubeletCfg)).To(Succeed())

				nodeClass := newNodeClass(tc.gvk).object
				nodeClass.SetName(testNodeClassName)
				nodeClass.Object["status"] = map[string]interface{}{}
				nodeClass.Object["spec"] = map[string]interface{}{"kubelet": kubeletCfg}

				r, fakeGuestClient := fullReconciler(t, fullHCP(), nodeClass)
				ctx := log.IntoContext(t.Context(), testr.New(t))

				_, err := r.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{Name: testNodeClassName},
				})
				g.Expect(err).NotTo(HaveOccurred())

				// Verify finalizer was added
				updated := newNodeClass(tc.gvk).object
				err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updated.GetFinalizers()).To(ContainElement(kubeletConfigFinalizer))

				// Verify ConfigMap content includes all supplied kubelet fields.
				cm := &corev1.ConfigMap{}
				err = r.ManagementClient.Get(ctx, client.ObjectKey{
					Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
					Namespace: testNamespace,
				}, cm)
				g.Expect(err).NotTo(HaveOccurred())

				var cr map[string]interface{}
				err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
				g.Expect(err).NotTo(HaveOccurred())
				spec, ok := cr["spec"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())
				kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
				g.Expect(ok).To(BeTrue())

				g.Expect(kubeletConfig["maxPods"]).To(BeEquivalentTo(500))
				g.Expect(kubeletConfig["registryPullQPS"]).To(BeEquivalentTo(5))
				g.Expect(kubeletConfig["containerLogMaxSize"]).To(Equal("50Mi"))

				taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
				g.Expect(ok).To(BeTrue())
				g.Expect(taints).NotTo(BeEmpty())
			})
		})
	}

	t.Run("When NodeClass is deleted with finalizer it should delete the ConfigMap and remove the finalizer", func(t *testing.T) {
		g := NewWithT(t)

		// Use a sentinel finalizer alongside ours so the fake client doesn't auto-delete
		// the object after reconcileDeletedNodeClass removes our finalizer.
		now := metav1.Now()
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              testNodeClassName,
				Finalizers:        []string{kubeletConfigFinalizer, "other-controller-finalizer"},
				DeletionTimestamp: &now,
			},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: kubeletConfig,
			},
		}
		hcp := baseHCP()

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
				Namespace: testNamespace,
			},
			Data: map[string]string{"config": "existing-data"},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, existingCM).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.reconcileDeletedNodeClass(ctx, hcp, nodeClass)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// ConfigMap should be deleted
		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not found"))

		// Our finalizer should be removed; the sentinel remains so the object still exists
		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).NotTo(ContainElement(kubeletConfigFinalizer))
	})

	t.Run("When NodeClass is deleted without the finalizer it should be a no-op", func(t *testing.T) {
		g := NewWithT(t)

		// Use a sentinel finalizer so the fake client accepts the object with DeletionTimestamp.
		now := metav1.Now()
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              testNodeClassName,
				DeletionTimestamp: &now,
				Finalizers:        []string{"other-controller-finalizer"},
				// No kubeletConfigFinalizer
			},
		}
		hcp := baseHCP()

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
				Namespace: testNamespace,
			},
			Data: map[string]string{"config": "should-remain"},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, existingCM).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.reconcileDeletedNodeClass(ctx, hcp, nodeClass)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// ConfigMap should remain untouched
		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cm.Data["config"]).To(Equal("should-remain"))
	})

	t.Run("When NodeClass is deleted with finalizer but ConfigMap is already absent it should remove the finalizer without error", func(t *testing.T) {
		g := NewWithT(t)

		// Use a sentinel finalizer alongside ours so the fake client doesn't auto-delete
		// the object after reconcileDeletedNodeClass removes our finalizer.
		now := metav1.Now()
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name:              testNodeClassName,
				Finalizers:        []string{kubeletConfigFinalizer, "other-controller-finalizer"},
				DeletionTimestamp: &now,
			},
		}
		hcp := baseHCP()

		// No ConfigMap pre-created
		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			GuestClient:            fakeGuestClient,
			Namespace:              testNamespace,
			CreateOrUpdateProvider: upsert.New(false),
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		result, err := r.reconcileDeletedNodeClass(ctx, hcp, nodeClass)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(Equal(ctrl.Result{}))

		// Our finalizer should be removed even though the ConfigMap was already gone
		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).NotTo(ContainElement(kubeletConfigFinalizer))
	})
}

func TestCreateInMemoryNodePool(t *testing.T) {
	r := &KarpenterIgnitionReconciler{}
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
		},
	}

	t.Run("When kubelet config is nil it should only have taint config ref", func(t *testing.T) {
		g := NewWithT(t)
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNodeClassName,
			},
			// KubeletConfig is nil
		}

		np := r.createInMemoryNodePool(hcp, nodeClass, hcp.Spec.ReleaseImage, false)

		g.Expect(np.Spec.Config).To(HaveLen(1))
		g.Expect(np.Spec.Config[0].Name).To(Equal(karpenterutil.KarpenterTaintConfigMapName))
		g.Expect(np.Name).To(Equal(karpenterutil.KarpenterNodePoolName(nodeClass)))
		g.Expect(np.Namespace).To(Equal(hcp.Namespace))
		g.Expect(np.Labels).To(HaveKeyWithValue(karpenterutil.ManagedByKarpenterLabel, "true"))
		g.Expect(np.Spec.ClusterName).To(Equal(hcp.Name))
		g.Expect(np.Spec.Replicas).To(Equal(ptr.To[int32](0)))
		g.Expect(np.Spec.Release.Image).To(Equal(hcp.Spec.ReleaseImage))
		g.Expect(np.Spec.Arch).To(Equal(hyperv1.ArchitectureAMD64))
	})

	t.Run("When kubelet config is set it should include only the per-nodeclass kubelet config ref", func(t *testing.T) {
		g := NewWithT(t)
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNodeClassName,
			},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods: 500,
				},
			},
		}

		np := r.createInMemoryNodePool(hcp, nodeClass, hcp.Spec.ReleaseImage, true)

		// When Spec.Kubelet is set, only the per-nodeclass kubelet config ref is included.
		// set-karpenter-taint is omitted because the taint is merged into the per-nodeclass
		// manifest via kubeletConfigManifest to avoid two KubeletConfigs targeting
		// the same MachineConfigPool, which the MCO bootstrap rejects.
		g.Expect(np.Spec.Config).To(HaveLen(1))
		g.Expect(np.Spec.Config[0].Name).To(Equal(karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName)))
		g.Expect(np.Name).To(Equal(karpenterutil.KarpenterNodePoolName(nodeClass)))
		g.Expect(np.Namespace).To(Equal(hcp.Namespace))
		g.Expect(np.Labels).To(HaveKeyWithValue(karpenterutil.ManagedByKarpenterLabel, "true"))
		g.Expect(np.Spec.ClusterName).To(Equal(hcp.Name))
		g.Expect(np.Spec.Replicas).To(Equal(ptr.To[int32](0)))
		g.Expect(np.Spec.Release.Image).To(Equal(hcp.Spec.ReleaseImage))
		g.Expect(np.Spec.Arch).To(Equal(hyperv1.ArchitectureAMD64))
	})
}

func TestReconcileKubeletConfigMap(t *testing.T) {
	t.Run("When kubelet config is nil it should delete the config map", func(t *testing.T) {
		g := NewWithT(t)
		scheme := api.Scheme

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
				Namespace: testNamespace,
			},
			Data: map[string]string{"config": "old-data"},
		}
		fakeManagementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingCM).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
		}
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			// KubeletConfig is nil
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		// ConfigMap should be deleted
		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not found"))
	})

	t.Run("When kubelet config is set it should create config map with manifest including taint", func(t *testing.T) {
		g := NewWithT(t)
		scheme := api.Scheme

		fakeManagementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
		}
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods: 500,
				},
			},
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cm.Name).To(Equal(karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName)))
		g.Expect(cm.Namespace).To(Equal(testNamespace))
		g.Expect(cm.Labels).To(HaveKeyWithValue(karpenterutil.KarpenterNodeClassKubeletConfigLabel, "true"))
		g.Expect(cm.Data).To(HaveKey("config"))
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cr["apiVersion"]).To(Equal("machineconfiguration.openshift.io/v1"))
		g.Expect(cr["kind"]).To(Equal("KubeletConfig"))
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(kubeletConfig["maxPods"]).To(BeEquivalentTo(500))
		// The taint must be merged in so that set-karpenter-taint can be omitted from configRefs
		// without losing the registerWithTaints behavior.
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
	})

	t.Run("When kubelet config has overflow fields they should appear in the config map manifest", func(t *testing.T) {
		g := NewWithT(t)
		scheme := api.Scheme

		fakeManagementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
		}

		// Create KubeletConfiguration with both typed and overflow fields
		kubeletJSON := `{"maxPods": 500, "registryPullQPS": 5, "containerLogMaxSize": "50Mi"}`
		var kubeletCfg hyperkarpenterv1.KubeletConfiguration
		g.Expect(json.Unmarshal([]byte(kubeletJSON), &kubeletCfg)).To(Succeed())

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: kubeletCfg,
			},
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred())

		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())

		// Typed field
		g.Expect(kubeletConfig["maxPods"]).To(BeEquivalentTo(500))
		// Overflow fields must survive the round-trip
		g.Expect(kubeletConfig["registryPullQPS"]).To(BeEquivalentTo(5))
		g.Expect(kubeletConfig["containerLogMaxSize"]).To(Equal("50Mi"))
		// Taint must still be merged
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taints).NotTo(BeEmpty())
	})

	t.Run("When kubelet config is nil and config map does not exist it should not return an error", func(t *testing.T) {
		g := NewWithT(t)
		scheme := api.Scheme

		fakeManagementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
		}
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			// KubeletConfig is nil
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())
	})

	t.Run("When kubelet config is set and config map already exists it should update the config map", func(t *testing.T) {
		g := NewWithT(t)
		scheme := api.Scheme

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
				Namespace: testNamespace,
			},
			Data: map[string]string{"config": "stale-data-maxPods-100"},
		}
		fakeManagementClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingCM).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: testNamespace,
			},
		}
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods: 250,
				},
			},
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		config, err := toNodeClass(t, nodeClass).kubeletConfig()
		g.Expect(err).NotTo(HaveOccurred())
		err = r.reconcileKubeletConfigMap(ctx, hcp, nodeClass, config)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{
			Name:      karpenterutil.KarpenterNodeClassKubeletConfigName(testNodeClassName),
			Namespace: testNamespace,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(kubeletConfig["maxPods"]).To(BeEquivalentTo(250))
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
	})
}

func TestReconcileTaintConfigMap(t *testing.T) {
	scheme := api.Scheme
	namespace := "clusters-test"

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: namespace,
		},
	}

	t.Run("When taint ConfigMap does not exist, it should create it", func(t *testing.T) {
		g := NewWithT(t)
		ctx := log.IntoContext(t.Context(), testr.New(t))

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}

		err := r.reconcileTaintConfigMap(ctx, hcp)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: karpenterutil.KarpenterTaintConfigMapName, Namespace: namespace}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cm.Data).To(HaveKey("config"))
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cr["apiVersion"]).To(Equal("machineconfiguration.openshift.io/v1"))
		g.Expect(cr["kind"]).To(Equal("KubeletConfig"))
		metadata, ok := cr["metadata"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(metadata["name"]).To(Equal(karpenterutil.KarpenterTaintConfigMapName))
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taints).To(HaveLen(len(karpenterutil.KarpenterBaseTaints)))
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
		g.Expect(taint["value"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Value))
		g.Expect(taint["effect"]).To(Equal(string(karpenterutil.KarpenterBaseTaints[0].Effect)))
	})

	t.Run("When taint ConfigMap already exists, it should be idempotent", func(t *testing.T) {
		g := NewWithT(t)
		ctx := log.IntoContext(t.Context(), testr.New(t))

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      karpenterutil.KarpenterTaintConfigMapName,
				Namespace: namespace,
			},
			Data: map[string]string{"config": "old-data"},
		}
		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingCM).Build()
		r := &KarpenterIgnitionReconciler{
			ManagementClient:       fakeManagementClient,
			CreateOrUpdateProvider: upsert.New(false),
		}

		err := r.reconcileTaintConfigMap(ctx, hcp)
		g.Expect(err).NotTo(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeManagementClient.Get(ctx, client.ObjectKey{Name: karpenterutil.KarpenterTaintConfigMapName, Namespace: namespace}, cm)
		g.Expect(err).NotTo(HaveOccurred())
		var cr map[string]interface{}
		err = yaml.Unmarshal([]byte(cm.Data["config"]), &cr)
		g.Expect(err).NotTo(HaveOccurred())
		spec, ok := cr["spec"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		kubeletConfig, ok := spec["kubeletConfig"].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		taints, ok := kubeletConfig["registerWithTaints"].([]interface{})
		g.Expect(ok).To(BeTrue())
		taint, ok := taints[0].(map[string]interface{})
		g.Expect(ok).To(BeTrue())
		g.Expect(taint["key"]).To(Equal(karpenterutil.KarpenterBaseTaints[0].Key))
	})
}

func TestBuildConfigGenerator(t *testing.T) {
	const (
		releasePayloadHAProxyImage = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:282ea1c0ee4f4cfb47b2565de68de7d5bdcca102a9f3fcbd8d7d55d25630d506"
		releaseImagePullSpec       = "quay.io/openshift-release-dev/ocp-release:4.22.19-x86_64"
	)

	testCases := map[string]struct {
		platform             hyperv1.PlatformSpec
		expectedHAProxyImage string
	}{
		"When the HostedCluster uses shared ingress, it should use the shared ingress HAProxy image": {
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Topology: hyperv1.AzureTopologyPublic,
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					},
				},
			},
			expectedHAProxyImage: images.DefaultSharedIngressHAProxyImage,
		},
		"When the HostedCluster does not use shared ingress, it should use the release payload HAProxy image": {
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{Region: "us-east-1"},
			},
			expectedHAProxyImage: releasePayloadHAProxyImage,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			t.Setenv(images.SharedIngressHAProxyEnvVar, "")

			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: "4.22.19"},
					Spec: imagev1.ImageStreamSpec{
						Tags: []imagev1.TagReference{
							{Name: haproxy.HAProxyRouterImageName, From: &corev1.ObjectReference{Name: releasePayloadHAProxyImage}},
							{Name: "hypershift", From: &corev1.ObjectReference{Name: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cpo"}},
						},
					},
				},
			}
			mockCtrl := gomock.NewController(t)
			mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
			mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(releaseImage, nil).AnyTimes()

			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: testNamespace},
				Spec: hyperv1.HostedClusterSpec{
					Release:   hyperv1.Release{Image: releaseImagePullSpec},
					ClusterID: "test-cluster-id",
					Platform:  tc.platform,
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")}},
					},
					PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				},
				Status: hyperv1.HostedClusterStatus{
					KubeConfig: &corev1.LocalObjectReference{Name: "admin-kubeconfig"},
				},
			}
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "karpenter-test", Namespace: testNamespace},
				Spec: hyperv1.NodePoolSpec{
					Release: hyperv1.Release{Image: releaseImagePullSpec},
				},
			}

			pullSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: testNamespace},
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			}
			kubeconfigSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: testNamespace},
				Data: map[string][]byte{"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://api.test-cluster.example.com:6443
  name: cluster
contexts:
- context:
    cluster: cluster
  name: cluster
current-context: cluster
kind: Config`)},
			}
			sharedIngressService := sharedingress.RouterPublicService()
			sharedIngressService.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "10.0.0.100"}}
			coreConfigs := []client.Object{}
			for _, name := range []string{"core-ignition-config-1", "core-ignition-config-2"} {
				coreConfigs = append(coreConfigs, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: testNamespace,
						Labels:    map[string]string{"hypershift.openshift.io/core-ignition-config": "true"},
					},
				})
			}

			fakeManagementClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(append(coreConfigs, pullSecret, kubeconfigSecret, sharedIngressService)...).
				WithStatusSubresource(sharedIngressService).
				Build()

			r := &KarpenterIgnitionReconciler{
				ManagementClient: fakeManagementClient,
				ReleaseProvider:  mockedReleaseProvider,
				// The CPO skips HAProxy config generation, so the ignition config generated here includes it.
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{
					Labels: map[string]string{haproxy.ControlPlaneOperatorSkipsHAProxyConfigGenerationLabel: "true"},
				}}},
				HypershiftOperatorImage: "test-hypershift-operator-image",
			}

			ctx := log.IntoContext(t.Context(), testr.New(t))
			cg, err := r.buildConfigGenerator(ctx, hostedCluster, nodePool, testNamespace)
			g.Expect(err).NotTo(HaveOccurred())

			compressedConfig, err := cg.CompressedAndEncoded()
			g.Expect(err).NotTo(HaveOccurred())
			config, err := supportutil.DecodeAndDecompress(compressedConfig.Bytes())
			g.Expect(err).NotTo(HaveOccurred())

			var kubeAPIServerProxyManifest string
			for _, manifest := range strings.Split(config.String(), "\n---\n") {
				machineConfig := &mcfgv1.MachineConfig{}
				g.Expect(yaml.Unmarshal([]byte(manifest), machineConfig)).To(Succeed())
				ignitionConfig := &ignitionapi.Config{}
				g.Expect(yaml.Unmarshal(machineConfig.Spec.Config.Raw, ignitionConfig)).To(Succeed())
				for _, file := range ignitionConfig.Storage.Files {
					if file.Path == "/etc/kubernetes/manifests/kube-apiserver-proxy.yaml" {
						contents, err := dataurl.DecodeString(*file.Contents.Source)
						g.Expect(err).NotTo(HaveOccurred())
						kubeAPIServerProxyManifest = string(contents.Data)
					}
				}
			}
			g.Expect(kubeAPIServerProxyManifest).To(ContainSubstring("image: " + tc.expectedHAProxyImage))
		})
	}
}
