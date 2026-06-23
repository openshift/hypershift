package oauth

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	component "github.com/openshift/hypershift/support/controlplane-component"

	osinv1 "github.com/openshift/api/osin/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptOAuthConfig_LoginURL(t *testing.T) {
	const (
		lbHost    = "10.71.22.118"
		dnsName   = "api.ocp4.example.com"
		oauthHost = "oauth.example.com"
	)
	var lbPort int32 = 6443

	tests := []struct {
		name               string
		kubeAPIServerDNS   string
		loginURLAnnotation string
		expectedLoginURL   string
	}{
		{
			name:             "no kubeAPIServerDNSName — uses controlPlaneEndpoint host",
			kubeAPIServerDNS: "",
			expectedLoginURL: fmt.Sprintf("https://%s:%d", lbHost, lbPort),
		},
		{
			name:             "kubeAPIServerDNSName set — uses DNS name",
			kubeAPIServerDNS: dnsName,
			expectedLoginURL: fmt.Sprintf("https://%s:%d", dnsName, lbPort),
		},
		{
			name:               "kubeAPIServerDNSName set but annotation override present — annotation wins",
			kubeAPIServerDNS:   dnsName,
			loginURLAnnotation: "https://iam.custom.ibm.com/login",
			expectedLoginURL:   "https://iam.custom.ibm.com/login",
		},
		{
			name:               "only annotation override, no DNS name — annotation used",
			kubeAPIServerDNS:   "",
			loginURLAnnotation: "https://iam.custom.ibm.com/login",
			expectedLoginURL:   "https://iam.custom.ibm.com/login",
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
					KubeAPIServerDNSName: tt.kubeAPIServerDNS,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: lbHost,
						Port: lbPort,
					},
				},
			}
			if tt.loginURLAnnotation != "" {
				hcp.Annotations = map[string]string{
					hyperv1.OauthLoginURLOverrideAnnotation: tt.loginURLAnnotation,
				}
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
				InfraStatus: infra.InfrastructureStatus{
					OAuthHost: oauthHost,
					OAuthPort: 443,
					APIPort:   lbPort,
				},
				Client: fake.NewClientBuilder().Build(),
			}

			cfg := &osinv1.OsinServerConfig{
				OAuthConfig: osinv1.OAuthConfig{},
			}

			adaptOAuthConfig(cpContext, cfg)

			g.Expect(cfg.OAuthConfig.LoginURL).To(Equal(tt.expectedLoginURL))
			g.Expect(cfg.OAuthConfig.MasterURL).To(Equal(fmt.Sprintf("https://%s:%d", oauthHost, 443)))
			g.Expect(cfg.OAuthConfig.MasterPublicURL).To(Equal(fmt.Sprintf("https://%s:%d", oauthHost, 443)))
		})
	}
}
