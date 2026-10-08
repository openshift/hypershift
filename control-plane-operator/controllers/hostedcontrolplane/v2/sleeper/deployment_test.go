package sleeper

import (
	"testing"

	. "github.com/onsi/gomega"

	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestDeploymentManifest(t *testing.T) {
	t.Parallel()

	t.Run("When the sleeper manifest is loaded, it should define only the restricted sleeping container", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(deployment.Name).To(Equal(ComponentName))
		g.Expect(deployment.Spec.Replicas).ToNot(BeNil())
		g.Expect(*deployment.Spec.Replicas).To(Equal(int32(1)))
		g.Expect(deployment.Spec.Selector.MatchLabels).To(Equal(map[string]string{"app": ComponentName}))
		g.Expect(deployment.Spec.Template.Labels).To(Equal(map[string]string{"app": ComponentName}))

		podSpec := deployment.Spec.Template.Spec
		g.Expect(podSpec.AutomountServiceAccountToken).ToNot(BeNil())
		g.Expect(*podSpec.AutomountServiceAccountToken).To(BeFalse())
		g.Expect(podSpec.SecurityContext).ToNot(BeNil())
		g.Expect(podSpec.SecurityContext.RunAsNonRoot).ToNot(BeNil())
		g.Expect(*podSpec.SecurityContext.RunAsNonRoot).To(BeTrue())
		g.Expect(podSpec.SecurityContext.SeccompProfile).ToNot(BeNil())
		g.Expect(podSpec.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
		g.Expect(podSpec.InitContainers).To(BeEmpty())
		g.Expect(podSpec.Volumes).To(BeEmpty())
		g.Expect(podSpec.Containers).To(HaveLen(1))

		container := podSpec.Containers[0]
		g.Expect(container.Name).To(Equal("sleeper"))
		g.Expect(container.Image).To(Equal("cli"))
		g.Expect(container.Command).To(Equal([]string{"/bin/bash", "-c", "sleep infinity"}))
		g.Expect(container.Args).To(BeEmpty())
		g.Expect(container.Env).To(BeEmpty())
		g.Expect(container.EnvFrom).To(BeEmpty())
		g.Expect(container.Ports).To(BeEmpty())
		g.Expect(container.VolumeMounts).To(BeEmpty())
		g.Expect(container.LivenessProbe).To(BeNil())
		g.Expect(container.ReadinessProbe).To(BeNil())
		g.Expect(container.StartupProbe).To(BeNil())
		g.Expect(container.Resources.Requests.Cpu().Cmp(resource.MustParse("10m"))).To(Equal(0))
		g.Expect(container.Resources.Requests.Memory().Cmp(resource.MustParse("10Mi"))).To(Equal(0))
		g.Expect(container.Resources.Limits).To(BeEmpty())
		g.Expect(container.SecurityContext).ToNot(BeNil())
		g.Expect(container.SecurityContext.AllowPrivilegeEscalation).ToNot(BeNil())
		g.Expect(*container.SecurityContext.AllowPrivilegeEscalation).To(BeFalse())
		g.Expect(container.SecurityContext.Capabilities).ToNot(BeNil())
		g.Expect(container.SecurityContext.Capabilities.Drop).To(Equal([]corev1.Capability{"ALL"}))
		g.Expect(container.SecurityContext.RunAsNonRoot).ToNot(BeNil())
		g.Expect(*container.SecurityContext.RunAsNonRoot).To(BeTrue())
		g.Expect(container.SecurityContext.SeccompProfile).ToNot(BeNil())
		g.Expect(container.SecurityContext.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
	})
}
