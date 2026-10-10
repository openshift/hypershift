package kubevirtexternalinfra

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func validInfraConfig() *clientcmdapi.Config {
	return &clientcmdapi.Config{
		CurrentContext: "infra",
		Contexts: map[string]*clientcmdapi.Context{
			"infra": {Cluster: "infra", AuthInfo: "infra"},
		},
		Clusters: map[string]*clientcmdapi.Cluster{
			"infra": {Server: "https://api.infra.cluster.test:6443"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"infra": {Token: "private-test-token"},
		},
	}
}

func TestGetKubeConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		keys      []string
		custom    bool
		canonical bool
		invalid   bool
		want      string
	}{
		{name: "When no key is specified, it should preserve canonical-key retrieval", canonical: true},
		{name: "When only a custom key is provided, it should return the configured credentials", keys: []string{"custom"}, custom: true},
		{name: "When a canonical key accompanies a custom key, it should return the configured credentials", keys: []string{"custom"}, custom: true, canonical: true},
		{name: "When a configured key is absent, it should reject credentials", keys: []string{"missing"}, canonical: true, want: "missing"},
		{name: "When a configured key is empty, it should reject credentials", keys: []string{""}, canonical: true, want: "key"},
		{name: "When tenant credentials contain an auth provider, it should reject them before constructing clients", canonical: true, invalid: true, want: "auth-provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			config := validInfraConfig()
			if tc.invalid {
				config.AuthInfos["infra"].AuthProvider = &clientcmdapi.AuthProviderConfig{Name: "oidc"}
			}
			data := configBytes(t, config)
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra"}, Data: map[string][]byte{}}
			if tc.custom {
				secret.Data["custom"] = data
			}
			if tc.canonical {
				secret.Data["kubeconfig"] = data
				if tc.custom {
					secret.Data["kubeconfig"] = []byte(strings.ReplaceAll(string(data), "private-test-token", "canonical-test-token"))
				}
			}
			c := fake.NewClientBuilder().WithObjects(secret).Build()
			got, err := GetKubeConfig(t.Context(), c, secret.Namespace, secret.Name, tc.keys...)
			if tc.want == "" {
				g.Expect(err).NotTo(HaveOccurred())
				if tc.custom {
					g.Expect(got).To(Equal(secret.Data["custom"]))
				} else {
					g.Expect(got).To(Equal(secret.Data["kubeconfig"]))
				}
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
			}
		})
	}
}

