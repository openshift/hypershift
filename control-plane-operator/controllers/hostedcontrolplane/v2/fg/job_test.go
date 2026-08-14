package fg

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	configv1 "github.com/openshift/api/config/v1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptJob(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		validate func(g Gomega, job *batchv1.Job, err error)
	}{
		{
			name: "When HCP has no feature gate configuration, it should set default environment variables",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: nil,
				},
			},
			validate: func(g Gomega, job *batchv1.Job, err error) {
				g.Expect(err).ToNot(HaveOccurred())

				// Check render-feature-gates init container
				renderContainer := podspec.FindContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers)
				g.Expect(renderContainer).ToNot(BeNil())

				payloadVersionEnv := podspec.FindEnvVar("PAYLOAD_VERSION", renderContainer.Env)
				g.Expect(payloadVersionEnv).ToNot(BeNil())
				g.Expect(payloadVersionEnv.Value).To(Equal(testutil.FakeImageProvider().Version()))

				featureGateEnv := podspec.FindEnvVar("FEATURE_GATE_YAML", renderContainer.Env)
				g.Expect(featureGateEnv).ToNot(BeNil())
				g.Expect(featureGateEnv.Value).To(ContainSubstring("kind: FeatureGate"))
				g.Expect(featureGateEnv.Value).To(ContainSubstring("name: cluster"))

				// Check apply container
				applyContainer := podspec.FindContainer("apply", job.Spec.Template.Spec.Containers)
				g.Expect(applyContainer).ToNot(BeNil())

				applyPayloadVersionEnv := podspec.FindEnvVar("PAYLOAD_VERSION", applyContainer.Env)
				g.Expect(applyPayloadVersionEnv).ToNot(BeNil())
				g.Expect(applyPayloadVersionEnv.Value).To(Equal(testutil.FakeImageProvider().Version()))
			},
		},
		{
			name: "When HCP has feature gate configuration, it should include feature gate spec in YAML",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						FeatureGate: &configv1.FeatureGateSpec{
							FeatureGateSelection: configv1.FeatureGateSelection{
								FeatureSet: configv1.TechPreviewNoUpgrade,
							},
						},
					},
				},
			},
			validate: func(g Gomega, job *batchv1.Job, err error) {
				g.Expect(err).ToNot(HaveOccurred())

				renderContainer := podspec.FindContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers)
				g.Expect(renderContainer).ToNot(BeNil())

				featureGateEnv := podspec.FindEnvVar("FEATURE_GATE_YAML", renderContainer.Env)
				g.Expect(featureGateEnv).ToNot(BeNil())
				g.Expect(featureGateEnv.Value).To(ContainSubstring("featureSet: TechPreviewNoUpgrade"))
			},
		},
		{
			name: "When HCP has custom feature gates, it should include custom feature gates in YAML",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						FeatureGate: &configv1.FeatureGateSpec{
							FeatureGateSelection: configv1.FeatureGateSelection{
								FeatureSet: configv1.CustomNoUpgrade,
								CustomNoUpgrade: &configv1.CustomFeatureGates{
									Enabled: []configv1.FeatureGateName{
										"CustomFeature1",
										"CustomFeature2",
									},
									Disabled: []configv1.FeatureGateName{
										"DisabledFeature1",
									},
								},
							},
						},
					},
				},
			},
			validate: func(g Gomega, job *batchv1.Job, err error) {
				g.Expect(err).ToNot(HaveOccurred())

				renderContainer := podspec.FindContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers)
				g.Expect(renderContainer).ToNot(BeNil())

				featureGateEnv := podspec.FindEnvVar("FEATURE_GATE_YAML", renderContainer.Env)
				g.Expect(featureGateEnv).ToNot(BeNil())
				g.Expect(featureGateEnv.Value).To(ContainSubstring("featureSet: CustomNoUpgrade"))
				g.Expect(featureGateEnv.Value).To(ContainSubstring("CustomFeature1"))
				g.Expect(featureGateEnv.Value).To(ContainSubstring("CustomFeature2"))
				g.Expect(featureGateEnv.Value).To(ContainSubstring("DisabledFeature1"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			job, err := assets.LoadJobManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      tc.hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			err = adaptJob(cpContext, job)
			tc.validate(g, job, err)
		})
	}
}

func TestAdaptJobPreservesExistingEnvVars(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
	}

	job, err := assets.LoadJobManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	// Add existing env vars to containers
	renderContainer := podspec.FindContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers)
	g.Expect(renderContainer).ToNot(BeNil())
	renderContainer.Env = append(renderContainer.Env, corev1.EnvVar{
		Name:  "EXISTING_VAR",
		Value: "existing-value",
	})

	applyContainer := podspec.FindContainer("apply", job.Spec.Template.Spec.Containers)
	g.Expect(applyContainer).ToNot(BeNil())
	applyContainer.Env = append(applyContainer.Env, corev1.EnvVar{
		Name:  "ANOTHER_EXISTING_VAR",
		Value: "another-existing-value",
	})

	cpContext := component.WorkloadContext{
		Context:                  t.Context(),
		HCP:                      hcp,
		UserReleaseImageProvider: testutil.FakeImageProvider(),
	}

	err = adaptJob(cpContext, job)
	g.Expect(err).ToNot(HaveOccurred())

	// Check that existing env vars are preserved
	renderContainer = podspec.FindContainer("render-feature-gates", job.Spec.Template.Spec.InitContainers)
	existingVar := podspec.FindEnvVar("EXISTING_VAR", renderContainer.Env)
	g.Expect(existingVar).ToNot(BeNil())
	g.Expect(existingVar.Value).To(Equal("existing-value"))

	applyContainer = podspec.FindContainer("apply", job.Spec.Template.Spec.Containers)
	anotherExistingVar := podspec.FindEnvVar("ANOTHER_EXISTING_VAR", applyContainer.Env)
	g.Expect(anotherExistingVar).ToNot(BeNil())
	g.Expect(anotherExistingVar.Value).To(Equal("another-existing-value"))
}
