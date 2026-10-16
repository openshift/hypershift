package oapi

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	tests := []struct {
		name           string
		managementType hyperv1.EtcdManagementType
		disablePKI     bool
		platform       hyperv1.PlatformType
	}{
		{
			name:           "When etcd is managed and PKI reconciliation is enabled, it should retain the managed CA ConfigMap",
			managementType: hyperv1.Managed,
			platform:       hyperv1.NonePlatform,
		},
		{
			name:           "When etcd is managed and PKI reconciliation is disabled, it should retain the managed CA ConfigMap",
			managementType: hyperv1.Managed,
			disablePKI:     true,
			platform:       hyperv1.NonePlatform,
		},
		{
			name:           "When etcd is unmanaged and PKI reconciliation is enabled, it should mount the client Secret CA",
			managementType: hyperv1.Unmanaged,
			platform:       hyperv1.NonePlatform,
		},
		{
			name:           "When etcd is unmanaged and PKI reconciliation is disabled, it should mount the client Secret CA",
			managementType: hyperv1.Unmanaged,
			disablePKI:     true,
			platform:       hyperv1.NonePlatform,
		},
		{
			name:           "When IBMCloud etcd is unmanaged and PKI reconciliation is disabled, it should mount the client Secret CA",
			managementType: hyperv1.Unmanaged,
			disablePKI:     true,
			platform:       hyperv1.IBMCloudPlatform,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{Type: tt.platform},
					Etcd:     hyperv1.EtcdSpec{ManagementType: tt.managementType},
				},
			}
			if tt.managementType == hyperv1.Unmanaged {
				hcp.Spec.Etcd.Unmanaged = &hyperv1.UnmanagedEtcdSpec{
					Endpoint: "https://custom-etcd.example.com:2379",
				}
			}
			if tt.disablePKI {
				hcp.Annotations = map[string]string{hyperv1.DisablePKIReconciliationAnnotation: "true"}
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).NotTo(HaveOccurred())
			podSpec := &deployment.Spec.Template.Spec
			originalCA := podspec.FindVolume("etcd-client-ca", podSpec.Volumes)
			g.Expect(originalCA).NotTo(BeNil())
			originalCA = originalCA.DeepCopy()
			originalClientCert := podspec.FindVolume("etcd-client-cert", podSpec.Volumes)
			g.Expect(originalClientCert).NotTo(BeNil())
			originalClientCert = originalClientCert.DeepCopy()
			originalContainer := podspec.FindContainer(ComponentName, podSpec.Containers)
			g.Expect(originalContainer).NotTo(BeNil())
			originalContainer = originalContainer.DeepCopy()
			originalVolumeCount := len(podSpec.Volumes)

			cpContext := component.WorkloadContext{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP:    hcp,
			}
			g.Expect(adaptDeployment(cpContext, deployment)).To(Succeed())

			ca := podspec.FindVolume("etcd-client-ca", podSpec.Volumes)
			g.Expect(ca).NotTo(BeNil())
			if tt.managementType == hyperv1.Unmanaged {
				g.Expect(ca.VolumeSource).To(Equal(corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: manifests.EtcdClientSecret(hcp.Namespace).Name,
						Items:      []corev1.KeyToPath{{Key: "etcd-client-ca.crt", Path: "ca.crt"}},
					},
				}))
			} else {
				g.Expect(ca).To(Equal(originalCA))
			}
			g.Expect(podSpec.Volumes).To(HaveLen(originalVolumeCount))

			clientCert := podspec.FindVolume("etcd-client-cert", podSpec.Volumes)
			g.Expect(clientCert).To(Equal(originalClientCert))
			container := podspec.FindContainer(ComponentName, podSpec.Containers)
			g.Expect(container).NotTo(BeNil())
			g.Expect(podspec.FindVolumeMount("etcd-client-ca", container.VolumeMounts)).To(Equal(&corev1.VolumeMount{
				Name: "etcd-client-ca", MountPath: "/etc/kubernetes/certs/etcd-client-ca",
			}))
			g.Expect(podspec.FindVolumeMount("etcd-client-cert", container.VolumeMounts)).To(Equal(&corev1.VolumeMount{
				Name: "etcd-client-cert", MountPath: "/etc/kubernetes/certs/etcd-client",
			}))
			g.Expect(container.Args).To(ContainElements(originalContainer.Args))
		})
	}
}

