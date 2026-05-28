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

	oldCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	newCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	controlPlaneSigner := mustGenerateCA(t, "kube-control-plane-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(controlPlaneSigner) + string(oldCA),
		},
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			totalClientCADirectCertsAnnotation: encodeCertificateIdentities(parsePEMCertificates(append(append([]byte{}, controlPlaneSigner...), oldCA...))),
		}},
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
		"kube-csr-signer":           false,
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
		t.Errorf("expected both same-subject direct CAs and the control-plane CA, got %d: %v", countCertsInPEM(bundle), cns)
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
	var annotation, bundle string
	for i := 0; i < 5; i++ {
		err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer)
		if err != nil {
			t.Fatalf("iteration %d: ReconcileTotalClientCA failed: %v", i, err)
		}
		currentBundle := cm.Data[certs.CASignerCertMapKey]
		currentAnnotation := cm.Annotations[totalClientCADirectCertsAnnotation]
		if i == 0 {
			annotation = currentAnnotation
			bundle = currentBundle
		} else if currentAnnotation != annotation {
			t.Errorf("iteration %d changed direct certificate provenance annotation", i)
		} else if currentBundle != bundle {
			t.Errorf("iteration %d changed serialized CA bundle", i)
		}
	}

	bundle = cm.Data[certs.CASignerCertMapKey]
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

func TestReconcileTotalClientCA_SkipsMalformedPreviousCertificate(t *testing.T) {
	t.Parallel()
	now := time.Now()

	malformedCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a certificate")})
	currentCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(malformedCA),
		},
	}
	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: currentCA,
		},
	}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer); err != nil {
		t.Fatalf("reconcile with malformed previous certificate failed: %v", err)
	}
	if got := countCertsInPEM(cm.Data[certs.CASignerCertMapKey]); got != 1 {
		t.Fatalf("expected only the valid current certificate, got %d: %v", got, certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
	}
}

func TestReconcileTotalClientCA_EmptyAdditionalSource(t *testing.T) {
	t.Parallel()
	now := time.Now()

	currentCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	cm := &corev1.ConfigMap{}
	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: currentCA,
		},
	}
	additional := []*corev1.ConfigMap{{}}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, signer); err != nil {
		t.Fatalf("reconcile with empty additional source failed: %v", err)
	}
	if got := countCertsInPEM(cm.Data[certs.CASignerCertMapKey]); got != 1 {
		t.Fatalf("expected only the current direct certificate, got %d: %v", got, certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
	}
}

func TestReconcileTotalClientCA_WithAdditionalConfigMaps(t *testing.T) {
	t.Parallel()
	now := time.Now()

	signerCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	additionalCA := mustGenerateCA(t, "ocp-additional-ca", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	oldSignerCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-48*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(oldSignerCA) + string(additionalCA),
		},
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			totalClientCADirectCertsAnnotation: encodeCertificateIdentities(parsePEMCertificates(oldSignerCA)),
		}},
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
		"kube-csr-signer":   false,
		"ocp-additional-ca": false,
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

func TestReconcileTotalClientCA_DoesNotRetainRemovedAdditionalCAs(t *testing.T) {
	t.Parallel()
	now := time.Now()

	directCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	additionalCA := mustGenerateCA(t, "break-glass-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{}
	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: directCA,
		},
	}
	additional := []*corev1.ConfigMap{{
		Data: map[string]string{
			certs.OCPCASignerCertMapKey: string(additionalCA),
		},
	}}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, signer); err != nil {
		t.Fatalf("initial ReconcileTotalClientCA failed: %v", err)
	}
	if countCertsInPEM(cm.Data[certs.CASignerCertMapKey]) != 2 {
		t.Fatalf("expected direct and additional CAs after initial reconcile, got: %v", certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
	}

	delete(additional[0].Data, certs.OCPCASignerCertMapKey)
	if err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, signer); err != nil {
		t.Fatalf("reconcile after additional CA removal failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	if countCertsInPEM(bundle) != 1 {
		t.Fatalf("expected only the direct CA after the additional source was pruned, got: %v", certCNsInPEM(bundle))
	}
	if got := certCNsInPEM(bundle); len(got) != 1 || got[0] != "kube-csr-signer" {
		t.Errorf("expected the direct CA to remain after pruning the additional source, got: %v", got)
	}
}

