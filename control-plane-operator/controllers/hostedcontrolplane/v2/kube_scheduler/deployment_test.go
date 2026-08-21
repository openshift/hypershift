package scheduler

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveSchedulerVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) hyperv1.KubeSchedulerOperatorSpec {
		return hyperv1.KubeSchedulerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: l},
		}
	}

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected int
	}{
		{
			name: "When no operatorConfiguration is set it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 2,
		},
		{
			name: "When operatorConfiguration exists but kubeScheduler logLevel is nil it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expected: 2,
		},
		{
			name: "When kubeScheduler logLevel is Normal it should return verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeScheduler: logLevel(hyperv1.Normal),
					},
				},
			},
			expected: 2,
		},
		{
			name: "When kubeScheduler logLevel is Debug it should return verbosity 4",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeScheduler: logLevel(hyperv1.Debug),
					},
				},
			},
			expected: 4,
		},
		{
			name: "When kubeScheduler logLevel is Trace it should return verbosity 6",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeScheduler: logLevel(hyperv1.Trace),
					},
				},
			},
			expected: 6,
		},
		{
			name: "When kubeScheduler logLevel is TraceAll it should return verbosity 8",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeScheduler: logLevel(hyperv1.TraceAll),
					},
				},
			},
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resolveSchedulerVerbosity(tt.hcp)).To(Equal(tt.expected))
		})
	}
}

func TestAdaptDeploymentSchedulerLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected string
	}{
		{
			name: "When no operatorConfiguration is set it should default to --v=2",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace"},
			},
			expected: "--v=2",
		},
		{
			name: "When logLevel is Debug it should set --v=4",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace"},
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						KubeScheduler: hyperv1.KubeSchedulerOperatorSpec{
							ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: hyperv1.Debug},
						},
					},
				},
			},
			expected: "--v=4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "feature-gate",
						Namespace: tt.hcp.Namespace,
					},
					Data: map[string]string{
						"feature-gate.yaml": `{"apiVersion":"config.openshift.io/v1","kind":"FeatureGate","spec":{},"status":{"featureGates":[]}}`,
					},
				},
			).Build()

			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: ComponentName}},
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				Client:  fakeClient,
				HCP:     tt.hcp,
			}

			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())
			g.Expect(container.Args).To(ContainElement(tt.expected))
		})
	}
}
