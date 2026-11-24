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
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/gmpmonitoring"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
						Tolerations: []corev1.Toleration{{
							Key:      "custom-key",
							Operator: corev1.TolerationOpEqual,
							Value:    "custom-value",
							Effect:   corev1.TaintEffectNoSchedule,
						}},
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

			g.Expect(result.podTemplate.Spec.Tolerations).To(ContainElement(corev1.Toleration{
				Key:      "custom-key",
				Operator: corev1.TolerationOpEqual,
				Value:    "custom-value",
				Effect:   corev1.TaintEffectNoSchedule,
			}))

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
		return nil, fmt.Errorf("failed to generate self signed CA: %w", err)
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

func TestWorkloadContext(t *testing.T) {
	testCases := []struct {
		name                string
		enableGMPMonitoring bool
	}{
		{name: "When GMP monitoring is disabled, it should propagate the disabled value to the workload context"},
		{name: "When GMP monitoring is enabled, it should propagate the enabled value to the workload context", enableGMPMonitoring: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			cp := &ControlPlaneContext{Context: t.Context(), EnableGMPMonitoring: tc.enableGMPMonitoring}
			g.Expect(cp.workloadContext().EnableGMPMonitoring).To(Equal(tc.enableGMPMonitoring))
		})
	}
}

const (
	gmpTestNamespace = "clusters-example"
	gmpTestClusterID = "cluster-123"
)

func newGMPTestContext(t *testing.T, objects ...client.Object) *ControlPlaneContext {
	t.Helper()
	return &ControlPlaneContext{
		Context:       t.Context(),
		ApplyProvider: upsert.NewApplyProvider(false),
		MetricsSet:    metrics.MetricsSetAll,
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: gmpTestNamespace,
				Name:      "example",
				UID:       "hcp-uid",
			},
			Spec: hyperv1.HostedControlPlaneSpec{ClusterID: gmpTestClusterID},
		},
		Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
	}
}

func newKubeAPIServerWorkloadForTest() *controlPlaneWorkload[*appsv1.Deployment] {
	// Only the GMP helpers under test are exercised, so the component options and
	// adapt functions stay unset.
	return &controlPlaneWorkload[*appsv1.Deployment]{
		name:             "kube-apiserver",
		workloadProvider: &deploymentProvider{},
	}
}

