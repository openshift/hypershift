package kas

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"

	corev1 "k8s.io/api/core/v1"
)

func TestUpdateMainContainerNoProxy(t *testing.T) {
	testCases := []struct {
		name           string
		clusterNetwork []hyperv1.ClusterNetworkEntry
		serviceNetwork []hyperv1.ServiceNetworkEntry
		machineNetwork []hyperv1.MachineNetworkEntry
		expectedCIDRs  []string
	}{
		{
			name:           "When proxy is configured with machineNetwork it should include cluster service machine and kube-apiserver in NO_PROXY",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")}},
			serviceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.30.0.0/16")}},
			machineNetwork: []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
			expectedCIDRs:  []string{"10.128.0.0/14", "172.30.0.0/16", "192.168.1.0/24", "kube-apiserver"},
		},
		{
			name:           "When proxy is configured without machineNetwork it should include cluster and service in NO_PROXY",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")}},
			serviceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.30.0.0/16")}},
			expectedCIDRs:  []string{"10.128.0.0/14", "172.30.0.0/16", "kube-apiserver"},
		},
		{
			name:           "When proxy is configured with dual-stack machineNetwork it should include both IPv4 and IPv6 CIDRs in NO_PROXY",
			clusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")}},
			serviceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.30.0.0/16")}},
			machineNetwork: []hyperv1.MachineNetworkEntry{
				{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")},
				{CIDR: *ipnet.MustParseCIDR("fd00::/48")},
			},
			expectedCIDRs: []string{"10.128.0.0/14", "172.30.0.0/16", "192.168.1.0/24", "fd00::/48", "kube-apiserver"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HTTP_PROXY", "http://proxy.example.com:3128")
			t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
			t.Setenv("NO_PROXY", "")

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: tc.clusterNetwork,
						ServiceNetwork: tc.serviceNetwork,
						MachineNetwork: tc.machineNetwork,
					},
				},
			}

			podSpec := &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: ComponentName,
						Ports: []corev1.ContainerPort{
							{ContainerPort: 6443},
						},
					},
				},
			}

			updateMainContainer(podSpec, hcp)

			g := NewWithT(t)
			var noProxyValue string
			for _, env := range podSpec.Containers[0].Env {
				if env.Name == "NO_PROXY" {
					noProxyValue = env.Value
					break
				}
			}
			g.Expect(noProxyValue).ToNot(BeEmpty(), "NO_PROXY env var should be set when proxy is configured")
			for _, cidr := range tc.expectedCIDRs {
				g.Expect(noProxyValue).To(ContainSubstring(cidr), "NO_PROXY should contain %s", cidr)
			}
		})
	}
}

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
