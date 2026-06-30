package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func generateSelfSignedCA(cn string, notBefore, notAfter time.Time) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, OrganizationalUnit: []string{"openshift"}},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func mustGenerateCA(t *testing.T, cn string, notBefore, notAfter time.Time) []byte {
	t.Helper()
	data, err := generateSelfSignedCA(cn, notBefore, notAfter)
	if err != nil {
		t.Fatalf("failed to generate CA %q: %v", cn, err)
	}
	return data
}

func countCertsInPEM(data string) int {
	return len(parsePEMCertificates([]byte(data)))
}

func certCNsInPEM(data string) []string {
	certs := parsePEMCertificates([]byte(data))
	names := make([]string, len(certs))
	for i, c := range certs {
		names[i] = c.Subject.CommonName
	}
	return names
}

func newOwnerRef() config.OwnerRef {
	return config.OwnerRef{
		Reference: &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "test",
			UID:        "test-uid",
		},
	}
}

func TestReconcileTotalClientCA_PreservesPreviousCAsOnRotation(t *testing.T) {
	t.Parallel()
	now := time.Now()

	oldCA := mustGenerateCA(t, "kube-csr-signer-old", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	newCA := mustGenerateCA(t, "kube-csr-signer-new", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	controlPlaneSigner := mustGenerateCA(t, "kube-control-plane-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(controlPlaneSigner) + string(oldCA),
		},
	}

	signerWithNewCA := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: append(controlPlaneSigner, newCA...),
		},
	}

	err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signerWithNewCA)
	if err != nil {
		t.Fatalf("ReconcileTotalClientCA failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	cns := certCNsInPEM(bundle)

	wantCNs := map[string]bool{
		"kube-control-plane-signer": false,
		"kube-csr-signer-new":      false,
		"kube-csr-signer-old":      false,
	}
	for _, cn := range cns {
		if _, ok := wantCNs[cn]; ok {
			wantCNs[cn] = true
		}
	}
	for cn, found := range wantCNs {
		if !found {
			t.Errorf("expected CA %q in bundle but it was missing; got CNs: %v", cn, cns)
		}
	}
}

func TestReconcileTotalClientCA_EvictsExpiredCAs(t *testing.T) {
	t.Parallel()
	now := time.Now()

	expiredCA := mustGenerateCA(t, "kube-csr-signer-expired", now.Add(-48*time.Hour), now.Add(-1*time.Hour))
	currentCA := mustGenerateCA(t, "kube-csr-signer-current", now.Add(-1*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(expiredCA) + string(currentCA),
		},
	}

	signerWithCurrentCA := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: currentCA,
		},
	}

	err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signerWithCurrentCA)
	if err != nil {
		t.Fatalf("ReconcileTotalClientCA failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	cns := certCNsInPEM(bundle)

	if countCertsInPEM(bundle) != 1 {
		t.Errorf("expected 1 cert in bundle (expired should be evicted), got %d: %v", countCertsInPEM(bundle), cns)
	}
	for _, cn := range cns {
		if strings.Contains(cn, "expired") {
			t.Errorf("expired CA %q should have been evicted from bundle", cn)
		}
	}
}

func TestReconcileTotalClientCA_NoDuplicatesOnSteadyState(t *testing.T) {
	t.Parallel()
	now := time.Now()

	ca1 := mustGenerateCA(t, "kube-csr-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	ca2 := mustGenerateCA(t, "kube-control-plane-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))

	initialBundle := string(ca1) + string(ca2)
	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: initialBundle,
		},
	}

	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: append(ca1, ca2...),
		},
	}

	// Run reconcile multiple times
	for i := 0; i < 5; i++ {
		err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer)
		if err != nil {
			t.Fatalf("iteration %d: ReconcileTotalClientCA failed: %v", i, err)
		}
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	if countCertsInPEM(bundle) != 2 {
		t.Errorf("expected 2 certs in bundle after repeated reconcile, got %d: %v",
			countCertsInPEM(bundle), certCNsInPEM(bundle))
	}
}

func TestReconcileTotalClientCA_EmptyPreviousBundle(t *testing.T) {
	t.Parallel()
	now := time.Now()

	ca := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{},
	}

	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: ca,
		},
	}

	err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer)
	if err != nil {
		t.Fatalf("ReconcileTotalClientCA failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	if countCertsInPEM(bundle) != 1 {
		t.Errorf("expected 1 cert in bundle for fresh ConfigMap, got %d", countCertsInPEM(bundle))
	}
}

func TestReconcileTotalClientCA_WithAdditionalConfigMaps(t *testing.T) {
	t.Parallel()
	now := time.Now()

	signerCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	additionalCA := mustGenerateCA(t, "ocp-additional-ca", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	oldSignerCA := mustGenerateCA(t, "kube-csr-signer-old", now.Add(-48*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(oldSignerCA) + string(additionalCA),
		},
	}

	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: signerCA,
		},
	}

	additional := []*corev1.ConfigMap{
		{Data: map[string]string{certs.OCPCASignerCertMapKey: string(additionalCA)}},
	}

	err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, signer)
	if err != nil {
		t.Fatalf("ReconcileTotalClientCA failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	cns := certCNsInPEM(bundle)

	wantCNs := map[string]bool{
		"kube-csr-signer":     false,
		"ocp-additional-ca":   false,
		"kube-csr-signer-old": false,
	}
	for _, cn := range cns {
		if _, ok := wantCNs[cn]; ok {
			wantCNs[cn] = true
		}
	}
	for cn, found := range wantCNs {
		if !found {
			t.Errorf("expected CA %q in bundle but it was missing; got CNs: %v", cn, cns)
		}
	}

	if countCertsInPEM(bundle) != 3 {
		t.Errorf("expected 3 certs in bundle, got %d: %v", countCertsInPEM(bundle), cns)
	}
}