func TestDiscoverKubevirtClusterClient(t *testing.T) {
	t.Parallel()
	t.Run("When cached credentials change or become unsafe, it should revalidate and replace or invalidate the client", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		data := configBytes(t, validInfraConfig())
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra"}, Data: map[string][]byte{"kubeconfig": data}}
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		credentials := &hyperv1.KubevirtPlatformCredentials{
			InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: secret.Name, Key: "kubeconfig"},
			InfraNamespace:        "worker-vms",
		}
		cache := NewKubevirtInfraClientMap()
		first, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		cached, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cached).To(BeIdenticalTo(first))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(secret), secret)).To(Succeed())
		secret.Data["kubeconfig"] = []byte(strings.ReplaceAll(string(data), "private-test-token", "rotated-test-token"))
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		rotated, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(rotated).NotTo(BeIdenticalTo(first))
		unsafe := validInfraConfig()
		unsafe.AuthInfos["unused"] = &clientcmdapi.AuthInfo{AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "oidc"}}
		secret.Data["kubeconfig"] = configBytes(t, unsafe)
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		rejected, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).To(MatchError(ContainSubstring("auth-provider")))
		g.Expect(rejected).To(BeNil())
		secret.Data["kubeconfig"] = data
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		recovered, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(recovered).NotTo(BeIdenticalTo(rotated))
		g.Expect(recovered.GetInfraNamespace()).To(Equal("worker-vms"))
	})
	t.Run("When a custom reference has canonical credentials, it should use the configured key and rotate on its changes", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		selected := configBytes(t, validInfraConfig())
		canonical := []byte(strings.ReplaceAll(string(selected), "private-test-token", "canonical-test-token"))
		canonical = []byte(strings.ReplaceAll(string(canonical), "api.infra.cluster.test", "api.canonical.cluster.test"))
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra"}, Data: map[string][]byte{"custom": selected, "kubeconfig": canonical}}
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		credentials := &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: secret.Name, Key: "custom"}, InfraNamespace: "worker-vms"}
		cache := NewKubevirtInfraClientMap()
		first, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(first.(*kubevirtInfraClientImp).DiscoveryClient.RESTClient().Get().URL().Host).To(Equal("api.infra.cluster.test:6443"))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(secret), secret)).To(Succeed())
		secret.Data["custom"] = []byte(strings.ReplaceAll(string(selected), "private-test-token", "selected-rotated-token"))
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		rotated, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(rotated).NotTo(BeIdenticalTo(first))
		secret.Data["kubeconfig"] = []byte(strings.ReplaceAll(string(canonical), "canonical-test-token", "canonical-rotated-token"))
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		unchanged, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(unchanged).To(BeIdenticalTo(rotated))
		credentials.InfraNamespace = "replacement-vms"
		moved, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(moved).NotTo(BeIdenticalTo(rotated))
		g.Expect(moved.GetInfraNamespace()).To(Equal("replacement-vms"))
		unsafe := validInfraConfig()
		unsafe.AuthInfos["infra"].TokenFile = "/must-not-read"
		secret.Data["custom"] = configBytes(t, unsafe)
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		_, err = cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).To(MatchError(ContainSubstring("tokenFile")))
		secret.Data["custom"] = selected
		g.Expect(c.Update(t.Context(), secret)).To(Succeed())
		recovered, err := cache.DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(recovered).NotTo(BeIdenticalTo(moved))
	})
	t.Run("When credentials embed real client certificates and CA data, it should construct an infrastructure client without reading files", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "infra"}, Data: map[string][]byte{"kubeconfig": configBytes(t, certificateInfraConfig(t))}}
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		credentials := &hyperv1.KubevirtPlatformCredentials{InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: secret.Name, Key: "kubeconfig"}, InfraNamespace: "worker-vms"}
		infraClient, err := NewKubevirtInfraClientMap().DiscoverKubevirtClusterClient(t.Context(), c, "tenant", credentials, "local", secret.Namespace)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(infraClient.GetInfraNamespace()).To(Equal("worker-vms"))
	})
}

func certificateInfraConfig(t *testing.T) *clientcmdapi.Config {
	t.Helper()
	g := NewWithT(t)
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "infra-ca", OrganizationalUnit: []string{"infra"}}, KeyUsages: x509.KeyUsageCertSign, Validity: certs.ValidityOneDay})
	g.Expect(err).NotTo(HaveOccurred())
	key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, &certs.CertCfg{Subject: pkix.Name{CommonName: "infra-client", OrganizationalUnit: []string{"infra"}}, ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, Validity: certs.ValidityOneDay})
	g.Expect(err).NotTo(HaveOccurred())
	config := validInfraConfig()
	config.AuthInfos["infra"] = &clientcmdapi.AuthInfo{ClientCertificateData: certs.CertToPem(cert), ClientKeyData: certs.PrivateKeyToPem(key)}
	config.Clusters["infra"].CertificateAuthorityData = certs.CertToPem(caCert)
	return config
}

func configBytes(t *testing.T, config *clientcmdapi.Config) []byte {
	t.Helper()
	g := NewWithT(t)
	data, err := clientcmd.Write(*config)
	g.Expect(err).NotTo(HaveOccurred())
	return data
}

