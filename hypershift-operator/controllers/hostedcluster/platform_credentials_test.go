package hostedcluster

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformkubevirt "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const externalInfraKubeconfig = `apiVersion: v1
kind: Config
current-context: infra
contexts:
- name: infra
  context: {cluster: infra, user: infra}
clusters:
- name: infra
  cluster: {server: "https://api.infra.cluster.test:6443"}
users:
- name: infra
  user: {token: private-test-token}
`

func credentialHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "tenant", Generation: 4},
		Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{
			Type: hyperv1.KubevirtPlatform,
			Kubevirt: &hyperv1.KubevirtPlatformSpec{Credentials: &hyperv1.KubevirtPlatformCredentials{
				InfraKubeConfigSecret: &hyperv1.KubeconfigSecretRef{Name: "infra", Key: "kubeconfig"},
			}},
		}},
	}
}

func TestReconcileKubevirtCredentialSafety(t *testing.T) {
	t.Parallel()
	unsafe := strings.ReplaceAll(externalInfraKubeconfig, "user: {token: private-test-token}", "user: {auth-provider: {name: oidc}}")
	for _, tc := range []struct {
		name          string
		platform      hyperv1.PlatformType
		local         bool
		deleting      bool
		source        string
		target        string
		denyDelete    bool
		wantErr       string
		wantTarget    string
		wantCondition bool
	}{
		{name: "When external credentials are valid, it should not mark them found before publication", source: externalInfraKubeconfig},
		{name: "When external credentials are invalid, it should remove the unsafe target and report rejection", source: "malformed", target: "malformed", wantErr: "kubeconfig cannot be parsed", wantCondition: true},
		{name: "When the source Secret is missing, it should report rejection", wantErr: "failed to get secret", wantCondition: true},
		{name: "When a corrected source has an unsafe target, it should remove the target before discovery", source: externalInfraKubeconfig, target: unsafe},
		{name: "When a valid source has a safe target, it should retain the target", source: externalInfraKubeconfig, target: externalInfraKubeconfig, wantTarget: externalInfraKubeconfig},
		{name: "When external credentials are disabled, it should preserve local infrastructure behavior", local: true},
		{name: "When another platform is selected, it should leave its credential handling unchanged", platform: hyperv1.NonePlatform, source: "malformed", target: "malformed", wantTarget: "malformed"},
		{name: "When a deleting cluster has an unsafe target and no source, it should remove the target and allow teardown", deleting: true, target: "malformed"},
		{name: "When a deleting cluster has a safe target and no source, it should retain the target and allow teardown", deleting: true, target: externalInfraKubeconfig, wantTarget: externalInfraKubeconfig},
		{name: "When a deleting cluster has a rejected source, it should allow teardown without publication", deleting: true, source: unsafe},
		{name: "When unsafe target cleanup fails during deletion, it should report the cleanup failure", deleting: true, target: "malformed", denyDelete: true, wantErr: "cleanup denied", wantTarget: "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := credentialHostedCluster()
			if tc.platform != "" {
				hc.Spec.Platform.Type = tc.platform
			}
			if tc.local {
				hc.Spec.Platform.Kubevirt.Credentials = nil
			}
			if tc.deleting {
				hc.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				hc.Finalizers = []string{HostedClusterFinalizer}
			}
			objects := []client.Object{hc}
			if tc.source != "" {
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte(tc.source)}})
			}
			target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: hyperv1.KubeVirtInfraCredentialsSecretName}}
			if tc.target != "" {
				target.Data = map[string][]byte{"kubeconfig": []byte(tc.target)}
				objects = append(objects, target.DeepCopy())
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).
				WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if tc.denyDelete {
						return errors.New("target cleanup denied")
					}
					return c.Delete(ctx, obj, opts...)
				}}).Build()
			r := &HostedClusterReconciler{Client: c}

			err := r.reconcileKubevirtCredentialSafety(t.Context(), hc)

			if tc.wantErr == "" {
				g.Expect(err).NotTo(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(ContainSubstring(tc.wantErr)))
				g.Expect(err.Error()).NotTo(ContainSubstring("private-test-token"))
			}
			condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))
			if tc.wantCondition {
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.ObservedGeneration).To(Equal(hc.Generation))
				g.Expect(condition.Message).NotTo(ContainSubstring("private-test-token"))
			} else {
				g.Expect(condition).To(BeNil())
			}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(target), target)
			if tc.wantTarget == "" {
				g.Expect(err).To(Satisfy(apierrors.IsNotFound))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(target.Data).To(Equal(map[string][]byte{"kubeconfig": []byte(tc.wantTarget)}))
			}
		})
	}
}

func TestReconcilePlatformCredentialsWithStatus(t *testing.T) {
	t.Parallel()
	t.Run("When rejected credentials are corrected, it should publish credentials and recover status", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		hc := credentialHostedCluster()
		source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": []byte("malformed")}}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source).Build()
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		r := &HostedClusterReconciler{Client: c}
		namespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
		p := platformkubevirt.New(nil)
		g.Expect(r.reconcilePlatformCredentialsWithStatus(t.Context(), hc, upsert.New(false).CreateOrUpdate, namespace, p)).To(HaveOccurred())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.IsStatusConditionFalse(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeTrue())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
		source.Data["kubeconfig"] = []byte(externalInfraKubeconfig)
		g.Expect(c.Update(t.Context(), source)).To(Succeed())
		g.Expect(r.reconcilePlatformCredentialsWithStatus(t.Context(), hc, upsert.New(false).CreateOrUpdate, namespace, p)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		g.Expect(meta.IsStatusConditionTrue(hc.Status.Conditions, string(hyperv1.PlatformCredentialsFound))).To(BeTrue())
		target := &corev1.Secret{}
		g.Expect(c.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: hyperv1.KubeVirtInfraCredentialsSecretName}, target)).To(Succeed())
		g.Expect(target.Data).To(HaveKeyWithValue("kubeconfig", []byte(externalInfraKubeconfig)))
	})
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *clientcmdapi.Config)
		want   string
	}{
		{name: "When credentials reference a token file, it should reject publication and persist credential failure", mutate: func(_ *testing.T, config *clientcmdapi.Config) {
			config.AuthInfos["infra"].TokenFile = "/private-test-token"
		}, want: "tokenFile"},
		{name: "When credentials disable TLS verification, it should reject publication and persist credential failure", mutate: func(_ *testing.T, config *clientcmdapi.Config) {
			config.Clusters["infra"].InsecureSkipTLSVerify = true
		}, want: "insecure-skip-tls-verify"},
		{name: "When current context references an absent user, it should persist an actionable sanitized rejection", mutate: func(_ *testing.T, config *clientcmdapi.Config) {
			config.Contexts["infra"].AuthInfo = "private-test-token"
		}, want: "current-context references a missing user"},
		{name: "When current context references an absent cluster, it should persist an actionable sanitized rejection", mutate: func(_ *testing.T, config *clientcmdapi.Config) {
			config.Contexts["infra"].Cluster = "private-test-token"
		}, want: "current-context references a missing cluster"},
		{name: "When current context does not exist, it should reject publication and persist credential failure", mutate: func(_ *testing.T, config *clientcmdapi.Config) {
			config.CurrentContext = "private-test-token"
		}, want: "current-context does not exist"},
		{name: "When credentials contain embedded client certificates and CA data, it should publish usable credentials and persist success", mutate: func(t *testing.T, config *clientcmdapi.Config) {
			g := NewWithT(t)
			caKey, caCert, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{IsCA: true, Subject: pkix.Name{CommonName: "infra-ca", OrganizationalUnit: []string{"infra"}}, KeyUsages: x509.KeyUsageCertSign, Validity: certs.ValidityOneDay})
			g.Expect(err).NotTo(HaveOccurred())
			key, cert, err := certs.GenerateSignedCertificate(caKey, caCert, &certs.CertCfg{Subject: pkix.Name{CommonName: "infra-client", OrganizationalUnit: []string{"infra"}}, ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, Validity: certs.ValidityOneDay})
			g.Expect(err).NotTo(HaveOccurred())
			config.AuthInfos["infra"] = &clientcmdapi.AuthInfo{ClientCertificateData: certs.CertToPem(cert), ClientKeyData: certs.PrivateKeyToPem(key)}
			config.Clusters["infra"].CertificateAuthorityData = certs.CertToPem(caCert)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			config, err := clientcmd.Load([]byte(externalInfraKubeconfig))
			g.Expect(err).NotTo(HaveOccurred())
			tc.mutate(t, config)
			data, err := clientcmd.Write(*config)
			g.Expect(err).NotTo(HaveOccurred())
			hc := credentialHostedCluster()
			initialStatus := metav1.ConditionTrue
			if tc.want == "" {
				initialStatus = metav1.ConditionFalse
			}
			meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{Type: string(hyperv1.PlatformCredentialsFound), Status: initialStatus, Reason: "Previous", ObservedGeneration: hc.Generation})
			source := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "infra"}, Data: map[string][]byte{"kubeconfig": data, "extra": []byte("untrusted")}}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, source).Build()
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			r := &HostedClusterReconciler{Client: c}
			namespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
			p := platformkubevirt.New(nil)
			err = r.reconcilePlatformCredentialsWithStatus(t.Context(), hc, upsert.New(false).CreateOrUpdate, namespace, p)
			fresh := &hyperv1.HostedCluster{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), fresh)).To(Succeed())
			condition := meta.FindStatusCondition(fresh.Status.Conditions, string(hyperv1.PlatformCredentialsFound))
			g.Expect(condition).NotTo(BeNil())
			g.Expect(condition.ObservedGeneration).To(Equal(hc.Generation))
			target := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: hyperv1.KubeVirtInfraCredentialsSecretName}}
			if tc.want != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.want)))
				g.Expect(err.Error()).NotTo(ContainSubstring("private-test-token"))
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Message).To(ContainSubstring(tc.want))
				g.Expect(condition.Message).NotTo(ContainSubstring("private-test-token"))
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), target)).To(Satisfy(apierrors.IsNotFound))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(target), target)).To(Succeed())
				g.Expect(target.Data).To(Equal(map[string][]byte{"kubeconfig": data}))
				restConfig, err := clientcmd.RESTConfigFromKubeConfig(target.Data["kubeconfig"])
				g.Expect(err).NotTo(HaveOccurred())
				transportConfig, err := restConfig.TransportConfig()
				g.Expect(err).NotTo(HaveOccurred())
				tlsConfig, err := transport.TLSConfigFor(transportConfig)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tlsConfig.RootCAs).NotTo(BeNil())
				g.Expect(tlsConfig.GetClientCertificate).NotTo(BeNil())
				clientCertificate, err := tlsConfig.GetClientCertificate(nil)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(clientCertificate.Certificate).To(HaveLen(1))
			}
			unchanged := &corev1.Secret{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(source), unchanged)).To(Succeed())
			g.Expect(unchanged.Data).To(Equal(source.Data))
		})
	}
}