func TestReconcileTotalClientCA_LegacyAggregateRetainsUnambiguousDirectCA(t *testing.T) {
	t.Parallel()
	now := time.Now()

	oldDirectCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-48*time.Hour), now.Add(8760*time.Hour))
	oldAdditionalCA := mustGenerateCA(t, "old-additional-ca", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	newDirectCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(oldDirectCA) + string(oldAdditionalCA),
		},
	}
	directSigner := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: newDirectCA,
		},
	}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, directSigner); err != nil {
		t.Fatalf("legacy direct rotation reconcile failed: %v", err)
	}

	parsed := parsePEMCertificates([]byte(cm.Data[certs.CASignerCertMapKey]))
	if len(parsed) != 2 {
		t.Fatalf("expected current and unambiguous historical direct CAs, got %d: %v", len(parsed), certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
	}
	identities := make(map[string]struct{}, len(parsed))
	for _, cert := range parsed {
		identities[certificateIdentity(cert)] = struct{}{}
	}
	if _, ok := identities[certificateIdentity(parsePEMCertificates(oldDirectCA)[0])]; !ok {
		t.Errorf("expected unambiguous historical direct CA to be retained")
	}
	if _, ok := identities[certificateIdentity(parsePEMCertificates(oldAdditionalCA)[0])]; ok {
		t.Errorf("did not expect unrelated historical additional CA to be retained")
	}
}

func TestReconcileTotalClientCA_ProvenanceDisambiguatesSameSubjectSources(t *testing.T) {
	t.Parallel()
	now := time.Now()

	oldDirectCA := mustGenerateCA(t, "shared-signer", now.Add(-48*time.Hour), now.Add(8760*time.Hour))
	newDirectCA := mustGenerateCA(t, "shared-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	additionalCA := mustGenerateCA(t, "shared-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{}
	directSigner := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: oldDirectCA,
		},
	}
	additional := []*corev1.ConfigMap{{
		Data: map[string]string{
			certs.OCPCASignerCertMapKey: string(additionalCA),
		},
	}}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, directSigner); err != nil {
		t.Fatalf("initial ReconcileTotalClientCA failed: %v", err)
	}
	if cm.Annotations[totalClientCADirectCertsAnnotation] == "" {
		t.Fatal("expected direct certificate provenance annotation after initial reconcile")
	}

	directSigner.Data[certs.CASignerCertMapKey] = newDirectCA
	delete(additional[0].Data, certs.OCPCASignerCertMapKey)
	if err := ReconcileTotalClientCA(cm, newOwnerRef(), additional, directSigner); err != nil {
		t.Fatalf("reconcile after additional CA removal failed: %v", err)
	}

	bundle := cm.Data[certs.CASignerCertMapKey]
	parsed := parsePEMCertificates([]byte(bundle))
	if len(parsed) != 2 {
		t.Fatalf("expected both direct same-subject CAs and no removed additional CA, got: %v", certCNsInPEM(bundle))
	}
	identities := make(map[string]struct{}, len(parsed))
	for _, cert := range parsed {
		identities[certificateIdentity(cert)] = struct{}{}
	}
	for name, ca := range map[string][]byte{
		"old direct": oldDirectCA,
		"new direct": newDirectCA,
	} {
		if _, ok := identities[certificateIdentity(parsePEMCertificates(ca)[0])]; !ok {
			t.Errorf("expected %s certificate in bundle", name)
		}
	}
	if _, ok := identities[certificateIdentity(parsePEMCertificates(additionalCA)[0])]; ok {
		t.Errorf("did not expect removed additional certificate in bundle")
	}
}

func TestReconcileTotalClientCA_ProvenanceMetadata(t *testing.T) {
	t.Parallel()
	now := time.Now()
	oldCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	newCA := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	oldCert := parsePEMCertificates(oldCA)[0]
	newCert := parsePEMCertificates(newCA)[0]

	testCases := []struct {
		name       string
		annotation string
		wantCount  int
	}{
		{name: "empty", annotation: "", wantCount: 1},
		{name: "malformed", annotation: "not-a-fingerprint", wantCount: 1},
		{name: "unknown", annotation: strings.Repeat("0", 64), wantCount: 1},
		{name: "known direct certificate", annotation: certificateIdentity(oldCert), wantCount: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
					totalClientCADirectCertsAnnotation: testCase.annotation,
				}},
				Data: map[string]string{
					certs.CASignerCertMapKey: string(oldCA),
				},
			}
			signer := &corev1.Secret{
				Data: map[string][]byte{
					certs.CASignerCertMapKey: newCA,
				},
			}

			if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer); err != nil {
				t.Fatalf("ReconcileTotalClientCA failed: %v", err)
			}
			if got := countCertsInPEM(cm.Data[certs.CASignerCertMapKey]); got != testCase.wantCount {
				t.Fatalf("expected %d certificates, got %d: %v", testCase.wantCount, got, certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
			}

			identities := parseCertificateIdentities(cm.Annotations[totalClientCADirectCertsAnnotation])
			if _, ok := identities[certificateIdentity(newCert)]; !ok {
				t.Errorf("expected current direct certificate identity in updated annotation")
			}
			if testCase.wantCount == 2 {
				if _, ok := identities[certificateIdentity(oldCert)]; !ok {
					t.Errorf("expected known historical direct certificate identity in updated annotation")
				}
			}
		})
	}
}

