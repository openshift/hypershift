package controlplanecomponent

import (
	"path"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

func TestInjectContainer(t *testing.T) {
	baseOpts := TokenMinterContainerOptions{
		TokenType:               CloudToken,
		ServiceAccountName:      "test-sa",
		ServiceAccountNameSpace: "test-ns",
	}

	baseContainer := corev1.Container{
		Name:  "cloud-token-minter",
		Image: "test-image",
	}

	basePodSpec := func() *corev1.PodSpec {
		return &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main"},
			},
		}
	}

	t.Run("When native sidecars are enabled it should inject as init container with RestartPolicy Always", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := basePodSpec()
		baseOpts.injectContainer(true, podSpec, baseContainer, cloudTokenFileMountPath, "cloud-token")

		g.Expect(podSpec.InitContainers).To(HaveLen(1))
		g.Expect(podSpec.Containers).To(HaveLen(1), "should not add to regular containers")

		initContainer := podSpec.InitContainers[0]
		g.Expect(initContainer.Name).To(Equal("cloud-token-minter"))
		g.Expect(initContainer.RestartPolicy).ToNot(BeNil())
		g.Expect(*initContainer.RestartPolicy).To(Equal(corev1.ContainerRestartPolicyAlways))
	})

	t.Run("When native sidecars are enabled it should set a startup probe that checks the token file", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := basePodSpec()
		baseOpts.injectContainer(true, podSpec, baseContainer, cloudTokenFileMountPath, "cloud-token")

		initContainer := podSpec.InitContainers[0]
		g.Expect(initContainer.StartupProbe).ToNot(BeNil())
		g.Expect(initContainer.StartupProbe.Exec).ToNot(BeNil())
		g.Expect(initContainer.StartupProbe.Exec.Command).To(Equal(
			[]string{"test", "-f", path.Join(cloudTokenFileMountPath, "token")},
		))
		g.Expect(initContainer.StartupProbe.PeriodSeconds).To(Equal(int32(1)))
		g.Expect(initContainer.StartupProbe.FailureThreshold).To(Equal(int32(30)))
	})

	t.Run("When native sidecars are enabled with KubeAPIServerToken it should use cloudTokenFileMountPath for the startup probe", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := basePodSpec()
		baseOpts.injectContainer(true, podSpec, baseContainer, kubeAPITokenFileMountPath, "apiserver-token")

		initContainer := podSpec.InitContainers[0]
		g.Expect(initContainer.StartupProbe.Exec.Command).To(Equal(
			[]string{"test", "-f", path.Join(cloudTokenFileMountPath, "token")},
		), "probe must check the token-minter's own mount path, not the main container's")
	})

	t.Run("When native sidecars are disabled it should inject as regular sidecar container", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := basePodSpec()
		baseOpts.injectContainer(false, podSpec, baseContainer, cloudTokenFileMountPath, "cloud-token")

		g.Expect(podSpec.InitContainers).To(BeEmpty())
		g.Expect(podSpec.Containers).To(HaveLen(2))

		sidecar := podSpec.Containers[1]
		g.Expect(sidecar.Name).To(Equal("cloud-token-minter"))
		g.Expect(sidecar.RestartPolicy).To(BeNil())
		g.Expect(sidecar.StartupProbe).To(BeNil())
	})

	t.Run("When OneShot is true it should inject as regular init container without restart policy or probe", func(t *testing.T) {
		g := NewGomegaWithT(t)

		oneShotOpts := TokenMinterContainerOptions{
			TokenType:               CloudToken,
			ServiceAccountName:      "test-sa",
			ServiceAccountNameSpace: "test-ns",
			OneShot:                 true,
		}

		for _, nativeSidecars := range []bool{true, false} {
			podSpec := basePodSpec()
			oneShotOpts.injectContainer(nativeSidecars, podSpec, baseContainer, cloudTokenFileMountPath, "cloud-token")

			g.Expect(podSpec.InitContainers).To(HaveLen(1), "oneshot minters should be injected as init containers")
			g.Expect(podSpec.Containers).To(HaveLen(1), "oneshot minters should not be added to regular containers")
			g.Expect(podSpec.InitContainers[0].RestartPolicy).To(BeNil())
			g.Expect(podSpec.InitContainers[0].StartupProbe).To(BeNil())
		}
	})

	t.Run("When injecting it should always add volume mount to the main container", func(t *testing.T) {
		g := NewGomegaWithT(t)

		for _, nativeSidecars := range []bool{true, false} {
			podSpec := basePodSpec()
			baseOpts.injectContainer(nativeSidecars, podSpec, baseContainer, cloudTokenFileMountPath, "cloud-token")

			mainContainer := podSpec.Containers[0]
			g.Expect(mainContainer.VolumeMounts).To(ContainElement(corev1.VolumeMount{
				Name:      "cloud-token",
				MountPath: cloudTokenFileMountPath,
			}))
		}
	})
}

