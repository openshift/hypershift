package cco

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name               string
		releaseVersion     string
		userReleaseVersion string
		expectedVersion    string
	}{
		{
			name:               "When the control plane and user payloads match, it should set RELEASE_VERSION to that version",
			releaseVersion:     "4.22.10",
			userReleaseVersion: "4.22.10",
			expectedVersion:    "4.22.10",
		},
		{
			name:               "When the control plane payload is skewed, it should set RELEASE_VERSION from the user payload",
			releaseVersion:     "4.22.0-0.nightly-2026-11-13-121834",
			userReleaseVersion: "4.22.10",
			expectedVersion:    "4.22.10",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "hcp-namespace"},
				},
				ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion(tc.releaseVersion)),
				UserReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion(tc.userReleaseVersion)),
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(adaptDeployment(cpContext, deployment)).To(Succeed())

			container := util.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())
			g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
				Name:  "RELEASE_VERSION",
				Value: tc.expectedVersion,
			}))
		})
	}
}
