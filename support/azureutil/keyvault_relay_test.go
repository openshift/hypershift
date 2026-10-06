package azureutil

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testKeyVaultFQDN = "test-kms-keyvault.vault.azure.net"

func privateKeyVaultHostedControlPlaneForTest() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "hcp-namespace", Generation: 1},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type:  hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{Cloud: "AzurePublicCloud"},
			},
			SecretEncryption: &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.KMS,
				KMS: &hyperv1.KMSSpec{
					Provider: hyperv1.AZURE,
					Azure: &hyperv1.AzureKMSSpec{
						ActiveKey:      hyperv1.AzureKMSKey{KeyVaultName: "test-kms-keyvault", KeyName: "test-key", KeyVersion: "1"},
						KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
					},
				},
			},
		},
	}
}

func privateRouterObjectsForTest(clusterIP string, port int32, availableReplicas int32) (*corev1.Service, *appsv1.Deployment) {
	svc := manifests.PrivateRouterService("hcp-namespace")
	svc.Spec.ClusterIP = clusterIP
	svc.Spec.Ports = []corev1.ServicePort{{Name: "https", Port: port, Protocol: corev1.ProtocolTCP}}
	deployment := manifests.RouterDeployment("hcp-namespace")
	deployment.Spec.Replicas = ptr.To(int32(3))
	deployment.Status.AvailableReplicas = availableReplicas
	return svc, deployment
}

func keyVaultServerForTest(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	key, cert, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
		Subject:      pkix.Name{CommonName: testKeyVaultFQDN, OrganizationalUnit: []string{"test"}},
		DNSNames:     []string{testKeyVaultFQDN},
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsages:    x509.KeyUsageDigitalSignature,
		Validity:     time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return server, roots
}

func TestPrivateRouterKeyVaultClient(t *testing.T) {
	for _, tc := range []struct {
		name, clusterIP string
		port            int32
		message         string
	}{
		{name: "When the Service is headless, it should reject the relay", clusterIP: corev1.ClusterIPNone, port: 443, message: "no ClusterIP"},
		{name: "When the https port is absent, it should reject the relay", clusterIP: "127.0.0.1", message: "no https port"},
		{name: "When the Service uses another https port, it should dial that port", clusterIP: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			port := tc.port
			var relay net.Listener
			if tc.message == "" {
				var err error
				relay, err = net.Listen("tcp", "127.0.0.1:0")
				g.Expect(err).ToNot(HaveOccurred())
				defer relay.Close()
				port = int32(relay.Addr().(*net.TCPAddr).Port)
			}
			svc, router := privateRouterObjectsForTest(tc.clusterIP, port, 1)
			if tc.message == "no https port" {
				svc.Spec.Ports = nil
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(svc, router).Build()
			keyVaultClient, err := PrivateRouterKeyVaultClient(t.Context(), c, privateKeyVaultHostedControlPlaneForTest())
			if tc.message != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.message)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			defer keyVaultClient.CloseIdleConnections()
			conn, err := keyVaultClient.Transport.(*http.Transport).DialContext(t.Context(), "tcp", net.JoinHostPort(testKeyVaultFQDN, "443"))
			g.Expect(err).ToNot(HaveOccurred())
			defer conn.Close()
			g.Expect(conn.RemoteAddr().String()).To(Equal(relay.Addr().String()))
		})
	}

	t.Run("When the router Deployment has no available replicas, it should report the relay as unavailable", func(t *testing.T) {
		g := NewWithT(t)
		svc, router := privateRouterObjectsForTest("172.30.0.100", 443, 0)
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(svc, router).Build()
		_, err := PrivateRouterKeyVaultClient(t.Context(), c, privateKeyVaultHostedControlPlaneForTest())
		g.Expect(err).To(MatchError(ContainSubstring("no available replicas")))
	})
}

// stubRoundTripper stands in for a http.DefaultTransport that something else in
// the process has replaced with a type that is not *http.Transport.
type stubRoundTripper struct{}

func (stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("stub round tripper")
}

func TestKeyVaultRelayTransport(t *testing.T) {
	relay, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	direct, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	for _, tc := range []struct{ name, address, destination string }{
		{name: "When the address is the vault hostname, it should dial the relay", address: net.JoinHostPort(testKeyVaultFQDN, "443"), destination: relay.Addr().String()},
		{name: "When the vault hostname uses upper case, it should dial the relay", address: net.JoinHostPort(strings.ToUpper(testKeyVaultFQDN), "443"), destination: relay.Addr().String()},
		{name: "When the address is unrelated, it should dial that address directly", address: direct.Addr().String(), destination: direct.Addr().String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			transport := KeyVaultRelayTransport(testKeyVaultFQDN, relay.Addr().String())
			defer transport.CloseIdleConnections()
			g.Expect(transport.Proxy).To(BeNil(), "the private relay must bypass environment proxies")
			conn, err := transport.DialContext(t.Context(), "tcp", tc.address)
			g.Expect(err).ToNot(HaveOccurred())
			defer conn.Close()
			g.Expect(conn.RemoteAddr().String()).To(Equal(tc.destination))
		})
	}
	t.Run("When DefaultTransport is not an *http.Transport, it should still build a relay transport", func(t *testing.T) {
		g := NewWithT(t)
		original := http.DefaultTransport
		http.DefaultTransport = stubRoundTripper{}
		defer func() { http.DefaultTransport = original }()
		transport := KeyVaultRelayTransport(testKeyVaultFQDN, relay.Addr().String())
		defer transport.CloseIdleConnections()
		conn, err := transport.DialContext(t.Context(), "tcp", net.JoinHostPort(testKeyVaultFQDN, "443"))
		g.Expect(err).ToNot(HaveOccurred())
		defer conn.Close()
		g.Expect(conn.RemoteAddr().String()).To(Equal(relay.Addr().String()))
	})
	t.Run("When the relay serves a certificate for a different hostname, it should reject the TLS connection", func(t *testing.T) {
		g := NewWithT(t)
		server, roots := keyVaultServerForTest(t, func(http.ResponseWriter, *http.Request) { t.Error("an invalid TLS connection reached the handler") })
		transport := KeyVaultRelayTransport("wrong.vault.azure.net", server.Listener.Addr().String())
		defer transport.CloseIdleConnections()
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://wrong.vault.azure.net", nil)
		g.Expect(err).ToNot(HaveOccurred())
		response, err := (&http.Client{Transport: transport}).Do(request)
		if response != nil {
			response.Body.Close()
		}
		var hostnameError x509.HostnameError
		g.Expect(errors.As(err, &hostnameError)).To(BeTrue(), "the vault certificate must still be checked against its hostname: %v", err)
	})
}
