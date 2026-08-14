package kcm

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		hcp             *hyperv1.HostedControlPlane
		existingObjects []client.Object
		validate        func(*testing.T, *appsv1.Deployment, error)
	}{
		{
			name: "When HCP has basic networking config, it should set cluster and service CIDR args",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--cluster-cidr=10.132.0.0/14"))
				g.Expect(container.Args).To(ContainElement("--service-cluster-ip-range=172.31.0.0/16"))
			},
		},
		{
			name: "When platform is Azure, it should set cloud-provider to external",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--cloud-provider=external"))
			},
		},
		{
			name: "When AllocateNodeCIDRs is enabled, it should set allocate-node-cidrs to true",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
						AllocateNodeCIDRs: ptr.To(hyperv1.AllocateNodeCIDRsEnabled),
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--allocate-node-cidrs=true"))
			},
		},
		{
			name: "When AllocateNodeCIDRs is disabled, it should set allocate-node-cidrs to false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
						AllocateNodeCIDRs: ptr.To(hyperv1.AllocateNodeCIDRsDisabled),
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--allocate-node-cidrs=false"))
			},
		},
		{
			name: "When AllocateNodeCIDRs is nil, it should set allocate-node-cidrs to false",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--allocate-node-cidrs=false"))
			},
		},
		{
			name: "When platform is IBMCloud, it should set node-monitor-grace-period to 55s",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--node-monitor-grace-period=55s"))
			},
		},
		{
			name: "When platform is not IBMCloud, it should set node-monitor-grace-period to 50s",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--node-monitor-grace-period=50s"))
			},
		},
		{
			name: "When TLS security profile is set, it should configure TLS min version and cipher suites",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: &configv1.TLSSecurityProfile{
								Type: configv1.TLSProfileModernType,
							},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())

				tlsProfile := &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileModernType,
				}
				minTLSVersion := config.MinTLSVersion(tlsProfile)
				g.Expect(container.Args).To(ContainElement(fmt.Sprintf("--tls-min-version=%s", minTLSVersion)))

				cipherSuites := config.CipherSuites(tlsProfile)
				if len(cipherSuites) > 0 {
					g.Expect(container.Args).To(ContainElement(fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ","))))
				}
			},
		},
		{
			name: "When disable profiling annotation is set, it should add profiling=false arg",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						hyperv1.DisableProfilingAnnotation: ComponentName,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--profiling=false"))
			},
		},
		{
			name: "When feature gates are configured, it should add feature-gates args",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			existingObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "feature-gate",
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"feature-gate.yaml": `{
							"apiVersion": "config.openshift.io/v1",
							"kind": "FeatureGate",
							"spec": {},
							"status": {
								"featureGates": [
									{
										"version": "4.16",
										"enabled": [{"name": "FeatureGate1"}],
										"disabled": [{"name": "FeatureGate2"}]
									}
								]
							}
						}`,
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(container.Args).To(ContainElement("--feature-gates=FeatureGate1=true"))
				g.Expect(container.Args).To(ContainElement("--feature-gates=FeatureGate2=false"))
			},
		},
		{
			name: "When service serving CA exists, it should add volume and volume mount",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			existingObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "service-serving-ca",
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						"service-ca.crt": "test-ca-data",
					},
				},
			},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				// Check volume is added
				vol := podspec.FindVolume("service-serving-ca", deployment.Spec.Template.Spec.Volumes)
				g.Expect(vol).ToNot(BeNil(), "service-serving-ca volume should be added")
				g.Expect(vol.VolumeSource.ConfigMap).ToNot(BeNil())
				g.Expect(vol.VolumeSource.ConfigMap.Name).To(Equal("service-serving-ca"))

				// Check volume mount is added
				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				mount := podspec.FindVolumeMount("service-serving-ca", container.VolumeMounts)
				g.Expect(mount).ToNot(BeNil(), "service-serving-ca volume mount should be added")
				g.Expect(mount.MountPath).To(Equal("/etc/kubernetes/certs/service-ca"))
			},
		},
		{
			name: "When service serving CA does not exist, it should not add volume or mount",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.132.0.0/14")},
						},
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")},
						},
					},
				},
			},
			existingObjects: []client.Object{},
			validate: func(t *testing.T, deployment *appsv1.Deployment, err error) {
				g := NewWithT(t)
				g.Expect(err).ToNot(HaveOccurred())

				// Check volume is not added
				g.Expect(podspec.FindVolume("service-serving-ca", deployment.Spec.Template.Spec.Volumes)).To(BeNil())

				// Check volume mount is not added
				container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
				g.Expect(container).ToNot(BeNil())
				g.Expect(podspec.FindVolumeMount("service-serving-ca", container.VolumeMounts)).To(BeNil())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			// Make a local copy to avoid mutating the shared tc.existingObjects slice in parallel subtests.
			existingObjects := append([]client.Object(nil), tc.existingObjects...)

			// Add feature-gate configmap if not already in existingObjects
			hasFeatureGateConfigMap := false
			for _, obj := range existingObjects {
				if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Name == "feature-gate" {
					hasFeatureGateConfigMap = true
					break
				}
			}
			if !hasFeatureGateConfigMap {
				existingObjects = append(existingObjects, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "feature-gate",
						Namespace: tc.hcp.Namespace,
					},
					Data: map[string]string{
						"feature-gate.yaml": `{"apiVersion":"config.openshift.io/v1","kind":"FeatureGate","spec":{},"status":{"featureGates":[]}}`,
					},
				})
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingObjects...).Build()

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				Client:  fakeClient,
				HCP:     tc.hcp,
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ComponentName,
					Namespace: tc.hcp.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  ComponentName,
									Image: "test-image:latest",
									Args:  []string{},
								},
							},
						},
					},
				},
			}

			err := adaptDeployment(cpContext, deployment)
			tc.validate(t, deployment, err)
		})
	}
}
