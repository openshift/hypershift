package oapi

import (
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// Ensure certain deployment fields do not get set
func TestReconcileOpenshiftAPIServerDeploymentNoChanges(t *testing.T) {

	imageName := "oapiImage"
	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	oapiDeployment := manifests.OpenShiftAPIServerDeployment(targetNamespace)
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	ownerRef := config.OwnerRefFrom(hcp)
	serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)

	testCases := []struct {
		cm                    corev1.ConfigMap
		auditConfig           *corev1.ConfigMap
		deploymentConfig      config.DeploymentConfig
		additionalTrustBundle *corev1.LocalObjectReference
		clusterConf           *hyperv1.ClusterConfiguration
	}{
		// empty deployment config
		{
			cm: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-oapi-config",
					Namespace: targetNamespace,
				},
				Data: map[string]string{"config.yaml": "test-data"},
			},
			auditConfig:           manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig:      config.DeploymentConfig{},
			additionalTrustBundle: &corev1.LocalObjectReference{Name: "test-trust-bundle"},
			clusterConf:           nil,
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		expectedTermGraceSeconds := oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds
		oapiDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := oapiDeployment.Spec.MinReadySeconds
		tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
		err := ReconcileDeployment(oapiDeployment, nil, ownerRef, &tc.cm, tc.auditConfig, serviceServingCA, tc.deploymentConfig, imageName, "konnectivityProxyImage", config.DefaultEtcdURL, util.AvailabilityProberImageName, false, hyperv1.IBMCloudPlatform, tc.additionalTrustBundle, nil, tc.clusterConf, nil, "")
		g.Expect(err).To(BeNil())
		g.Expect(expectedTermGraceSeconds).To(Equal(oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds))
		g.Expect(expectedMinReadySeconds).To(Equal(oapiDeployment.Spec.MinReadySeconds))
	}
}

func TestReconcileOpenshiftAPIServerDeploymentTrustBundle(t *testing.T) {
	var (
		imageName       = "oapiImage"
		targetNamespace = "test"
		oapiDeployment  = manifests.OpenShiftAPIServerDeployment(targetNamespace)
		hcp             = &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hcp",
				Namespace: targetNamespace,
			},
		}
		testOapiCM = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-oapi-config",
				Namespace: targetNamespace,
			},
			Data: map[string]string{"config.yaml": "test-data"},
		}
	)
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	ownerRef := config.OwnerRefFrom(hcp)
	testCases := []struct {
		name                              string
		cm                                corev1.ConfigMap
		expectedVolume                    *corev1.Volume
		expectedProxyVolume               *corev1.Volume
		auditConfig                       *corev1.ConfigMap
		expectedVolumeProjection          []corev1.VolumeProjection
		deploymentConfig                  config.DeploymentConfig
		additionalTrustBundle             *corev1.LocalObjectReference
		clusterConf                       *hyperv1.ClusterConfiguration
		imageRegistryAdditionalCAs        *corev1.ConfigMap
		expectProjectedVolumeMounted      bool
		expectProjectedProxyVolumeMounted bool
	}{
		{
			name:             "Trust bundle provided",
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
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
			expectProjectedVolumeMounted:      true,
			expectProjectedProxyVolumeMounted: false,
		},
		{
			name:                              "Trust bundle not provided",
			auditConfig:                       manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig:                  config.DeploymentConfig{},
			expectedVolume:                    nil,
			additionalTrustBundle:             nil,
			expectProjectedVolumeMounted:      false,
			expectProjectedProxyVolumeMounted: false,
		},
		{
			name:             "Trust bundle and image registry additional CAs provided",
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			imageRegistryAdditionalCAs: &corev1.ConfigMap{
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
			expectProjectedVolumeMounted:      true,
			expectProjectedProxyVolumeMounted: false,
		},
		{
			name:             "Trust bundle and proxy trust bundle provided",
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
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
			expectedProxyVolume: &corev1.Volume{
				Name: "proxy-additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources:     []corev1.VolumeProjection{getFakeVolumeProjectionCABundle()},
						DefaultMode: ptr.To[int32](420),
					},
				},
			},
			expectProjectedVolumeMounted:      true,
			expectProjectedProxyVolumeMounted: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
			err := ReconcileDeployment(oapiDeployment, nil, ownerRef, testOapiCM, tc.auditConfig, nil, tc.deploymentConfig, imageName, "konnectivityProxyImage", config.DefaultEtcdURL, util.AvailabilityProberImageName, false, hyperv1.AgentPlatform, tc.additionalTrustBundle, tc.imageRegistryAdditionalCAs, tc.clusterConf, nil, "")
			g.Expect(err).To(BeNil())
			if tc.expectProjectedVolumeMounted {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).To(ContainElement(*tc.expectedVolume))
			} else {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).NotTo(ContainElement(&corev1.Volume{Name: "additional-trust-bundle"}))
			}
			if tc.expectProjectedProxyVolumeMounted {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).To(ContainElement(*tc.expectedProxyVolume))
			} else {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).NotTo(ContainElement(&corev1.Volume{Name: "proxy-additional-trust-bundle"}))
			}
		})
	}
}

func TestReconcileOpenshiftOAuthAPIServerDeployment(t *testing.T) {
	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	oauthAPIDeployment := manifests.OpenShiftOAuthAPIServerDeployment(targetNamespace)
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	ownerRef := config.OwnerRefFrom(hcp)

	testCases := []struct {
		deploymentConfig config.DeploymentConfig
		auditConfig      *corev1.ConfigMap
		params           OAuthDeploymentParams
	}{
		// empty deployment config and oauth params
		{
			deploymentConfig: config.DeploymentConfig{},
			auditConfig:      manifests.OpenShiftOAuthAPIServerAuditConfig(targetNamespace),
			params: OAuthDeploymentParams{
				EtcdURL: "https://etcd-client:2379",
			},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		oauthAPIDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := oauthAPIDeployment.Spec.MinReadySeconds
		tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
		err := ReconcileOAuthAPIServerDeployment(oauthAPIDeployment, ownerRef, tc.auditConfig, &tc.params, hyperv1.IBMCloudPlatform)
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(oauthAPIDeployment.Spec.MinReadySeconds))
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
