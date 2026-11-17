package cvo

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func createTestContext(hcp *hyperv1.HostedControlPlane) component.WorkloadContext {
	pullSecret := common.PullSecret(hcp.Namespace)
	pullSecret.Data = map[string][]byte{
		corev1.DockerConfigJsonKey: []byte(`{"auths":{"test.registry":{"auth":"dGVzdDp0ZXN0"}}}`),
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(pullSecret).
		Build()

	fakeImageProvider := &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
		Digest: "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}

	if hcp.Spec.ReleaseImage == "" {
		hcp.Spec.ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64"
	}

	return component.WorkloadContext{
		Context:               context.Background(),
		Client:                fakeClient,
		HCP:                   hcp,
		ReleaseImageProvider:  testutil.FakeImageProvider(),
		ImageMetadataProvider: fakeImageProvider,
	}
}

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	// The payload staged by the CVO (prepare-payload) and RELEASE_IMAGE must
	// always follow the data plane (spec.release) image, even when
	// controlPlaneReleaseImage points at a different (skewed) payload. The
	// fake metadata provider pins the configured digest onto the resolved
	// reference of whatever image was looked up, so the expected value proves
	// the lookup used the data plane release image.
	t.Run("When the control plane payload is skewed, it should stage the data plane payload", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform:                 hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
				ReleaseImage:             "quay.io/openshift-release-dev/ocp-release:4.22.10-x86_64",
				ControlPlaneReleaseImage: ptr.To("quay.io/csrwng/release:4.22-kas-mask-nightly"),
			},
		}

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())

		cpContext := createTestContext(hcp)

		cvo := &clusterVersionOperator{}
		err = cvo.adaptDeployment(cpContext, deployment)
		g.Expect(err).ToNot(HaveOccurred())

		expectedDataPlaneImage := "quay.io/openshift-release-dev/ocp-release@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

		preparePayload := util.FindContainer("prepare-payload", deployment.Spec.Template.Spec.InitContainers)
		g.Expect(preparePayload).ToNot(BeNil())
		g.Expect(preparePayload.Image).To(Equal(expectedDataPlaneImage))

		container := util.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
		g.Expect(container).ToNot(BeNil())
		g.Expect(container.Env).To(ContainElement(corev1.EnvVar{
			Name:  "RELEASE_IMAGE",
			Value: expectedDataPlaneImage,
		}))
	})
}