func TestValidateKubeConfig(t *testing.T) {
	t.Parallel()
	certificateConfig := certificateInfraConfig(t)
	for _, tc := range []struct {
		name   string
		mutate func(*clientcmdapi.Config)
		want   string
	}{
		{name: "When credentials use a static token, it should accept them"},
		{
			name: "When an inactive user has an exec plugin, it should reject it",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["unused"] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "must-not-run"}}
			}, want: "exec",
		},
		{
			name: "When an inactive user has an auth provider, it should reject it",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["unused"] = &clientcmdapi.AuthInfo{AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "oidc"}}
			}, want: "auth-provider",
		},
		{
			name:   "When a token file supplements an inline token, it should reject it without reading the file",
			mutate: func(c *clientcmdapi.Config) { c.AuthInfos["infra"].TokenFile = "/must-not-read" },
			want:   "tokenFile",
		},
		{
			name: "When an inactive cluster disables TLS verification, it should reject it",
			mutate: func(c *clientcmdapi.Config) {
				c.Clusters["unused"] = &clientcmdapi.Cluster{Server: "https://api.other.cluster.test:6443", InsecureSkipTLSVerify: true}
			}, want: "insecure-skip-tls-verify",
		},
		{
			name:   "When current context is absent, it should reject the configuration",
			mutate: func(c *clientcmdapi.Config) { c.CurrentContext = "" }, want: "current-context",
		},
		{
			name:   "When current context does not exist, it should reject the configuration",
			mutate: func(c *clientcmdapi.Config) { c.CurrentContext = "missing" }, want: "context",
		},
		{
			name:   "When a context references a missing user, it should reject the configuration",
			mutate: func(c *clientcmdapi.Config) { c.Contexts["infra"].AuthInfo = "private-test-token" }, want: "current-context references a missing user",
		},
		{
			name:   "When a context references a missing cluster, it should reject the configuration",
			mutate: func(c *clientcmdapi.Config) { c.Contexts["infra"].Cluster = "private-test-token" }, want: "current-context references a missing cluster",
		},
		{
			name:   "When current context has no user reference, it should identify the missing reference",
			mutate: func(c *clientcmdapi.Config) { c.Contexts["infra"].AuthInfo = "" }, want: "current-context must reference a user",
		},
		{
			name:   "When current context has no cluster reference, it should identify the missing reference",
			mutate: func(c *clientcmdapi.Config) { c.Contexts["infra"].Cluster = "" }, want: "current-context must reference a cluster",
		},
		{
			name:   "When a cluster has no server, it should reject the configuration",
			mutate: func(c *clientcmdapi.Config) { c.Clusters["infra"].Server = "" }, want: "structure",
		},
		{
			name: "When credentials use embedded certificates, it should accept them without reading files",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["infra"] = certificateConfig.AuthInfos["infra"].DeepCopy()
				c.Clusters["infra"].CertificateAuthorityData = certificateConfig.Clusters["infra"].CertificateAuthorityData
			},
		},
		{
			name: "When an embedded certificate has no key, it should reject the invalid structure",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["infra"] = &clientcmdapi.AuthInfo{ClientCertificateData: certificateConfig.AuthInfos["infra"].ClientCertificateData}
			}, want: "structure",
		},
		{
			name: "When certificate file paths are provided, it should reject them without opening those files",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["infra"] = &clientcmdapi.AuthInfo{ClientCertificate: "/must-not-open-cert", ClientKey: "/must-not-open-key"}
				c.Clusters["infra"].CertificateAuthority = "/must-not-open-ca"
			}, want: "client-certificate",
		},
		{
			name:   "When a client key path accompanies an inline token, it should reject it without opening the file",
			mutate: func(c *clientcmdapi.Config) { c.AuthInfos["infra"].ClientKey = "/private-test-token" }, want: "client-key",
		},
		{
			name: "When an inactive user references a client certificate, it should reject that path",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["unused"] = &clientcmdapi.AuthInfo{ClientCertificate: "/private-test-token"}
			}, want: "client-certificate",
		},
		{
			name: "When an inactive cluster references a CA file, it should reject that path",
			mutate: func(c *clientcmdapi.Config) {
				c.Clusters["unused"] = &clientcmdapi.Cluster{Server: "https://api.other.cluster.test:6443", CertificateAuthority: "/private-test-token"}
			}, want: "certificate-authority",
		},
		{
			name:   "When token and basic authentication conflict, it should use upstream structural rejection without disclosing values",
			mutate: func(c *clientcmdapi.Config) { c.AuthInfos["infra"].Username = "private-test-token" }, want: "structure",
		},
		{
			name:   "When a context namespace is invalid, it should use upstream structural rejection without disclosing values",
			mutate: func(c *clientcmdapi.Config) { c.Contexts["infra"].Namespace = "invalid/private-test-token" }, want: "structure",
		},
		{
			name: "When certificate file and embedded certificate conflict, it should reject them without reading files",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["infra"] = certificateConfig.AuthInfos["infra"].DeepCopy()
				c.AuthInfos["infra"].ClientCertificate = "/private-test-token"
			}, want: "client-certificate",
		},
		{
			name: "When key file and embedded key conflict, it should reject them without reading files",
			mutate: func(c *clientcmdapi.Config) {
				c.AuthInfos["infra"] = certificateConfig.AuthInfos["infra"].DeepCopy()
				c.AuthInfos["infra"].ClientKey = "/private-test-token"
			}, want: "client-key",
		},
		{
			name: "When CA file and embedded CA conflict, it should reject them without reading files",
			mutate: func(c *clientcmdapi.Config) {
				c.Clusters["infra"].CertificateAuthority = "/private-test-token"
				c.Clusters["infra"].CertificateAuthorityData = certificateConfig.Clusters["infra"].CertificateAuthorityData
			}, want: "certificate-authority",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			config := validInfraConfig()
			if tc.mutate != nil {
				tc.mutate(config)
			}
			err := ValidateKubeConfig(configBytes(t, config))
			if tc.want == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
				g.Expect(err.Error()).NotTo(ContainSubstring("private-test-token"))
			}
		})
	}
	for _, tc := range []struct{ name, data, want string }{
		{"When kubeconfig is empty, it should reject it", "", "empty"},
		{"When kubeconfig is malformed, it should reject it without leaking input", "users: [private-test-token", "parsed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			err := ValidateKubeConfig([]byte(tc.data))
			g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
			g.Expect(err.Error()).NotTo(ContainSubstring("private-test-token"))
		})
	}
}

