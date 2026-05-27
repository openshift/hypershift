package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptOauthMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cfg       *corev1.ConfigMap
		wantErr   bool
		errSubstr string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     &hyperv1.HostedControlPlane{},
				Client:  fake.NewClientBuilder().Build(),
			}

			err := adaptOauthMetadata(cpContext, tt.cfg)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
