package controlplanecomponent

import (
	"context"
	"crypto/x509/pkix"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testComponentName      = "test-component"
	testComponentNamespace = "test-component"
)

type testComponent struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *testComponent) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *testComponent) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *testComponent) NeedsManagementKASAccess() bool {
	return false
}

func newDeploymentComponentForTest() ControlPlaneComponent {
	return NewDeploymentComponent(testComponentName, &testComponent{}).
		InjectServiceAccountKubeConfig(
			ServiceAccountKubeConfigOpts{
				Name:      testComponentName,
				Namespace: "sa-namespace",
				MountPath: "/test",
			},
		).
		WithManifestAdapter(
			"serviceaccount.yaml",
			SetHostedClusterAnnotation(),
		).
		Build()
}

func newStatefulSetComponentForTest() ControlPlaneComponent {
	return NewStatefulSetComponent(testComponentName, &testComponent{}).
		InjectServiceAccountKubeConfig(
			ServiceAccountKubeConfigOpts{
				Name:      testComponentName,
				Namespace: "sa-namespace",
				MountPath: "/test",
			},
		).
		WithManifestAdapter(
			"serviceaccount.yaml",
			SetHostedClusterAnnotation(),
		).
		Build()
}

// workloadResult holds the common fields extracted from a reconciled workload
// so that validation logic can be shared across Deployment and StatefulSet test cases.
type workloadResult struct {
	labels      map[string]string
	podTemplate corev1.PodTemplateSpec
	replicas    int32
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name         string
		newComponent func() ControlPlaneComponent
		getWorkload  func(ctx context.Context, g Gomega, cl client.Client) workloadResult
	}{
		{
			name:         "when reconciling a Deployment workload it should enforce builtin hypershift opinions",
			newComponent: newDeploymentComponentForTest,
			getWorkload: func(ctx context.Context, g Gomega, cl client.Client) workloadResult {
				got := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testComponentName,
						Namespace: testComponentNamespace,
					},
				}
				err := cl.Get(ctx, client.ObjectKeyFromObject(got), got)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.Spec.Replicas).NotTo(BeNil(), "deployment replicas should not be nil")
				return workloadResult{
					labels:      got.Labels,
					podTemplate: got.Spec.Template,
					replicas:    *got.Spec.Replicas,
				}
			},
		},
		{
			name:         "when reconciling a StatefulSet workload it should enforce builtin hypershift opinions",
			newComponent: newStatefulSetComponentForTest,
			getWorkload: func(ctx context.Context, g Gomega, cl client.Client) workloadResult {
				got := &appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testComponentName,
						Namespace: testComponentNamespace,
					},
				}
				err := cl.Get(ctx, client.ObjectKeyFromObject(got), got)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.Spec.Replicas).NotTo(BeNil(), "statefulset replicas should not be nil")
				return workloadResult{
					labels:      got.Labels,
					podTemplate: got.Spec.Template,
					replicas:    *got.Spec.Replicas,
				}
			},
		},
	}

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeObjects, err := componentsFakeObjects()
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := ControlPlaneContext{
				Context:                  t.Context(),
				ApplyProvider:            upsert.NewApplyProvider(false),
				ReleaseImageProvider:     testutil.FakeImageProvider(),
				UserReleaseImageProvider: testutil.FakeImageProvider(),
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result:   &dockerv1client.DockerImageConfig{},
					Manifest: fakeimagemetadataprovider.FakeManifest{},
				},
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testComponentNamespace,
						Annotations: map[string]string{
							"hypershift.openshift.io/cluster": "test-cluster",
						},
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Labels: map[string]string{
							"test-label": "test",
						},
					},
				},
				Client: fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(fakeObjects...).Build(),
			}

			c := tc.newComponent()
			err = c.Reconcile(cpContext)
			g.Expect(err).NotTo(HaveOccurred())

			result := tc.getWorkload(t.Context(), g, cpContext.Client)

			// builtin reconciliation must pass the following validations:
			// core labels.
			g.Expect(result.labels).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "control-plane-operator"))

			// pod template labels
			g.Expect(result.podTemplate.Labels).To(HaveKeyWithValue(hyperv1.ControlPlaneComponentLabel, testComponentName))
			g.Expect(result.podTemplate.Labels).To(HaveKeyWithValue("test-label", "test"))

			// pod template annotations
			g.Expect(result.podTemplate.Annotations).To(HaveKey(hyperv1.ReleaseImageAnnotation))

			// PriorityClassName should be set
			g.Expect(result.podTemplate.Spec.PriorityClassName).ToNot(BeEmpty())

			// enforce image pull policy.
			for _, container := range result.podTemplate.Spec.Containers {
				g.Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			}

			// honor replicas in the yaml.
			g.Expect(result.replicas).To(Equal(int32(1)))

			// enforce volume permissions.
			for _, volume := range result.podTemplate.Spec.Volumes {
				if volume.ConfigMap != nil {
					g.Expect(volume.ConfigMap.DefaultMode).To(HaveValue(BeEquivalentTo(420)))
				}
				if volume.Secret != nil {
					g.Expect(volume.Secret.DefaultMode).To(HaveValue(BeEquivalentTo(416)))
				}
			}
			// enforce automount token sa is false.
			g.Expect(result.podTemplate.Spec.AutomountServiceAccountToken).NotTo(BeNil(), "AutomountServiceAccountToken should be set")
			g.Expect(*result.podTemplate.Spec.AutomountServiceAccountToken).To(BeFalse())

			// enforce affinity rules.
			g.Expect(result.podTemplate.Spec.Affinity).NotTo(BeNil(), "affinity should be set")
			nodeAffinity := &corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
					{
						Weight: 50,
						Preference: corev1.NodeSelectorTerm{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "hypershift.openshift.io/control-plane",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"true"},
								},
							},
						},
					},
					{
						Weight: 100,
						Preference: corev1.NodeSelectorTerm{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "hypershift.openshift.io/cluster",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{testComponentNamespace},
								},
							},
						},
					},
				},
			}
			podAffinity := &corev1.PodAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
					{
						Weight: 100,
						PodAffinityTerm: corev1.PodAffinityTerm{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"hypershift.openshift.io/hosted-control-plane": testComponentNamespace,
								},
							},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			}
			g.Expect(result.podTemplate.Spec.Affinity.NodeAffinity).To(Equal(nodeAffinity), "node affinity does not match")
			g.Expect(result.podTemplate.Spec.Affinity.PodAffinity).To(Equal(podAffinity), "pod affinity does not match")

			// enforce Service Account Kubeconfig volume mounts
			expectedVolume := corev1.Volume{
				Name: "service-account-kubeconfig",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: ptr.To[int32](416),
						SecretName:  testComponentName + "-service-account-kubeconfig",
					},
				},
			}
			foundVolume := false
			for _, v := range result.podTemplate.Spec.Volumes {
				if reflect.DeepEqual(v, expectedVolume) {
					foundVolume = true
					break
				}
			}
			g.Expect(foundVolume).To(BeTrue())

			expectedVolumeMount := corev1.VolumeMount{
				Name:      "service-account-kubeconfig",
				MountPath: "/test",
			}
			found := false
			for _, container := range result.podTemplate.Spec.Containers {
				found = false
				for _, volumeMount := range container.VolumeMounts {
					if volumeMount.Name == expectedVolumeMount.Name &&
						volumeMount.MountPath == expectedVolumeMount.MountPath {
						found = true
						break
					}
				}
				if !found {
					break
				}
			}
			g.Expect(found).To(BeTrue())

			// validate service account kubeconfig secret was created.
			kubeconfigSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testComponentName + "-service-account-kubeconfig",
					Namespace: testComponentNamespace,
				},
			}
			err = cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret)
			g.Expect(err).NotTo(HaveOccurred(), "kubeconfig secret does not exist")

			// validate service account was created and has the expected annotations and pull secret.
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testComponentName,
					Namespace: testComponentNamespace,
				},
			}
			err = cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(sa), sa)
			g.Expect(err).NotTo(HaveOccurred(), "sa does not exist")

			g.Expect(sa.ImagePullSecrets).To(HaveLen(1))
			g.Expect(sa.ImagePullSecrets[0].Name).To(Equal("pull-secret"))
			g.Expect(sa.Annotations).To(HaveKeyWithValue("hypershift.openshift.io/cluster", "test-cluster"))
		})
	}
}

func componentsFakeObjects() ([]client.Object, error) {
	// we need this to exist for components to reconcile
	fakeComponent := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: testComponentNamespace,
		},
		Status: hyperv1.ControlPlaneComponentStatus{
			Version: testutil.FakeImageProvider().Version(),
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	rootCA := manifests.RootCASecret(testComponentNamespace)
	rootCA.Data = map[string][]byte{
		certs.CASignerCertMapKey: []byte("fake"),
	}

	caCfg := certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"ou"}}}
	key, cert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to generate self signed CA: %v", err)
	}
	csrSigner := manifests.CSRSignerCASecret(testComponentNamespace)
	csrSigner.Data = map[string][]byte{
		certs.CASignerCertMapKey: certs.CertToPem(cert),
		certs.CASignerKeyMapKey:  certs.PrivateKeyToPem(key),
	}

	fakeObjects := []client.Object{
		fakeComponent,
		rootCA,
		csrSigner,
	}
	return fakeObjects, nil
}
