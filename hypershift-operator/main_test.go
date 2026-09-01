package main

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/hypershift-operator/controllers/webhookcerts"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestWaitAndConfigureServingCert(t *testing.T) {
	t.Parallel()

	t.Run("When cert dir is empty, it should return the original TLS options unchanged", func(t *testing.T) {
		g := NewWithT(t)

		existingCalled := false
		existing := func(c *tls.Config) {
			existingCalled = true
			c.MinVersion = tls.VersionTLS12
		}
		tlsOpts := []func(*tls.Config){existing}

		result, err := waitAndConfigureServingCert(t.Context(), "", tlsOpts, time.Millisecond, 50*time.Millisecond, logr.Discard())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(HaveLen(1))

		cfg := &tls.Config{}
		result[0](cfg)
		g.Expect(existingCalled).To(BeTrue())
		g.Expect(cfg.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
		g.Expect(cfg.GetCertificate).To(BeNil())
	})

	t.Run("When serving cert files already exist, it should load them and set GetCertificate", func(t *testing.T) {
		g := NewWithT(t)

		dir := t.TempDir()
		writeServingCertFiles(t, dir)

		existingCalled := false
		existing := func(c *tls.Config) {
			existingCalled = true
		}
		tlsOpts := []func(*tls.Config){existing}

		result, err := waitAndConfigureServingCert(t.Context(), dir, tlsOpts, 20*time.Millisecond, time.Second, logr.Discard())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(HaveLen(2))

		cfg := &tls.Config{}
		for _, opt := range result {
			opt(cfg)
		}
		g.Expect(existingCalled).To(BeTrue())
		g.Expect(cfg.GetCertificate).NotTo(BeNil())

		cert, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cert).NotTo(BeNil())
		g.Expect(cert.Certificate).NotTo(BeEmpty())
	})

	t.Run("When serving cert files appear after a delay, it should retry until they are available", func(t *testing.T) {
		g := NewWithT(t)

		dir := t.TempDir()
		_, servingSecret, _, err := webhookcerts.GenerateInitialWebhookCerts("hypershift", "operator")
		g.Expect(err).NotTo(HaveOccurred())

		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = os.WriteFile(filepath.Join(dir, "tls.crt"), servingSecret.Data[corev1.TLSCertKey], 0600)
			_ = os.WriteFile(filepath.Join(dir, "tls.key"), servingSecret.Data[corev1.TLSPrivateKeyKey], 0600)
		}()

		result, err := waitAndConfigureServingCert(t.Context(), dir, nil, 50*time.Millisecond, 2*time.Second, logr.Discard())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(HaveLen(1))

		cfg := &tls.Config{}
		result[0](cfg)
		g.Expect(cfg.GetCertificate).NotTo(BeNil())
		cert, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(cert).NotTo(BeNil())
	})

	t.Run("When serving cert files never appear, it should return a timeout error", func(t *testing.T) {
		g := NewWithT(t)

		dir := t.TempDir()
		result, err := waitAndConfigureServingCert(t.Context(), dir, nil, 20*time.Millisecond, 150*time.Millisecond, logr.Discard())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("serving cert never became available"))
		g.Expect(result).To(BeNil())
	})

	t.Run("When serving cert files are invalid, it should retry until timeout", func(t *testing.T) {
		g := NewWithT(t)

		dir := t.TempDir()
		g.Expect(os.WriteFile(filepath.Join(dir, "tls.crt"), []byte("not-a-cert"), 0600)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(dir, "tls.key"), []byte("not-a-key"), 0600)).To(Succeed())

		result, err := waitAndConfigureServingCert(t.Context(), dir, nil, 20*time.Millisecond, 150*time.Millisecond, logr.Discard())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("serving cert never became available"))
		g.Expect(result).To(BeNil())
	})

	t.Run("When the context is canceled before certs appear, it should return an error", func(t *testing.T) {
		g := NewWithT(t)

		dir := t.TempDir()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		result, err := waitAndConfigureServingCert(ctx, dir, nil, 20*time.Millisecond, time.Second, logr.Discard())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("serving cert never became available"))
		g.Expect(result).To(BeNil())
	})
}

func TestBootstrapAndWaitForServingCert(t *testing.T) {
	t.Parallel()

	const (
		namespace   = "hypershift"
		serviceName = "operator"
	)

	t.Run("When cert dir is empty, it should skip bootstrap and return the original TLS options", func(t *testing.T) {
		g := NewWithT(t)

		existing := func(c *tls.Config) { c.MinVersion = tls.VersionTLS12 }
		tlsOpts := []func(*tls.Config){existing}

		result, err := bootstrapAndWaitForServingCert(t.Context(), nil, namespace, serviceName, "", tlsOpts, time.Millisecond, 50*time.Millisecond, logr.Discard())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(HaveLen(1))
	})

	t.Run("When serving cert secrets are missing, it should create them before waiting for files", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		dir := t.TempDir()

		result, err := bootstrapAndWaitForServingCert(t.Context(), cl, namespace, serviceName, dir, nil, 20*time.Millisecond, 150*time.Millisecond, logr.Discard())
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("serving cert never became available"))
		g.Expect(result).To(BeNil())

		caSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: webhookcerts.CASecretName}, caSecret)).To(Succeed())
		g.Expect(caSecret.Data).NotTo(BeEmpty())

		servingSecret := &corev1.Secret{}
		g.Expect(cl.Get(t.Context(), client.ObjectKey{Namespace: namespace, Name: webhookcerts.ServingCertSecretName}, servingSecret)).To(Succeed())
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSCertKey))
		g.Expect(servingSecret.Data).To(HaveKey(corev1.TLSPrivateKeyKey))
	})

	t.Run("When serving cert secrets are bootstrapped and files exist, it should configure GetCertificate", func(t *testing.T) {
		g := NewWithT(t)

		cl := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		dir := t.TempDir()
		writeServingCertFiles(t, dir)

		result, err := bootstrapAndWaitForServingCert(t.Context(), cl, namespace, serviceName, dir, nil, 20*time.Millisecond, time.Second, logr.Discard())
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(HaveLen(1))

		cfg := &tls.Config{}
		result[0](cfg)
		g.Expect(cfg.GetCertificate).NotTo(BeNil())
	})
}

func writeServingCertFiles(t *testing.T, dir string) {
	t.Helper()
	g := NewWithT(t)

	_, servingSecret, _, err := webhookcerts.GenerateInitialWebhookCerts("hypershift", "operator")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(os.WriteFile(filepath.Join(dir, "tls.crt"), servingSecret.Data[corev1.TLSCertKey], 0600)).To(Succeed())
	g.Expect(os.WriteFile(filepath.Join(dir, "tls.key"), servingSecret.Data[corev1.TLSPrivateKeyKey], 0600)).To(Succeed())
}
