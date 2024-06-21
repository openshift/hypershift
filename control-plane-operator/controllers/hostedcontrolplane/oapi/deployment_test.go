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
)

// Ensure certain deployment fields do not get set
func TestReconcileOpenshiftAPIServerDeploymentNoChanges(t *testing.T) {
	imageName := "oapiImage"
	targetNamespace := "test"
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"

	testCases := []struct {
		name                  string
		cm                    corev1.ConfigMap
		auditConfig           *corev1.ConfigMap
		deploymentConfig      config.DeploymentConfig
		additionalTrustBundle *corev1.LocalObjectReference
		clusterConf           *hyperv1.ClusterConfiguration
	}{
		{
			name: "Empty deployment config",
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
		ownerRef := config.OwnerRefFrom(hcp)
		serviceServingCA := manifests.ServiceServingCA(hcp.Namespace)
		oapiDeployment := manifests.OpenShiftAPIServerDeployment(targetNamespace)
		expectedTermGraceSeconds := oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds
		oapiDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := oapiDeployment.Spec.MinReadySeconds
		tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
		err := ReconcileDeployment(oapiDeployment, nil, ownerRef, &tc.cm, tc.auditConfig, serviceServingCA, tc.deploymentConfig, imageName, "socks5ProxyImage", config.DefaultEtcdURL, util.AvailabilityProberImageName, false, hyperv1.IBMCloudPlatform, tc.additionalTrustBundle, tc.clusterConf)
		g.Expect(err).To(BeNil())
		g.Expect(expectedTermGraceSeconds).To(Equal(oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds))
		g.Expect(expectedMinReadySeconds).To(Equal(oapiDeployment.Spec.MinReadySeconds))
	}
}

func TestReconcileOpenshiftAPIServerDeploymentTrustBundle(t *testing.T) {
	var (
		imageName       = "oapiImage"
		targetNamespace = "test"
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
		volumeProjection = []corev1.VolumeProjection{}
	)
	hcp.Name = "name"
	hcp.Namespace = "namespace"
	testCases := []struct {
		name                         string
		cm                           corev1.ConfigMap
		expectedVolume               *corev1.Volume
		auditConfig                  *corev1.ConfigMap
		expectedVolumeProjection     []corev1.VolumeProjection
		deploymentConfig             config.DeploymentConfig
		additionalTrustBundle        *corev1.LocalObjectReference
		clusterConf                  *hyperv1.ClusterConfiguration
		expectProjectedVolumeMounted bool
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
						Sources: append(volumeProjection, getFakeVolumeProjectionCABundle()),
					},
				},
			},
			clusterConf:                  nil,
			expectProjectedVolumeMounted: true,
		},
		{
			name:                         "Trust bundle not provided",
			auditConfig:                  manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig:             config.DeploymentConfig{},
			expectedVolume:               nil,
			additionalTrustBundle:        nil,
			clusterConf:                  nil,
			expectProjectedVolumeMounted: false,
		},
		{
			name:             "Trust bundle and clusterConf provided",
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			expectedVolume: &corev1.Volume{
				Name: "additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: append(volumeProjection, getFakeVolumeProjectionCABundle(), getFakeVolumeProjectionClusterConf()),
					},
				},
			},
			clusterConf: &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{
						Name: "sample-cluster-conf-image-ca",
					},
				},
			},
			expectProjectedVolumeMounted: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			oapiDeployment := manifests.OpenShiftAPIServerDeployment(targetNamespace)
			ownerRef := config.OwnerRefFrom(hcp)
			tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
			err := ReconcileDeployment(oapiDeployment, nil, ownerRef, testOapiCM, tc.auditConfig, nil, tc.deploymentConfig, imageName, "socks5ProxyImage", config.DefaultEtcdURL, util.AvailabilityProberImageName, false, hyperv1.AgentPlatform, tc.additionalTrustBundle, tc.clusterConf)
			g.Expect(err).To(BeNil())
			if tc.expectProjectedVolumeMounted {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).To(ContainElement(*tc.expectedVolume))
			} else {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).NotTo(ContainElement(&corev1.Volume{Name: "additional-trust-bundle"}))
			}
		})
	}
}

func TestReconcileOpenshiftOAuthAPIServerDeployment(t *testing.T) {
	// Setup expected values that are universal

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"

	testCases := []struct {
		name             string
		deploymentConfig config.DeploymentConfig
		auditConfig      *corev1.ConfigMap
		params           OAuthDeploymentParams
	}{
		//
		{
			name:             "Empty deployment config and oauth params",
			deploymentConfig: config.DeploymentConfig{},
			auditConfig:      manifests.OpenShiftOAuthAPIServerAuditConfig(targetNamespace),
			params:           OAuthDeploymentParams{},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		oauthAPIDeployment := manifests.OpenShiftOAuthAPIServerDeployment(targetNamespace)
		ownerRef := config.OwnerRefFrom(hcp)
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

func getFakeVolumeProjectionClusterConf() corev1.VolumeProjection {
	return corev1.VolumeProjection{
		ConfigMap: &corev1.ConfigMapProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "sample-cluster-conf-image-ca",
			},
		},
	}
}
