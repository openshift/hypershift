package controlplanecomponent

import (
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

func NewComponent() ControlPlaneComponent {
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

func TestReconcile(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "when reconciling a Deployment workload it should enforce builtin hypershift opinions",
		},
		// TODO(alberto): add StatefulSet test case.
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

			c := NewComponent()
			err = c.Reconcile(cpContext)
			g.Expect(err).NotTo(HaveOccurred())

			got := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testComponentName,
					Namespace: testComponentNamespace,
				},
			}

			err = cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(got), got)
			g.Expect(err).NotTo(HaveOccurred())

			// builtin reconciliation must pass the following validations:
			// core labels.
			g.Expect(got.Labels).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "control-plane-operator"))

			// pod template labels
			g.Expect(got.Spec.Template.Labels).To(HaveKeyWithValue(hyperv1.ControlPlaneComponentLabel, testComponentName))
			g.Expect(got.Spec.Template.Labels).To(HaveKeyWithValue("test-label", "test"))

			// pod template annotations
			g.Expect(got.Spec.Template.Annotations).To(HaveKey(hyperv1.ReleaseImageAnnotation))

			// PriorityClassName should be set
			g.Expect(got.Spec.Template.Spec.PriorityClassName).ToNot(BeEmpty())

			// enforce image pull policy.
			for _, container := range got.Spec.Template.Spec.Containers {
				g.Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			}

			// honor replicas in the yaml.
			g.Expect(*got.Spec.Replicas).To(Equal(int32(1)))

			// enforce volume permissions.
			for _, volume := range got.Spec.Template.Spec.Volumes {
				if volume.ConfigMap != nil {
					g.Expect(volume.ConfigMap.DefaultMode).To(HaveValue(BeEquivalentTo(420)))
				}
				if volume.Secret != nil {
					g.Expect(volume.Secret.DefaultMode).To(HaveValue(BeEquivalentTo(416)))
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

type testWrappedDeploymentProvider struct {
	WorkloadProvider[*appsv1.Deployment]
}

func (d *testWrappedDeploymentProvider) LoadManifest(_ string) (*appsv1.Deployment, error) {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "newComponent",
		},
	}, nil
}

func (d *testWrappedDeploymentProvider) Wrap(delegate WorkloadProvider[*appsv1.Deployment]) WorkloadProvider[*appsv1.Deployment] {
	d.WorkloadProvider = delegate
	return d
}

func TestWithWrappedWorkloadProvider(t *testing.T) {
	g := NewWithT(t)

	builder := NewDeploymentComponent(testComponentName, &testComponent{})
	builder.WithWrappedWorkloadProvider(&testWrappedDeploymentProvider{})
	wrappedTarget := builder.Build()

	target, ok := wrappedTarget.(*controlPlaneWorkload[*appsv1.Deployment])
	g.Expect(ok).To(BeTrue())
	loadedManifest, err := target.workloadProvider.LoadManifest("nonExistingManifest")
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(loadedManifest).To(Equal(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "newComponent",
		},
	}))
}

type testControlPlaneWorkloadRenderer[T client.Object] interface {
	Render(cpContext ControlPlaneContext) (T, error)
}

func TestRenderWorkload(t *testing.T) {
	g := NewWithT(t)

	builder := NewDeploymentComponent(testComponentName, &testComponent{})
	builder.WithWrappedWorkloadProvider(&testWrappedDeploymentProvider{})
	wrappedTarget := builder.Build()

	target, ok := wrappedTarget.(testControlPlaneWorkloadRenderer[*appsv1.Deployment])
	g.Expect(ok).To(BeTrue())

	scheme := runtime.NewScheme()
	g.Expect(appsv1.AddToScheme(scheme)).NotTo(HaveOccurred())
	cpContext := ControlPlaneContext{
		Context: t.Context(),
		HCP:     &hyperv1.HostedControlPlane{},
		Client:  fake.NewClientBuilder().WithScheme(scheme).WithObjects().Build(),
	}

	actualDeployment, err := target.Render(cpContext)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(actualDeployment).ToNot(Equal(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "newComponent",
			},
		},
	))
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