func TestReconcileOpenshiftAPIServerDeploymentTrustBundle(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "test",
		},
	}

	testCases := []struct {
		name                         string
		expectedVolume               *corev1.Volume
		additionalTrustBundle        *corev1.LocalObjectReference
		clusterConf                  *hyperv1.ClusterConfiguration
		imageRegistryAdditionalCAs   *corev1.ConfigMap
		expectProjectedVolumeMounted bool
	}{
		{
			name: "Trust bundle provided",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			expectedVolume: &corev1.Volume{
				Name: "additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources:     []corev1.VolumeProjection{getFakeVolumeProjectionCABundle()},
						DefaultMode: ptr.To[int32](420),
					},
				},
			},
			expectProjectedVolumeMounted: true,
		},
		{
			name:                         "Trust bundle not provided",
			expectedVolume:               nil,
			additionalTrustBundle:        nil,
			expectProjectedVolumeMounted: false,
		},
		{
			name: "Trust bundle and image registry additional CAs provided",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			imageRegistryAdditionalCAs: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "image-registry-additional-ca",
					Namespace: hcp.Namespace,
				},
				Data: map[string]string{
					"registry1": "fake-bundle",
					"registry2": "fake-bundle-2",
				},
			},
			clusterConf: &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{
						Name: "image-registry-additional-ca",
					},
				},
			},
			expectedVolume: &corev1.Volume{
				Name: "additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources:     []corev1.VolumeProjection{getFakeVolumeProjectionCABundle(), getFakeVolumeProjectionImageRegistryCAs()},
						DefaultMode: ptr.To[int32](420),
					},
				},
			},
			expectProjectedVolumeMounted: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.imageRegistryAdditionalCAs != nil {
				fakeClientBuilder.WithObjects(tc.imageRegistryAdditionalCAs)
			}
			hcp.Spec.Configuration = tc.clusterConf
			hcp.Spec.AdditionalTrustBundle = tc.additionalTrustBundle
			cpContext := component.WorkloadContext{
				Client: fakeClientBuilder.Build(),
				HCP:    hcp,
			}

			oapiDeployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, oapiDeployment)
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectProjectedVolumeMounted {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).To(ContainElement(*tc.expectedVolume))
			} else {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).NotTo(ContainElement(&corev1.Volume{Name: "additional-trust-bundle"}))
			}
		})
	}
}

func getFakeVolumeProjectionCABundle() corev1.VolumeProjection {
	return corev1.VolumeProjection{
		ConfigMap: &corev1.ConfigMapProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			Items: []corev1.KeyToPath{
				{
					Key:  "ca-bundle.crt",
					Path: "additional-ca-bundle.pem",
				},
			},
		},
	}
}

func getFakeVolumeProjectionImageRegistryCAs() corev1.VolumeProjection {
	return corev1.VolumeProjection{
		ConfigMap: &corev1.ConfigMapProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "image-registry-additional-ca",
			},
			Items: []corev1.KeyToPath{
				{
					Key:  "registry1",
					Path: "image-registry-1.pem",
				},
				{
					Key:  "registry2",
					Path: "image-registry-2.pem",
				},
			},
		},
	}
}

func TestResolveOAPIVerbosity(t *testing.T) {
	logLevel := func(l hyperv1.LogLevel) hyperv1.OpenShiftAPIServerOperatorSpec {
		return hyperv1.OpenShiftAPIServerOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: l},
		}
	}

	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected int
	}{
		{
			name: "When no operatorConfiguration is set, it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: 2,
		},
		{
			name: "When operatorConfiguration exists but openShiftAPIServer logLevel is nil, it should default to verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
				},
			},
			expected: 2,
		},
		{
			name: "When openShiftAPIServer logLevel is Normal, it should return verbosity 2",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftAPIServer: logLevel(hyperv1.Normal),
					},
				},
			},
			expected: 2,
		},
		{
			name: "When openShiftAPIServer logLevel is Debug, it should return verbosity 4",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftAPIServer: logLevel(hyperv1.Debug),
					},
				},
			},
			expected: 4,
		},
		{
			name: "When openShiftAPIServer logLevel is Trace, it should return verbosity 6",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftAPIServer: logLevel(hyperv1.Trace),
					},
				},
			},
			expected: 6,
		},
		{
			name: "When openShiftAPIServer logLevel is TraceAll, it should return verbosity 8",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftAPIServer: logLevel(hyperv1.TraceAll),
					},
				},
			},
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resolveOAPIVerbosity(tt.hcp)).To(Equal(tt.expected))
		})
	}
}

func TestAdaptDeploymentOAPILogLevel(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected string
	}{
		{
			name: "When no operatorConfiguration is set, it should default to --v=2",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
			},
			expected: "--v=2",
		},
		{
			name: "When logLevel is Debug, it should set --v=4",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						OpenShiftAPIServer: hyperv1.OpenShiftAPIServerOperatorSpec{
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
			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP:    tt.hcp,
			}

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())
			g.Expect(container.Args).To(ContainElement(tt.expected))
		})
	}
}
