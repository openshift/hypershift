//go:build e2e

package util

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/certs"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// EnsureMetricsProxyWorking enables metrics forwarding on the HostedCluster,
// waits for the metrics-proxy and endpoint-resolver deployments to become
// available, then verifies the metrics-proxy is returning Prometheus metrics
// with the expected injected labels through its in-cluster service.
func EnsureMetricsProxyWorking(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureMetricsProxyWorking", func(t *testing.T) {
		AtLeast(t, Version422)
		g := NewWithT(t)
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		// 1. Enable metrics forwarding by adding the annotation.
		t.Log("Enabling metrics forwarding on HostedCluster")
		err := UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.EnableMetricsForwarding] = "true"
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to patch HostedCluster with EnableMetricsForwarding annotation")

		// 2. Wait for deployments.
		t.Log("Waiting for endpoint-resolver deployment")
		WaitForDeploymentAvailable(ctx, t, client, "endpoint-resolver", hcpNamespace, 5*time.Minute, 10*time.Second)

		t.Log("Waiting for metrics-proxy deployment")
		WaitForDeploymentAvailable(ctx, t, client, "metrics-proxy", hcpNamespace, 5*time.Minute, 10*time.Second)

		// 3. Build an mTLS HTTP client to access the metrics-proxy service.
		httpClient := buildMetricsProxyClient(t, ctx, g, client, hcpNamespace)

		// 4. Access the metrics-proxy via its in-cluster service rather than through the
		// route, because private HCPs use .hypershift.local domains that aren't resolvable
		// from the test pod's DNS.
		// 5. Scrape metrics for kube-apiserver through the proxy and verify labels.
		t.Log("Verifying metrics-proxy returns scraped metrics with correct labels for kube-apiserver")
		metricsURL := fmt.Sprintf("https://metrics-proxy.%s.svc.cluster.local/metrics/kube-apiserver", hcpNamespace)

		var families map[string]*dto.MetricFamily
		err = wait.PollUntilContextTimeout(ctx, 15*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL, nil)
			if err != nil {
				t.Logf("failed to create request: %v", err)
				return false, nil
			}

			resp, err := httpClient.Do(req)
			if err != nil {
				t.Logf("metrics request failed (will retry): %v", err)
				return false, nil
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Logf("metrics-proxy returned status %d: %s (will retry)", resp.StatusCode, string(body))
				return false, nil
			}

			parser := expfmt.NewTextParser(model.LegacyValidation)
			families, err = parser.TextToMetricFamilies(resp.Body)
			if err != nil {
				t.Logf("failed to parse metrics response: %v", err)
				return false, nil
			}

			if len(families) == 0 {
				t.Log("metrics response is empty (will retry)")
				return false, nil
			}

			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to get metrics from metrics-proxy for kube-apiserver")
		t.Logf("Received %d metric families from kube-apiserver via metrics-proxy", len(families))

		// 6. Verify injected labels on the scraped metrics.
		// The metrics-proxy Labeler injects labels whose values come from
		// the ServiceMonitor annotations (hypershift.openshift.io/metrics-*).
		// For kube-apiserver, the ServiceMonitor sets:
		//   metrics-job:       "apiserver"
		//   metrics-namespace: "default"
		//   metrics-service:   "kubernetes"
		//   metrics-endpoint:  "https"
		// These mimic what Prometheus would produce in a standalone cluster.
		verifyMetricsLabels(t, g, families, "kube-apiserver", map[string]string{
			"namespace": "default",
			"job":       "apiserver",
			"service":   "kubernetes",
			"endpoint":  "https",
		})
	})
}

