package oapi

import (
	"testing"

	. "github.com/onsi/gomega"
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
		cm               corev1.ConfigMap
		auditConfig      *corev1.ConfigMap
		deploymentConfig config.DeploymentConfig
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
			auditConfig:      manifests.OpenShiftAPIServerAuditConfig(targetNamespace),
			deploymentConfig: config.DeploymentConfig{},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		expectedTermGraceSeconds := oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds
		oapiDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := oapiDeployment.Spec.MinReadySeconds
		tc.auditConfig.Data = map[string]string{"policy.yaml": "test-data"}
		err := ReconcileDeployment(oapiDeployment, nil, ownerRef, &tc.cm, tc.auditConfig, serviceServingCA, tc.deploymentConfig, imageName, "socks5ProxyImage", config.DefaultEtcdURL, util.AvailabilityProberImageName, false, hyperv1.IBMCloudPlatform)
		g.Expect(err).To(BeNil())
		g.Expect(expectedTermGraceSeconds).To(Equal(oapiDeployment.Spec.Template.Spec.TerminationGracePeriodSeconds))
		g.Expect(expectedMinReadySeconds).To(Equal(oapiDeployment.Spec.MinReadySeconds))
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
			params:           OAuthDeploymentParams{},
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