func TestKubeConfigData(t *testing.T) {
	t.Parallel()
	t.Run("When credential entries differ, it should preserve validated consumer data without aliasing source bytes", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		selected := configBytes(t, validInfraConfig())
		canonical := []byte(strings.ReplaceAll(string(selected), "private-test-token", "canonical-test-token"))
		source := &corev1.Secret{Data: map[string][]byte{
			"custom": selected, "kubeconfig": canonical, "namespace": []byte("worker-vms"), "extra": []byte("untrusted"),
		}}
		data, err := KubeConfigData(source, "custom")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(data).To(Equal(map[string][]byte{"custom": selected, "kubeconfig": canonical, "namespace": []byte("worker-vms")}))
		data["custom"][0] = 'X'
		data["kubeconfig"][0] = 'X'
		data["namespace"][0] = 'X'
		g.Expect(source.Data["custom"][0]).To(Equal(byte('a')))
		g.Expect(source.Data["kubeconfig"][0]).To(Equal(byte('a')))
		g.Expect(source.Data["namespace"]).To(Equal([]byte("worker-vms")))
	})
	t.Run("When the source Secret is nil, it should reject credentials without panic", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		_, err := KubeConfigData(nil, "kubeconfig")
		g.Expect(err).To(HaveOccurred())
	})
}