func TestReconcileGMPManifests(t *testing.T) {
	t.Run("When reconciling the GMP manifests, it should apply the staged monitors adapted for the live cluster", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t)
		component := newKubeAPIServerWorkloadForTest()

		g.Expect(component.reconcileGMPManifests(*cpContext)).To(Succeed())

		podMonitoring := &gmpmonitoring.PodMonitoring{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: gmpTestNamespace},
		}
		g.Expect(cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(podMonitoring), podMonitoring)).To(Succeed())
		// The staged asset only carries the Telemetry set and no cluster ID; both
		// must come from the runtime adaptation instead. The staged service and
		// endpoint target-label rules must survive it.
		filterRelabeling, err := metrics.ToGMPRelabelingRules(metrics.KASRelabelConfigs(metrics.MetricsSetAll))
		g.Expect(err).ToNot(HaveOccurred())
		expectedRelabeling := []gmpmonitoring.RelabelingRule{
			{TargetLabel: "service", Replacement: "kube-apiserver", Action: "replace"},
			{TargetLabel: "endpoint", Replacement: "client", Action: "replace"},
		}
		expectedRelabeling = append(expectedRelabeling, filterRelabeling...)
		expectedRelabeling = append(expectedRelabeling, gmpmonitoring.RelabelingRule{
			TargetLabel: metrics.GMPClusterIDLabel,
			Replacement: gmpTestClusterID,
			Action:      "replace",
		})
		g.Expect(podMonitoring.Spec.Endpoints).To(HaveLen(1))
		g.Expect(podMonitoring.Spec.Endpoints[0].MetricRelabeling).To(Equal(expectedRelabeling))
		g.Expect(podMonitoring.Spec.TargetLabels.Metadata).To(Equal(&[]string{"container", "pod"}))

		g.Expect(podMonitoring.OwnerReferences).To(HaveLen(1))
		g.Expect(podMonitoring.OwnerReferences[0].Kind).To(Equal("HostedControlPlane"))
		g.Expect(podMonitoring.OwnerReferences[0].Name).To(Equal("example"))
		g.Expect(podMonitoring.OwnerReferences[0].UID).To(BeEquivalentTo("hcp-uid"))
	})

	t.Run("When the CoreOS monitor adapter disables monitoring, it should remove the owned GMP monitor instead of applying it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		ownedPodMonitoring := &gmpmonitoring.PodMonitoring{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "kube-apiserver",
				Namespace:       gmpTestNamespace,
				OwnerReferences: []metav1.OwnerReference{*config.ControllerOwnerRef(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: gmpTestNamespace, Name: "example", UID: "hcp-uid"}})},
			},
		}
		cpContext := newGMPTestContext(t, ownedPodMonitoring)
		cpContext.HCP.Annotations = map[string]string{hyperv1.DisableMonitoringServices: "true"}
		serviceMonitorAdapter := genericAdapter{}
		DisableIfAnnotationExist(hyperv1.DisableMonitoringServices)(&serviceMonitorAdapter)
		component := newKubeAPIServerWorkloadForTest()
		component.manifestsAdapters = map[string]genericAdapter{
			coreOSServiceMonitorManifest: serviceMonitorAdapter,
		}

		g.Expect(component.reconcileGMPManifests(*cpContext)).To(Succeed())

		err := cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(ownedPodMonitoring), &gmpmonitoring.PodMonitoring{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the opted-out GMP monitor should be removed")
	})

	t.Run("When the CoreOS monitor adapter skips its manifest, it should neither apply nor remove the GMP monitor", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t)
		component := newKubeAPIServerWorkloadForTest()
		component.manifestsAdapters = map[string]genericAdapter{
			coreOSServiceMonitorManifest: {skip: true},
		}

		g.Expect(component.reconcileGMPManifests(*cpContext)).To(Succeed())

		err := cpContext.Client.Get(t.Context(), client.ObjectKey{Namespace: gmpTestNamespace, Name: "kube-apiserver"}, &gmpmonitoring.PodMonitoring{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a skipped monitor should not be applied")
	})

	t.Run("When the component has no staged GMP manifests, it should do nothing", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t)
		component := &controlPlaneWorkload[*appsv1.Deployment]{
			name:             "test-component",
			workloadProvider: &deploymentProvider{},
		}

		g.Expect(component.reconcileGMPManifests(*cpContext)).To(Succeed())
	})
}

func TestDeleteGMPManifests(t *testing.T) {
	ownedPodMonitoring := &gmpmonitoring.PodMonitoring{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "kube-apiserver",
			Namespace:       gmpTestNamespace,
			OwnerReferences: []metav1.OwnerReference{*config.ControllerOwnerRef(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: gmpTestNamespace, Name: "example", UID: "hcp-uid"}})},
		},
	}
	// A PodMonitoring a different controller created with the same name must
	// survive: the delete path only owns objects carrying the HCP ownerRef.
	foreignPodMonitoring := &gmpmonitoring.PodMonitoring{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver", Namespace: gmpTestNamespace},
	}

	t.Run("When the HCP owns the staged PodMonitoring, it should delete it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t, ownedPodMonitoring.DeepCopy())
		component := newKubeAPIServerWorkloadForTest()

		g.Expect(component.deleteGMPManifests(*cpContext)).To(Succeed())

		err := cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(ownedPodMonitoring), &gmpmonitoring.PodMonitoring{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the owned PodMonitoring should be deleted")
	})

	t.Run("When the PodMonitoring has no HCP owner reference, it should keep it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t, foreignPodMonitoring.DeepCopy())
		component := newKubeAPIServerWorkloadForTest()

		g.Expect(component.deleteGMPManifests(*cpContext)).To(Succeed())

		g.Expect(cpContext.Client.Get(t.Context(), client.ObjectKeyFromObject(foreignPodMonitoring), &gmpmonitoring.PodMonitoring{})).To(Succeed(),
			"the PodMonitoring without the HCP ownerRef should be kept")
	})

	t.Run("When the client scheme does not register the GMP types, it should do nothing", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cpContext := newGMPTestContext(t)
		cpContext.Client = fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
		component := newKubeAPIServerWorkloadForTest()

		g.Expect(component.deleteGMPManifests(*cpContext)).To(Succeed())
	})
}
