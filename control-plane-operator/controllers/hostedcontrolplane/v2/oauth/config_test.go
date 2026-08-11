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
)

func TestGetOAuthServiceDNS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		namespace string
		expected  string
	}{
		{
			name:      "When namespace is provided, it should return the correct OAuth service DNS",
			namespace: "clusters-test-cluster",
			expected:  "oauth-openshift.clusters-test-cluster.svc.cluster.local",
		},
		{
			name:      "When namespace is empty, it should return empty string",
			namespace: "",
			expected:  "",
		},
		{
			name:      "When namespace has special characters, it should include them in the DNS",
			namespace: "test-ns-123",
			expected:  "oauth-openshift.test-ns-123.svc.cluster.local",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result := getOAuthServiceDNS(tt.namespace)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestAdaptOAuthConfig(t *testing.T) {

	const (
		testNamespace = "test-cluster"
	)
	testCases := []struct {
		name                    string
		oauthHost               string
		oauthPort               int32
		cpEndpointHost          string
		cpEndpointPort          int32
		kasDNSName              string
		loginURLOverride        string
		expectedLoginURL        string
		expectedMasterURL       string
		expectedMasterPublicURL string
	}{
		{
			name:                    "When no custom DNS is set, it should use the control plane endpoint for LoginURL",
			oauthHost:               "oauth.example.com",
			oauthPort:               443,
			cpEndpointHost:          "api.example.com",
			cpEndpointPort:          6443,
			expectedLoginURL:        "https://api.example.com:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://oauth.example.com:443",
		},
		{
			name:                    "When KubeAPIServerDNSName is set, it should use the custom DNS name for LoginURL",
			oauthHost:               "oauth.example.com",
			oauthPort:               443,
			cpEndpointHost:          "10.0.0.1",
			cpEndpointPort:          6443,
			kasDNSName:              "api.custom.example.com",
			expectedLoginURL:        "https://api.custom.example.com:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://oauth.example.com:443",
		},
		{
			name:                    "When control plane endpoint is an IP and no custom DNS is set, it should use the IP for LoginURL",
			oauthHost:               "10.0.0.2",
			oauthPort:               443,
			cpEndpointHost:          "10.0.0.1",
			cpEndpointPort:          6443,
			expectedLoginURL:        "https://10.0.0.1:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://10.0.0.2:443",
		},
		{
			name:                    "When login URL override annotation is set, it should take precedence over KubeAPIServerDNSName",
			oauthHost:               "oauth.example.com",
			oauthPort:               443,
			cpEndpointHost:          "10.0.0.1",
			cpEndpointPort:          6443,
			kasDNSName:              "api.custom.example.com",
			loginURLOverride:        "https://ibm.override.example.com:6443",
			expectedLoginURL:        "https://ibm.override.example.com:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://oauth.example.com:443",
		},
		{
			name:                    "When control plane endpoint is an IPv6 address, it should bracket it in the LoginURL",
			oauthHost:               "oauth.example.com",
			oauthPort:               443,
			cpEndpointHost:          "2001:db8::1",
			cpEndpointPort:          6443,
			expectedLoginURL:        "https://[2001:db8::1]:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://oauth.example.com:443",
		},
		{
			name:                    "When OAuth host is an IPv6 address, it should bracket it in the MasterURL",
			oauthHost:               "2001:db8::2",
			oauthPort:               443,
			cpEndpointHost:          "api.example.com",
			cpEndpointPort:          6443,
			expectedLoginURL:        "https://api.example.com:6443",
			expectedMasterURL:       fmt.Sprintf("https://%s:%d", getOAuthServiceDNS(testNamespace), OAuthServerPort),
			expectedMasterPublicURL: "https://[2001:db8::2]:443",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					KubeAPIServerDNSName: tc.kasDNSName,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: tc.cpEndpointHost,
						Port: tc.cpEndpointPort,
					},
				},
			}
			if tc.loginURLOverride != "" {
				hcp.Annotations = map[string]string{
					hyperv1.OauthLoginURLOverrideAnnotation: tc.loginURLOverride,
				}
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
				InfraStatus: infra.InfrastructureStatus{
					OAuthHost: tc.oauthHost,
					OAuthPort: tc.oauthPort,
				},
			}

			cfg := &osinv1.OsinServerConfig{}
			cfg.OAuthConfig = osinv1.OAuthConfig{}

			adaptOAuthConfig(cpContext, cfg)

			g.Expect(cfg.OAuthConfig.LoginURL).To(Equal(tc.expectedLoginURL))
			g.Expect(cfg.OAuthConfig.MasterURL).To(Equal(tc.expectedMasterURL))
			g.Expect(cfg.OAuthConfig.MasterPublicURL).To(Equal(tc.expectedMasterPublicURL))
		})
	}
}
