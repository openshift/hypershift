package oauth

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Ensure certain deployment fields do not get set
func TestReconcileOauthDeploymentNoChanges(t *testing.T) {

	// Setup expected values that are universal
	imageName := "oauthImage"

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	oauthDeployment := manifests.OAuthDeployment(targetNamespace)
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
		cm               corev1.ConfigMap
		deploymentConfig config.DeploymentConfig
		serverParams     OAuthServerParams
		configParams     OAuthConfigParams
	}{
		// empty deployment config
		{
			cm: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-oauth-config",
					Namespace: targetNamespace,
				},
				Data: map[string]string{"config.yaml": "test-data"},
			},
			deploymentConfig: config.DeploymentConfig{},
			serverParams: OAuthServerParams{
				AvailabilityProberImage: "test-availability-image",
				Socks5ProxyImage:        "test-socks-5-proxy-image",
			},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		ctx := context.Background()
		fakeClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
		oauthDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := oauthDeployment.Spec.MinReadySeconds
		err := ReconcileDeployment(ctx, fakeClient, oauthDeployment, ownerRef, &tc.cm, imageName, tc.deploymentConfig, tc.serverParams.IdentityProviders(), tc.serverParams.OauthConfigOverrides,
			tc.serverParams.AvailabilityProberImage, tc.serverParams.NamedCertificates(), tc.serverParams.Socks5ProxyImage, tc.serverParams.NoProxy, &tc.configParams, hyperv1.IBMCloudPlatform)
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(oauthDeployment.Spec.MinReadySeconds))
	}
}