func TestInjectTokenMinterContainer(t *testing.T) {
	opts := TokenMinterContainerOptions{
		TokenType:               CloudAndAPIServerToken,
		ServiceAccountName:      "test-sa",
		ServiceAccountNameSpace: "test-ns",
	}

	fakeImageProvider := &fakeReleaseImageProvider{images: map[string]string{"token-minter": "test-image:latest"}}

	t.Run("When CloudAndAPIServerToken on AWS with native sidecars it should inject two init containers", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main"},
			},
		}

		cpContext := ControlPlaneContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			ReleaseImageProvider:           fakeImageProvider,
			NativeSidecarContainersEnabled: true,
		}

		opts.injectTokenMinterContainer(cpContext, podSpec)

		g.Expect(podSpec.InitContainers).To(HaveLen(2))
		g.Expect(podSpec.InitContainers[0].Name).To(Equal("cloud-token-minter"))
		g.Expect(podSpec.InitContainers[1].Name).To(Equal("apiserver-token-minter"))
		g.Expect(podSpec.Containers).To(HaveLen(1), "should not add token-minter to regular containers")
	})

	t.Run("When CloudAndAPIServerToken on AWS without native sidecars it should inject two regular containers", func(t *testing.T) {
		g := NewGomegaWithT(t)

		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main"},
			},
		}

		cpContext := ControlPlaneContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			ReleaseImageProvider:           fakeImageProvider,
			NativeSidecarContainersEnabled: false,
		}

		opts.injectTokenMinterContainer(cpContext, podSpec)

		g.Expect(podSpec.InitContainers).To(BeEmpty())
		g.Expect(podSpec.Containers).To(HaveLen(3))
		g.Expect(podSpec.Containers[1].Name).To(Equal("cloud-token-minter"))
		g.Expect(podSpec.Containers[2].Name).To(Equal("apiserver-token-minter"))
	})

	t.Run("When CloudToken on GCP with native sidecars it should inject one cloud init container", func(t *testing.T) {
		g := NewGomegaWithT(t)

		gcpOpts := TokenMinterContainerOptions{
			TokenType:               CloudToken,
			ServiceAccountName:      "test-sa",
			ServiceAccountNameSpace: "test-ns",
		}

		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main"},
			},
		}

		cpContext := ControlPlaneContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
					},
				},
			},
			ReleaseImageProvider:           fakeImageProvider,
			NativeSidecarContainersEnabled: true,
		}

		gcpOpts.injectTokenMinterContainer(cpContext, podSpec)

		g.Expect(podSpec.InitContainers).To(HaveLen(1))
		g.Expect(podSpec.InitContainers[0].Name).To(Equal("cloud-token-minter"))
		g.Expect(podSpec.Containers).To(HaveLen(1), "should not add token-minter to regular containers")
	})

	t.Run("When KubeAPIServerToken on non-cloud platform with native sidecars it should inject one init container", func(t *testing.T) {
		g := NewGomegaWithT(t)

		apiServerOpts := TokenMinterContainerOptions{
			TokenType:               KubeAPIServerToken,
			ServiceAccountName:      "test-sa",
			ServiceAccountNameSpace: "test-ns",
		}

		podSpec := &corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "main"},
			},
		}

		cpContext := ControlPlaneContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
					},
				},
			},
			ReleaseImageProvider:           fakeImageProvider,
			NativeSidecarContainersEnabled: true,
		}

		apiServerOpts.injectTokenMinterContainer(cpContext, podSpec)

		g.Expect(podSpec.InitContainers).To(HaveLen(1))
		g.Expect(podSpec.InitContainers[0].Name).To(Equal("apiserver-token-minter"))
		g.Expect(podSpec.Containers).To(HaveLen(1), "cloud token should not be injected for non-cloud platform")
	})
}

type fakeReleaseImageProvider struct {
	images map[string]string
}

func (f *fakeReleaseImageProvider) GetImage(name string) string {
	return f.images[name]
}

func (f *fakeReleaseImageProvider) ImageExist(name string) (string, bool) {
	img, ok := f.images[name]
	return img, ok
}

func (f *fakeReleaseImageProvider) Version() string {
	return "4.18.0"
}

func (f *fakeReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return nil, nil
}

func (f *fakeReleaseImageProvider) ComponentImages() map[string]string {
	return f.images
}