// buildMetricsProxyClient creates an HTTP client configured with mTLS for
// accessing the metrics-proxy. It reads the cluster-signer-ca secret (used as
// the client CA by the metrics-proxy) and generates an ephemeral client cert
// signed by that CA. It also reads the metrics-proxy-ca-cert to verify the
// server certificate.
func buildMetricsProxyClient(t *testing.T, ctx context.Context, g Gomega, client crclient.Client, hcpNamespace string) *http.Client {
	t.Helper()

	// Read the cluster-signer-ca secret (contains the CA cert + key).
	signerSecret := &corev1.Secret{}
	err := client.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "cluster-signer-ca"}, signerSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get cluster-signer-ca secret")

	caCert, err := certs.PemToCertificate(signerSecret.Data[corev1.TLSCertKey])
	g.Expect(err).NotTo(HaveOccurred(), "failed to parse cluster-signer-ca certificate")

	caKey, err := certs.PemToPrivateKey(signerSecret.Data[corev1.TLSPrivateKeyKey])
	g.Expect(err).NotTo(HaveOccurred(), "failed to parse cluster-signer-ca private key")

	// Generate an ephemeral client certificate signed by the cluster-signer-ca.
	clientKey, clientCert, err := certs.GenerateSignedCertificate(caKey, caCert, &certs.CertCfg{
		Subject: pkix.Name{
			CommonName:         "metrics-proxy-e2e-client",
			OrganizationalUnit: []string{"e2e-test"},
		},
		ExtKeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		Validity:     1 * time.Hour,
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to generate ephemeral client certificate")

	clientTLSCert, err := tls.X509KeyPair(certs.CertToPem(clientCert), certs.PrivateKeyToPem(clientKey))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create TLS key pair from ephemeral cert")

	// Read the metrics-proxy-ca-cert secret to verify the server's TLS cert.
	proxyCASecret := &corev1.Secret{}
	err = client.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "metrics-proxy-ca-cert"}, proxyCASecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get metrics-proxy-ca-cert secret")

	serverCAPool := x509.NewCertPool()
	ok := serverCAPool.AppendCertsFromPEM(proxyCASecret.Data[corev1.TLSCertKey])
	g.Expect(ok).To(BeTrue(), "failed to parse metrics-proxy-ca-cert")

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientTLSCert},
				RootCAs:      serverCAPool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}
}

// verifyMetricsLabels finds a metric with all required labels and asserts
// that each label has the expected value. It also verifies that the pod label
// starts with the component name and the instance label has ip:port format
// (proving endpoint-resolver resolved real pod IPs). Finally it logs all
// unique pod names to show metrics were scraped from real pods.
func verifyMetricsLabels(t *testing.T, g Gomega, families map[string]*dto.MetricFamily, componentName string, expectedLabels map[string]string) {
	t.Helper()

	requiredLabels := []string{"pod", "instance"}
	for k := range expectedLabels {
		requiredLabels = append(requiredLabels, k)
	}

	var checkedFamily string
	for name, mf := range families {
		if len(mf.Metric) == 0 {
			continue
		}
		m := mf.Metric[0]
		labelMap := make(map[string]string)
		for _, lp := range m.Label {
			labelMap[lp.GetName()] = lp.GetValue()
		}

		allPresent := true
		for _, rl := range requiredLabels {
			if _, ok := labelMap[rl]; !ok {
				allPresent = false
				break
			}
		}
		if !allPresent {
			continue
		}

		checkedFamily = name

		// Verify annotation-driven label values.
		for label, expected := range expectedLabels {
			g.Expect(labelMap[label]).To(Equal(expected),
				fmt.Sprintf("%s label should match ServiceMonitor annotation value", label))
		}

		// Verify pod label references a real component pod.
		g.Expect(labelMap["pod"]).To(HavePrefix(componentName),
			fmt.Sprintf("pod label should be a real %s pod name", componentName))

		// Verify instance label has ip:port format (proves endpoint-resolver worked).
		g.Expect(labelMap["instance"]).To(MatchRegexp(`\d+\.\d+\.\d+\.\d+:\d+`),
			"instance label should contain a pod IP:port")

		t.Logf("Verified labels on metric %q: pod=%s, namespace=%s, job=%s, service=%s, endpoint=%s, instance=%s",
			name, labelMap["pod"], labelMap["namespace"], labelMap["job"], labelMap["service"], labelMap["endpoint"], labelMap["instance"])
		break
	}
	g.Expect(checkedFamily).NotTo(BeEmpty(), "should find at least one metric family with all required injected labels")

	// Log unique pod names to show metrics were scraped from real pods.
	if mf, ok := families[checkedFamily]; ok {
		podNames := map[string]bool{}
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if lp.GetName() == "pod" {
					podNames[lp.GetValue()] = true
				}
			}
		}
		names := make([]string, 0, len(podNames))
		for name := range podNames {
			names = append(names, name)
		}
		t.Logf("Metrics for %q came from %d unique pod(s): %v", checkedFamily, len(podNames), strings.Join(names, ", "))
	}
}
