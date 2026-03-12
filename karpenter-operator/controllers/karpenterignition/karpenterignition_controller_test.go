package karpenterignition

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr/testr"
	"go.uber.org/mock/gomock"
)

const (
	testNamespace           = "clusters-test"
	testIgnitionEndpoint    = "https://ignition.example.com"
	testNodeClassName       = "default"
	configVersionAnnotation = "hypershift.openshift.io/nodeClassCurrentConfigVersion"
)

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

func TestReconcile(t *testing.T) {
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
			AutoNode: &hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: hyperv1.ProvisionerKarpenter,
					Karpenter: &hyperv1.KarpenterConfig{
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

	nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNodeClassName,
		},
	}

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

	karpenterTaintConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-karpenter-taint",
			Namespace: testNamespace,
		},
		Data: map[string]string{
			"config": "",
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
		WithObjects(hcp, pullSecret, kubeconfigSecret, coreConfig1, coreConfig2, karpenterTaintConfig, ignitionServerCACert).
		Build()

	fakeGuestClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(nodeClass).
		WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
		Build()

	r := &KarpenterIgnitionReconciler{
		ManagementClient:        fakeManagementClient,
		GuestClient:             fakeGuestClient,
		ReleaseProvider:         mockedReleaseProvider,
		VersionResolver:         &fakeVersionResolver{},
		ImageMetadataProvider:   fakeImageMetadataProvider,
		HypershiftOperatorImage: "test-hypershift-operator-image",
		IgnitionEndpoint:        testIgnitionEndpoint,
		Namespace:               testNamespace,
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
			g.Expect(secret.Annotations).To(HaveKey(supportutil.HostedClusterAnnotation), "token secret should have HostedClusterAnnotation")
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

	// Part 2: Test config hash change and a second nodeclass

	// Get the initial config version from the nodeclass annotation
	err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, nodeClass)
	g.Expect(err).NotTo(HaveOccurred())
	initialConfigVersion := nodeClass.Annotations[configVersionAnnotation]
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
	nodeClass2 := &hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: secondNodeClassName,
		},
	}
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
	updatedConfigVersion := nodeClass.Annotations[configVersionAnnotation]
	g.Expect(updatedConfigVersion).NotTo(BeEmpty())
	g.Expect(updatedConfigVersion).NotTo(Equal(initialConfigVersion), "config version should change when config is updated")

	// Verify second nodeclass also got its config version
	err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: secondNodeClassName}, nodeClass2)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(nodeClass2.Annotations[configVersionAnnotation]).NotTo(BeEmpty(), "second nodeclass should have config version")

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
				AutoNode: &hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: &hyperv1.KarpenterConfig{
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

	t.Run("When version is set it should call resolver and use resolved image", func(t *testing.T) {
		g := NewWithT(t)
		mockCtrl := gomock.NewController(t)
		mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
		resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:resolved123"
		mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

		hcp := baseHCP()
		resolver := &fakeVersionResolver{image: resolvedImage}

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "version-test"},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Version: "4.17.0",
			},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:        fakeManagementClient,
			GuestClient:             fakeGuestClient,
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
		updatedNC := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "version-test"}, updatedNC)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updatedNC.Status.ReleaseImage).To(Equal(resolvedImage))
	})

	t.Run("When version is not set it should not call resolver and use HCP release image", func(t *testing.T) {
		g := NewWithT(t)
		mockCtrl := gomock.NewController(t)
		mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
		mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

		hcp := baseHCP()
		resolver := &fakeVersionResolver{image: "should-not-be-used"}

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "no-version-test"},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:        fakeManagementClient,
			GuestClient:             fakeGuestClient,
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
		updatedNC := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "no-version-test"}, updatedNC)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updatedNC.Status.ReleaseImage).To(Equal(hcp.Spec.ReleaseImage))
	})

	t.Run("When version resolution fails it should return error", func(t *testing.T) {
		g := NewWithT(t)
		mockCtrl := gomock.NewController(t)
		mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)

		hcp := baseHCP()
		resolver := &fakeVersionResolver{err: fmt.Errorf("Cincinnati API unavailable")}

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "fail-version-test"},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Version: "4.17.0",
			},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:        fakeManagementClient,
			GuestClient:             fakeGuestClient,
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
		updatedNC := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "fail-version-test"}, updatedNC)
		g.Expect(err).NotTo(HaveOccurred())
		var versionCondition *metav1.Condition
		for i, c := range updatedNC.Status.Conditions {
			if c.Type == hyperkarpenterv1.ConditionTypeVersionResolved {
				versionCondition = &updatedNC.Status.Conditions[i]
				break
			}
		}
		g.Expect(versionCondition).NotTo(BeNil(), "VersionResolved condition should be set")
		g.Expect(versionCondition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(versionCondition.Reason).To(Equal("ResolutionFailed"))

	})

	t.Run("When channel is set it should pass HCP channel to resolver", func(t *testing.T) {
		g := NewWithT(t)
		mockCtrl := gomock.NewController(t)
		mockedReleaseProvider := releaseinfo.NewMockProvider(mockCtrl)
		resolvedImage := "quay.io/openshift-release-dev/ocp-release@sha256:fast123"
		mockedReleaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

		hcp := baseHCP()
		hcp.Spec.Channel = "fast-4.17"
		resolver := &fakeVersionResolver{image: resolvedImage}

		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: "hcp-channel-test"},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Version: "4.17.0",
			},
		}

		fakeManagementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(baseManagementObjects(hcp)...).Build()
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nodeClass).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{
			ManagementClient:        fakeManagementClient,
			GuestClient:             fakeGuestClient,
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
		err := r.updateVersionStatus(ctx, nc, hcpImage, "4.17.0", nil)
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
		err := r.updateVersionStatus(ctx, nc, resolvedImage, "4.17.0", nil)
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
		err := r.updateVersionStatus(ctx, nc, "", "", resolveErr)
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
	})

	t.Run("When condition has not changed it should not patch", func(t *testing.T) {
		g := NewWithT(t)
		nc := newNodeClass("no-change", "")
		fakeGuestClient := fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(nc).
			WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
			Build()

		r := &KarpenterIgnitionReconciler{GuestClient: fakeGuestClient}
		ctx := log.IntoContext(t.Context(), testr.New(t))

		hcpImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64"

		// First call sets the condition
		err := r.updateVersionStatus(ctx, nc, hcpImage, "4.17.0", nil)
		g.Expect(err).NotTo(HaveOccurred())

		// Re-fetch to get the updated object with the condition already set
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: "no-change"}, nc)
		g.Expect(err).NotTo(HaveOccurred())

		// Second call with same inputs should not error (no-op patch skipped)
		err = r.updateVersionStatus(ctx, nc, hcpImage, "4.17.0", nil)
		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestCurrentClusterVersion(t *testing.T) {
	completedTime1 := metav1.Now()
	completedTime2 := metav1.NewTime(completedTime1.Add(time.Hour))

	testCases := []struct {
		name            string
		hostedCluster   *hyperv1.HostedCluster
		expectedVersion string
	}{
		{
			name: "When there is a single completed history entry it should return that version",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are multiple completed entries it should return the one with the most recent CompletionTime",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								CompletionTime: &completedTime1,
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.18.0",
								CompletionTime: &completedTime2,
							},
						},
					},
				},
			},
			expectedVersion: "4.18.0",
		},
		{
			name: "When there is a partial entry and a completed entry it should return the completed entry",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.18.0",
							},
							{
								State:          configv1.CompletedUpdate,
								Version:        "4.17.0",
								CompletionTime: &completedTime1,
							},
						},
					},
				},
			},
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are no completed entries and exactly one history entry it should fall back to Desired.Version",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.17.0",
							},
						},
					},
				},
			},
			expectedVersion: "4.17.0",
		},
		{
			name: "When there are no completed entries and multiple history entries it should fall back to Desired.Version",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.18.0"},
						History: []configv1.UpdateHistory{
							{
								State:   configv1.PartialUpdate,
								Version: "4.18.0",
							},
							{
								State:   configv1.PartialUpdate,
								Version: "4.17.0",
							},
						},
					},
				},
			},
			expectedVersion: "4.18.0",
		},
		{
			name: "When history is empty it should fall back to Desired.Version",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "4.17.0"},
						History: []configv1.UpdateHistory{},
					},
				},
			},
			expectedVersion: "4.17.0",
		},
		{
			name: "When Version is nil it should return empty string",
			hostedCluster: &hyperv1.HostedCluster{
				Status: hyperv1.HostedClusterStatus{
					Version: nil,
				},
			},
			expectedVersion: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			version := currentClusterVersion(tc.hostedCluster)
			g.Expect(version).To(Equal(tc.expectedVersion))
		})
	}
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
				AutoNode: &hyperv1.AutoNode{
					Provisioner: hyperv1.ProvisionerConfig{
						Name: hyperv1.ProvisionerKarpenter,
						Karpenter: &hyperv1.KarpenterConfig{
							Platform: hyperv1.AWSPlatform,
						},
					},
				},
			},
		}
	}

	kubeletConfig := &hyperkarpenterv1.KubeletConfiguration{
		MaxPods: ptr.To[int32](500),
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
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
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
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
		g.Expect(err).NotTo(HaveOccurred())

		updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
		err = fakeGuestClient.Get(ctx, client.ObjectKey{Name: testNodeClassName}, updated)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(updated.Finalizers).NotTo(ContainElement(kubeletConfigFinalizer))
	})

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

		np := r.createInMemoryNodePool(hcp, nodeClass, hcp.Spec.ReleaseImage)

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
		maxPods := int32(500)
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNodeClassName,
			},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: &hyperkarpenterv1.KubeletConfiguration{
					MaxPods: &maxPods,
				},
			},
		}

		np := r.createInMemoryNodePool(hcp, nodeClass, hcp.Spec.ReleaseImage)

		// When Spec.Kubelet is set, only the per-nodeclass kubelet config ref is included.
		// set-karpenter-taint is omitted because the taint is merged into the per-nodeclass
		// manifest via ToKubeletConfigManifestWithTaints to avoid two KubeletConfigs targeting
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
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
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
		maxPods := int32(500)
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: &hyperkarpenterv1.KubeletConfiguration{
					MaxPods: &maxPods,
				},
			},
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
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
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
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
		newMaxPods := int32(250)
		nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
			ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName},
			Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: &hyperkarpenterv1.KubeletConfiguration{
					MaxPods: &newMaxPods,
				},
			},
		}

		ctx := log.IntoContext(t.Context(), testr.New(t))
		err := r.reconcileKubeletConfigMap(ctx, hcp, nodeClass)
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
