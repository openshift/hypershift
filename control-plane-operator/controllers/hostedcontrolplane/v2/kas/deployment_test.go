package kas

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

func TestAddImagePrePullInitContainers(t *testing.T) {
	testCases := []struct {
		name                  string
		containers            []corev1.Container
		expectedPrePullImages []string
	}{
		{
			name: "When kube-apiserver container exists it should pre-pull only the apiserver image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "bootstrap", Image: "registry.io/controlplane-operator:v1"},
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
			},
			expectedPrePullImages: []string{"registry.io/kube-apiserver:v1"},
		},
		{
			name: "When kube-apiserver container does not exist it should not create pre-pull init containers",
			containers: []corev1.Container{
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
				{Name: "audit-logs", Image: "registry.io/cli:v1"},
			},
			expectedPrePullImages: []string{},
		},
		{
			name:                  "When there are no containers it should have no pre-pull init containers",
			containers:            []corev1.Container{},
			expectedPrePullImages: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			podSpec := &corev1.PodSpec{
				Containers: tc.containers,
			}

			addImagePrePullInitContainers(podSpec)

			// Find pre-pull init containers and their positions
			var prePullInitContainers []corev1.Container
			firstOtherInitContainerIndex := -1
			lastPrePullInitContainerIndex := -1

			for i, initContainer := range podSpec.InitContainers {
				if strings.HasPrefix(initContainer.Name, "pre-pull-image-") {
					prePullInitContainers = append(prePullInitContainers, initContainer)
					lastPrePullInitContainerIndex = i
				} else {
					if firstOtherInitContainerIndex == -1 {
						firstOtherInitContainerIndex = i
					}
				}
			}

			// Validate the expected number of pre-pull init containers
			g.Expect(len(prePullInitContainers)).To(Equal(len(tc.expectedPrePullImages)),
				"unexpected number of pre-pull init containers")

			// Validate that pre-pull init containers use the expected images
			prePullImages := make([]string, 0, len(prePullInitContainers))
			for _, c := range prePullInitContainers {
				prePullImages = append(prePullImages, c.Image)
			}
			g.Expect(prePullImages).To(Equal(tc.expectedPrePullImages),
				"pre-pull init containers have unexpected images")

			// Validate that pre-pull init containers come before other init containers
			if firstOtherInitContainerIndex != -1 && lastPrePullInitContainerIndex != -1 {
				g.Expect(lastPrePullInitContainerIndex).To(BeNumerically("<", firstOtherInitContainerIndex),
					"pre-pull init containers must come before other init containers")
			}
		})
	}
}

func TestApplyAWSCABundleToKMSContainers(t *testing.T) {
	testCases := []struct {
		name              string
		containers        []corev1.Container
		expectActiveWired bool
		expectBackupWired bool
	}{
		{
			name: "When both active and backup KMS containers exist it should wire both",
			containers: []corev1.Container{
				{Name: "kube-apiserver"},
				{Name: "aws-kms-token-minter"},
				{Name: "aws-kms-active"},
				{Name: "aws-kms-backup"},
			},
			expectActiveWired: true,
			expectBackupWired: true,
		},
		{
			name: "When only active KMS container exists it should wire active only",
			containers: []corev1.Container{
				{Name: "kube-apiserver"},
				{Name: "aws-kms-token-minter"},
				{Name: "aws-kms-active"},
			},
			expectActiveWired: true,
			expectBackupWired: false,
		},
		{
			name: "When no KMS containers exist it should not wire any containers",
			containers: []corev1.Container{
				{Name: "kube-apiserver"},
				{Name: "konnectivity-server"},
			},
			expectActiveWired: false,
			expectBackupWired: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			podSpec := &corev1.PodSpec{
				Containers: tc.containers,
			}

			applyAWSCABundleToKMSContainers(podSpec)

			assertContainerWired := func(containerName string, expectWired bool) {
				for _, c := range podSpec.Containers {
					if c.Name != containerName {
						continue
					}
					if expectWired {
						g.Expect(c.VolumeMounts).To(ContainElement(SatisfyAll(
							HaveField("Name", "aws-ca-bundle"),
							HaveField("MountPath", "/etc/pki/ca-trust/extracted/hypershift"),
							HaveField("ReadOnly", true),
						)), "%s should have aws-ca-bundle volume mount", containerName)
						g.Expect(c.Env).To(ContainElement(SatisfyAll(
							HaveField("Name", "AWS_CA_BUNDLE"),
							HaveField("Value", "/etc/pki/ca-trust/extracted/hypershift/combined-ca-bundle.pem"),
						)), "%s should have AWS_CA_BUNDLE env var", containerName)
					} else {
						g.Expect(c.VolumeMounts).ToNot(ContainElement(HaveField("Name", "aws-ca-bundle")),
							"%s should not have aws-ca-bundle volume mount", containerName)
						g.Expect(c.Env).ToNot(ContainElement(HaveField("Name", "AWS_CA_BUNDLE")),
							"%s should not have AWS_CA_BUNDLE env var", containerName)
					}
					return
				}
				if expectWired {
					t.Errorf("container %s not found but expected to be wired", containerName)
				}
			}

			assertContainerWired("aws-kms-active", tc.expectActiveWired)
			assertContainerWired("aws-kms-backup", tc.expectBackupWired)

			// Token minter should never be wired (it doesn't call AWS APIs).
			for _, c := range podSpec.Containers {
				if c.Name == "aws-kms-token-minter" {
					g.Expect(c.Env).ToNot(ContainElement(HaveField("Name", "AWS_CA_BUNDLE")),
						"aws-kms-token-minter should not have AWS_CA_BUNDLE")
				}
			}
		})
	}
}