func TestReconcileTotalClientCA_LegacyAggregateFailsClosedAfterAdditionalRemoval(t *testing.T) {
	t.Parallel()
	now := time.Now()

	oldDirectCA := mustGenerateCA(t, "shared-signer", now.Add(-48*time.Hour), now.Add(8760*time.Hour))
	oldAdditionalCA := mustGenerateCA(t, "shared-signer", now.Add(-24*time.Hour), now.Add(8760*time.Hour))
	newDirectCA := mustGenerateCA(t, "shared-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(oldDirectCA) + string(oldAdditionalCA),
		},
	}
	directSigner := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: newDirectCA,
		},
	}

	if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, directSigner); err != nil {
		t.Fatalf("legacy collision reconcile failed: %v", err)
	}
	if got := countCertsInPEM(cm.Data[certs.CASignerCertMapKey]); got != 1 {
		t.Fatalf("expected only the current direct CA for ambiguous legacy history, got %d: %v", got, certCNsInPEM(cm.Data[certs.CASignerCertMapKey]))
	}
}

func TestReconcileTotalClientCA_RefusesEmptyBundle(t *testing.T) {
	t.Parallel()
	now := time.Now()

	expiredCA := mustGenerateCA(t, "kube-csr-signer-expired", now.Add(-48*time.Hour), now.Add(-1*time.Hour))

	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(expiredCA),
		},
	}

	signer := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: expiredCA,
		},
	}

	err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signer)
	if err == nil {
		t.Fatal("expected error when all certificates are expired, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to write empty CA bundle") {
		t.Errorf("expected empty bundle error, got: %v", err)
	}
}

func TestReconcileTotalClientCA_SequentialRotation(t *testing.T) {
	t.Parallel()
	now := time.Now()

	caA := mustGenerateCA(t, "kube-csr-signer", now.Add(-72*time.Hour), now.Add(8760*time.Hour))
	caB := mustGenerateCA(t, "kube-csr-signer", now.Add(-48*time.Hour), now.Add(8760*time.Hour))
	caC := mustGenerateCA(t, "kube-csr-signer", now.Add(-1*time.Hour), now.Add(8760*time.Hour))
	controlPlane := mustGenerateCA(t, "kube-control-plane-signer", now.Add(-72*time.Hour), now.Add(8760*time.Hour))

	// Start with CA_A as the active signer
	cm := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(controlPlane) + string(caA),
		},
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			totalClientCADirectCertsAnnotation: encodeCertificateIdentities(parsePEMCertificates(append(append([]byte{}, controlPlane...), caA...))),
		}},
	}

	// Rotation 1: CA_A → CA_B
	signerB := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: append(controlPlane, caB...),
		},
	}
	if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signerB); err != nil {
		t.Fatalf("rotation A→B failed: %v", err)
	}

	bundleAfterB := cm.Data[certs.CASignerCertMapKey]
	cnsAfterB := certCNsInPEM(bundleAfterB)
	for _, want := range []string{"kube-csr-signer", "kube-control-plane-signer"} {
		found := false
		for _, cn := range cnsAfterB {
			if cn == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("after A→B rotation: expected %q in bundle, got: %v", want, cnsAfterB)
		}
	}
	if countCertsInPEM(bundleAfterB) != 3 {
		t.Errorf("after A→B rotation: expected 3 certificates, got %d: %v", countCertsInPEM(bundleAfterB), cnsAfterB)
	}

	// Rotation 2: CA_B → CA_C (CA_A should still be preserved from prior bundle)
	signerC := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: append(controlPlane, caC...),
		},
	}
	if err := ReconcileTotalClientCA(cm, newOwnerRef(), nil, signerC); err != nil {
		t.Fatalf("rotation B→C failed: %v", err)
	}

	bundleAfterC := cm.Data[certs.CASignerCertMapKey]
	cnsAfterC := certCNsInPEM(bundleAfterC)
	for _, want := range []string{"kube-csr-signer", "kube-control-plane-signer"} {
		found := false
		for _, cn := range cnsAfterC {
			if cn == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("after B→C rotation: expected %q in bundle, got: %v", want, cnsAfterC)
		}
	}

	if countCertsInPEM(bundleAfterC) != 4 {
		t.Errorf("expected 4 certs after sequential rotation, got %d: %v", countCertsInPEM(bundleAfterC), cnsAfterC)
	}
}
