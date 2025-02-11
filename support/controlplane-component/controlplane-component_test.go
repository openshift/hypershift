package controlplanecomponent

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

func NewComponent() ControlPlaneComponent {
	return NewDeploymentComponent(testComponentName, &testComponent{}).
		InjectServiceAccountKubeConfig(
			ServiceAccountKubeConfigOpts{
				Name:      testComponentName,
				Namespace: "sa-namespace",
				MountPath: "/test",
			},
		).
		Build()
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name         string
		workloadType workloadType
	}{
		{
			name:         "when reconciling a Deployment workload it should enforce builtin hypershift opinions",
			workloadType: deploymentWorkloadType,
		},
		// TODO(alberto): add StatefulSet test case.
	}

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	appsv1.AddToScheme(scheme)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cpContext := ControlPlaneContext{
				Context:                  context.Background(),
				CreateOrUpdateProviderV2: upsert.NewV2(false),
				ReleaseImageProvider:     testutil.FakeImageProvider(),
				UserReleaseImageProvider: testutil.FakeImageProvider(),
				ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
					Result:   &dockerv1client.DockerImageConfig{},
					Manifest: fakeimagemetadataprovider.FakeManifest{},
				},
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testComponentNamespace,
					},
				},
				Client: fake.NewClientBuilder().WithScheme(scheme).
					WithObjects(componentsFakeDependencies()...).Build(),
			}

			c := NewComponent()

			err := c.Reconcile(cpContext)
			g.Expect(err).NotTo(HaveOccurred())
			got := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testComponentName,
					Namespace: testComponentNamespace,
				},
			}

			cpContext.Client.Get(context.Background(), client.ObjectKeyFromObject(got), got)
			g.Expect(err).NotTo(HaveOccurred())

			// builtin reconciliation must pass the following validations:
			// core labels.
			g.Expect(got.Labels).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "control-plane-operator"))

			// enforce image pull policy.
			for _, container := range got.Spec.Template.Spec.Containers {
				g.Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			}

			// honour replicas in the yaml.
			g.Expect(*got.Spec.Replicas).To(Equal(int32(1)))

			// enforce volume permissions.
			for _, volume := range got.Spec.Template.Spec.Volumes {
				if volume.ConfigMap != nil {
					g.Expect(volume.ConfigMap.DefaultMode).To(Equal(ptr.To(int32(420))))
				}
				if volume.Secret != nil {
					g.Expect(volume.Secret.DefaultMode).To(Equal(ptr.To(int32(416))))
				}
			}
			// enforce automount token sa is false.
			g.Expect(*got.Spec.Template.Spec.AutomountServiceAccountToken).To(BeFalse())

			// enforce affinity rules.
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
			g.Expect(got.Spec.Template.Spec.Affinity.NodeAffinity).To(Equal(nodeAffinity), "node affinity does not match")
			g.Expect(got.Spec.Template.Spec.Affinity.PodAffinity).To(Equal(podAffinity), "pod affinity does not match")

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
			for _, v := range got.Spec.Template.Spec.Volumes {
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
			for _, container := range got.Spec.Template.Spec.Containers {
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
			err = cpContext.Client.Get(context.Background(), client.ObjectKeyFromObject(kubeconfigSecret), kubeconfigSecret)
			g.Expect(err).NotTo(HaveOccurred(), "kubeconfig secret does not exist")
		})
	}
}

func componentsFakeDependencies() []client.Object {
	var fakeComponents []client.Object

	// we need this to exist for components to reconcile
	fakeComponentTemplate := &hyperv1.ControlPlaneComponent{
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
			},
		},
	}

	fakeComponents = append(fakeComponents, fakeComponentTemplate)
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: testComponentNamespace},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{}`),
		},
	}

	fakeComponents = append(fakeComponents, pullSecret.DeepCopy())

	return fakeComponents
}
