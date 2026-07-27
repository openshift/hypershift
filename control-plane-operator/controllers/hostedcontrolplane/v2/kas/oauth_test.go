package kas

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptOauthMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cfg        *corev1.ConfigMap
		oauthHost  string
		oauthPort  int32
		wantErr    bool
		errSubstr  string
		wantIssuer string
		wantAuthz  string
		wantToken  string
	}{
		{
			name: "When ConfigMap contains invalid JSON, it should return an unmarshal error",
			cfg: &corev1.ConfigMap{
				Data: map[string]string{
					OauthMetadataConfigKey: "not-valid-json{{{",
				},
			},
			wantErr:   true,
			errSubstr: "failed to unmarshal oauth metadata",
		},
		{
			name: "When OAuth host is IPv4, issuer URLs should not use brackets",
			cfg: &corev1.ConfigMap{
				Data: map[string]string{
					OauthMetadataConfigKey: `{}`,
				},
			},
			oauthHost:  "192.0.2.10",
			oauthPort:  32047,
			wantIssuer: "https://192.0.2.10:32047",
			wantAuthz:  "https://192.0.2.10:32047/oauth/authorize",
			wantToken:  "https://192.0.2.10:32047/oauth/token",
		},
		{
			name: "When OAuth host is IPv6, issuer URLs should bracket the address",
			cfg: &corev1.ConfigMap{
				Data: map[string]string{
					OauthMetadataConfigKey: `{}`,
				},
			},
			oauthHost:  "fd2e:6f44:5dd8:c956::14",
			oauthPort:  32047,
			wantIssuer: "https://[fd2e:6f44:5dd8:c956::14]:32047",
			wantAuthz:  "https://[fd2e:6f44:5dd8:c956::14]:32047/oauth/authorize",
			wantToken:  "https://[fd2e:6f44:5dd8:c956::14]:32047/oauth/token",
		},
		{
			name: "When OAuth host is a hostname, issuer URLs should remain unbracketed",
			cfg: &corev1.ConfigMap{
				Data: map[string]string{
					OauthMetadataConfigKey: `{}`,
				},
			},
			oauthHost:  "oauth.example.com",
			oauthPort:  443,
			wantIssuer: "https://oauth.example.com:443",
			wantAuthz:  "https://oauth.example.com:443/oauth/authorize",
			wantToken:  "https://oauth.example.com:443/oauth/token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     &hyperv1.HostedControlPlane{},
				Client:  fake.NewClientBuilder().Build(),
				InfraStatus: infra.InfrastructureStatus{
					OAuthHost: tt.oauthHost,
					OAuthPort: tt.oauthPort,
				},
			}

			err := adaptOauthMetadata(cpContext, tt.cfg)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			var oauthMetadata map[string]interface{}
			g.Expect(json.Unmarshal([]byte(tt.cfg.Data[OauthMetadataConfigKey]), &oauthMetadata)).To(Succeed())
			g.Expect(oauthMetadata["issuer"]).To(Equal(tt.wantIssuer))
			g.Expect(oauthMetadata["authorization_endpoint"]).To(Equal(tt.wantAuthz))
			g.Expect(oauthMetadata["token_endpoint"]).To(Equal(tt.wantToken))
		})
	}
}
