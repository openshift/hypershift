package ignitionconsumer

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

// TestRolloutGlobalConfigData pins the rollout-relevant global-config subset the
// NodePool controller authors into the CR-owned rolloutGlobalConfig ConfigMap:
// it must reflect the user-set proxy/image/TLS inputs and must NOT depend on the
// computed Status proxy defaults (which derive from networking/platform and would
// otherwise churn the rollout hash).
func TestRolloutGlobalConfigData(t *testing.T) {
	const relWithTLS = "4.23.0"

	baseHC := func() *hyperv1.HostedCluster {
		return &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Configuration: &hyperv1.ClusterConfiguration{
					Proxy: &configv1.ProxySpec{HTTPProxy: "http://proxy.example.com"},
				},
				Platform: hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			},
		}
	}

	t.Run("deterministic", func(t *testing.T) {
		g := NewWithT(t)
		a, err := rolloutGlobalConfigData(baseHC(), relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(a).ToNot(BeEmpty())
		b, err := rolloutGlobalConfigData(baseHC(), relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(b).To(Equal(a))
	})

	t.Run("proxy change yields different bytes", func(t *testing.T) {
		g := NewWithT(t)
		a, err := rolloutGlobalConfigData(baseHC(), relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())

		hc := baseHC()
		hc.Spec.Configuration.Proxy.HTTPProxy = "http://other.example.com"
		b, err := rolloutGlobalConfigData(hc, relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(b).ToNot(Equal(a))
	})

	t.Run("image change yields different bytes", func(t *testing.T) {
		g := NewWithT(t)
		a, err := rolloutGlobalConfigData(baseHC(), relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())

		hc := baseHC()
		hc.Spec.Configuration.Image = &configv1.ImageSpec{ExternalRegistryHostnames: []string{"registry.example.com"}}
		b, err := rolloutGlobalConfigData(hc, relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(b).ToNot(Equal(a))
	})

	// RF#3: computed Status proxy defaults derive from networking/platform. The
	// rollout subset must ignore them, so two HCs with identical spec.Configuration
	// but different platform (which only feeds the computed NoProxy status default)
	// must produce identical bytes.
	t.Run("computed status defaults excluded", func(t *testing.T) {
		g := NewWithT(t)
		a, err := rolloutGlobalConfigData(baseHC(), relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())

		hc := baseHC()
		hc.Spec.Platform = hyperv1.PlatformSpec{
			Type: hyperv1.AWSPlatform,
			AWS:  &hyperv1.AWSPlatformSpec{Region: "us-east-1"},
		}
		b, err := rolloutGlobalConfigData(hc, relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(b).To(Equal(a))
	})

	// TLS security profile only affects worker configuration from 4.23.0 onward.
	t.Run("TLS profile included only from 4.23.0", func(t *testing.T) {
		g := NewWithT(t)
		hc := baseHC()
		hc.Spec.Configuration.APIServer = &configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
		}

		pre, err := rolloutGlobalConfigData(hc, "4.22.0")
		g.Expect(err).ToNot(HaveOccurred())
		post, err := rolloutGlobalConfigData(hc, relWithTLS)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(post).ToNot(Equal(pre))
	})
}
