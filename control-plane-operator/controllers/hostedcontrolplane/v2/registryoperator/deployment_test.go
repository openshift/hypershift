package registryoperator

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"
)

func TestAdaptDeployment(t *testing.T) {
	for _, tc := range []struct {
		version string
		config  bool
	}{
		{version: "4.21.32-candidate"},
		{version: "4.21.32"},
		{version: "4.22.0-rc.0", config: true},
		{version: "4.22.0", config: true},
		{version: "4.23.0", config: true},
		{version: "5.0.0", config: true},
		{version: "invalid"},
		{version: ""},
	} {
		t.Run(fmt.Sprintf("When the control plane release is %q, it should use supported registry configuration arguments", tc.version), func(t *testing.T) {
			g := NewWithT(t)
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).NotTo(HaveOccurred())
			err = adaptDeployment(component.WorkloadContext{
				HCP:                      &hyperv1.HostedControlPlane{},
				ReleaseImageProvider:     testutil.FakeImageProvider(testutil.WithVersion(tc.version)),
				UserReleaseImageProvider: testutil.FakeImageProvider(testutil.WithVersion("4.23.0")),
			}, deployment)
			if tc.version == "invalid" || tc.version == "" {
				g.Expect(err).To(MatchError(ContainSubstring("failed to parse control plane release version")))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).NotTo(BeNil())
			g.Expect(container.Args).To(HaveLen(2))
			g.Expect(strings.Contains(container.Args[1], "--config=")).To(Equal(tc.config))
			g.Expect(container.Args[1]).To(ContainSubstring(`--files="/etc/secrets/tls.crt"`))
			g.Expect(container.Args[1]).To(ContainSubstring(`--files="/etc/secrets/tls.key"`))
			g.Expect(container.Ports[0].ContainerPort).To(Equal(int32(60000)))
		})
	}
}
