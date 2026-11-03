package webhookcerts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type webhookCertificatePair struct {
	ca      *corev1.Secret
	serving *corev1.Secret
}

func readWebhookCertificatePair(ctx context.Context, c client.Reader, namespace string) (*webhookCertificatePair, error) {
	pair := &webhookCertificatePair{ca: &corev1.Secret{}, serving: &corev1.Secret{}}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: CASecretName}, pair.ca); err != nil {
		return nil, err
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ServingCertSecretName}, pair.serving); err != nil {
		return nil, err
	}
	return pair, nil
}

func generatedPair(t *testing.T) *webhookCertificatePair {
	t.Helper()
	ca, serving, _, err := GenerateInitialWebhookCerts("hypershift", "operator")
	if err != nil {
		t.Fatal(err)
	}
	return &webhookCertificatePair{ca: ca, serving: serving}
}

func assertWebhookPair(t *testing.T, pair *webhookCertificatePair) {
	t.Helper()
	ca, err := certs.PemToCertificate(pair.ca.Data[certs.CASignerCertMapKey])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := certs.PemToCertificate(pair.serving.Data[corev1.TLSCertKey])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(leaf.RawIssuer, ca.RawSubject) {
		t.Fatal("issuer does not match CA subject")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "operator.hypershift.svc", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatal(err)
	}
	candidate := pair.serving.DeepCopy()
	if err := reconcileWebhookServingCert(candidate, pair.ca, "hypershift", "operator"); err != nil {
		t.Fatal(err)
	}
	if !apiequality.Semantic.DeepEqual(candidate.Data, pair.serving.Data) {
		t.Fatal("persisted certificate does not match desired configuration")
	}
}

func testWebhookRotation(t *testing.T, reconcile func(context.Context, client.Client) error) {
	t.Helper()
	for _, scenario := range []string{"unchanged", "same subject different key", "poisoned hash", "reused key different subject", "malformed leaf", "wrong SAN", "wrong usages", "renewal"} {
		t.Run("When certificates are "+scenario+" it should reconcile a stable matching pair", func(t *testing.T) {
			original := generatedPair(t)
			current := original.ca.DeepCopy()
			switch scenario {
			case "same subject different key", "poisoned hash":
				current = generatedPair(t).ca
				if scenario == "poisoned hash" {
					if err := certs.ReconcileSignedCert(original.serving, current, "hypershift-operator", []string{"openshift"}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, corev1.TLSCertKey, corev1.TLSPrivateKeyKey, "", webhookDNSNames("operator", "hypershift"), nil); err != nil {
						t.Fatal(err)
					}
				}
			case "reused key different subject":
				certificate, err := certs.PemToCertificate(current.Data[certs.CASignerCertMapKey])
				if err != nil {
					t.Fatal(err)
				}
				key, err := certs.PemToPrivateKey(current.Data[certs.CASignerKeyMapKey])
				if err != nil {
					t.Fatal(err)
				}
				certificate.Subject.CommonName = "replacement-webhook-ca"
				certificate.RawSubject = nil
				encoded, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key.Public(), key)
				if err != nil {
					t.Fatal(err)
				}
				certificate, err = x509.ParseCertificate(encoded)
				if err != nil {
					t.Fatal(err)
				}
				current.Data[certs.CASignerCertMapKey] = certs.CertToPem(certificate)
			case "malformed leaf":
				original.serving.Data[corev1.TLSCertKey] = []byte("invalid PEM")
			case "wrong SAN":
				if err := reconcileWebhookServingCert(original.serving, current, "hypershift", "old-operator"); err != nil {
					t.Fatal(err)
				}
			case "wrong usages":
				leaf, err := certs.PemToCertificate(original.serving.Data[corev1.TLSCertKey])
				if err != nil {
					t.Fatal(err)
				}
				ca, err := certs.PemToCertificate(current.Data[certs.CASignerCertMapKey])
				if err != nil {
					t.Fatal(err)
				}
				key, err := certs.PemToPrivateKey(current.Data[certs.CASignerKeyMapKey])
				if err != nil {
					t.Fatal(err)
				}
				leaf.KeyUsage = x509.KeyUsageDigitalSignature
				leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
				encoded, err := x509.CreateCertificate(rand.Reader, leaf, ca, leaf.PublicKey, key)
				if err != nil {
					t.Fatal(err)
				}
				leaf, err = x509.ParseCertificate(encoded)
				if err != nil {
					t.Fatal(err)
				}
				original.serving.Data[corev1.TLSCertKey] = certs.CertToPem(leaf)
			case "renewal":
				t.Setenv(certs.CertificateRenewalEnvVar, "1")
			}
			cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(current, original.serving).Build()
			if err := reconcile(t.Context(), cl); err != nil {
				t.Fatal(err)
			}
			if scenario == "renewal" {
				t.Setenv(certs.CertificateRenewalEnvVar, "0.08")
			}
			stored, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil {
				t.Fatal(err)
			}
			assertWebhookPair(t, stored)
			if !apiequality.Semantic.DeepEqual(stored.ca.Data, current.Data) {
				t.Fatal("replaced the authoritative CA")
			}
			unchanged := bytes.Equal(stored.serving.Data[corev1.TLSCertKey], original.serving.Data[corev1.TLSCertKey])
			if unchanged != (scenario == "unchanged") {
				t.Fatalf("unexpected certificate rotation: unchanged=%t", unchanged)
			}
			if err := reconcile(t.Context(), cl); err != nil {
				t.Fatal(err)
			}
			after, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil || !apiequality.Semantic.DeepEqual(stored.ca, after.ca) || !apiequality.Semantic.DeepEqual(stored.serving, after.serving) {
				t.Fatalf("second reconcile was not stable: %v", err)
			}
		})
	}
}

func TestReconcileWebhookServingCert(t *testing.T) {
	reconcile := func(ctx context.Context, c client.Client) error {
		pair, err := readWebhookCertificatePair(ctx, c, "hypershift")
		if err != nil {
			return err
		}
		original := pair.serving.DeepCopy()
		if err := reconcileWebhookServingCert(pair.serving, pair.ca, "hypershift", "operator"); err != nil {
			return err
		}
		if apiequality.Semantic.DeepEqual(original, pair.serving) {
			return nil
		}
		return c.Update(ctx, pair.serving)
	}
	testWebhookRotation(t, reconcile)
	testUnusableWebhookCA(t, reconcile)
	t.Run("When TLS key pair leaf caching is disabled it should still reconcile certificates", func(t *testing.T) {
		t.Setenv("GODEBUG", "x509keypairleaf=0")
		pair := generatedPair(t)
		if err := reconcileWebhookServingCert(pair.serving, pair.ca, "hypershift", "operator"); err != nil {
			t.Fatal(err)
		}
		assertWebhookPair(t, pair)
	})
}

func changeWebhookCA(t *testing.T, ca *corev1.Secret, change func(*x509.Certificate)) {
	t.Helper()
	certificate, err := certs.PemToCertificate(ca.Data[certs.CASignerCertMapKey])
	if err != nil {
		t.Fatal(err)
	}
	key, err := certs.PemToPrivateKey(ca.Data[certs.CASignerKeyMapKey])
	if err != nil {
		t.Fatal(err)
	}
	change(certificate)
	encoded, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	ca.Data[certs.CASignerCertMapKey] = encodedPEMCertificate(t, encoded)
}

func encodedPEMCertificate(t *testing.T, encoded []byte) []byte {
	t.Helper()
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return certs.CertToPem(certificate)
}

func testWebhookCAInitialization(t *testing.T, reconcile func(context.Context, client.Client) error) {
	t.Helper()
	for _, testCase := range []struct {
		name string
		data map[string][]byte
	}{
		{name: "nil data"},
		{name: "empty data", data: map[string][]byte{}},
		{name: "both empty keys", data: map[string][]byte{certs.CASignerCertMapKey: {}, certs.CASignerKeyMapKey: {}}},
		{name: "unrelated data", data: map[string][]byte{"unrelated": []byte("preserved")}},
	} {
		t.Run("When the CA has "+testCase.name+" it should initialize once and preserve unrelated fields", func(t *testing.T) {
			stale := generatedPair(t)
			ca := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift", Labels: map[string]string{"custom": "label"}, Annotations: map[string]string{"custom": "annotation"}},
				Type:       corev1.SecretTypeOpaque,
				Data:       testCase.data,
			}
			cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(ca, stale.serving).Build()
			if err := reconcile(t.Context(), cl); err != nil {
				t.Fatal(err)
			}
			pair, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil {
				t.Fatal(err)
			}
			assertWebhookPair(t, pair)
			if !apiequality.Semantic.DeepEqual(ca.Labels, pair.ca.Labels) || !apiequality.Semantic.DeepEqual(ca.Annotations, pair.ca.Annotations) || !bytes.Equal(ca.Data["unrelated"], pair.ca.Data["unrelated"]) {
				t.Fatal("lost unrelated CA fields")
			}
			if err := reconcile(t.Context(), cl); err != nil {
				t.Fatal(err)
			}
			after, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil || !apiequality.Semantic.DeepEqual(pair.ca, after.ca) || !apiequality.Semantic.DeepEqual(pair.serving, after.serving) {
				t.Fatalf("initialized pair was not stable: %v", err)
			}
		})
	}
}

func testWebhookCAInitializationConflict(t *testing.T, reconcile func(context.Context, client.Client) error, expectConflict bool) {
	t.Helper()
	t.Run("When another writer initializes the empty CA it should preserve the winner", func(t *testing.T) {
		winner, stale := generatedPair(t), generatedPair(t)
		empty := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: CASecretName, Namespace: "hypershift", Labels: map[string]string{"custom": "label"}}}
		base := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(empty, stale.serving).Build()
		injected := false
		cl := interceptor.NewClient(base, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if obj.GetName() == CASecretName && !injected {
				injected = true
				current := &corev1.Secret{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(empty), current); err != nil {
					return err
				}
				current.Data = winner.ca.Data
				if err := c.Update(ctx, current); err != nil {
					return err
				}
			}
			return c.Update(ctx, obj, opts...)
		}})
		err := reconcile(t.Context(), cl)
		if expectConflict {
			if !apierrors.IsConflict(err) {
				t.Fatalf("expected resourceVersion conflict, got %v", err)
			}
			err = reconcile(t.Context(), cl)
		}
		if err != nil {
			t.Fatal(err)
		}
		stored, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
		if err != nil {
			t.Fatal(err)
		}
		if !injected || !apiequality.Semantic.DeepEqual(winner.ca.Data, stored.ca.Data) || stored.ca.Labels["custom"] != "label" {
			t.Fatal("overwrote the winning CA or unrelated metadata")
		}
		assertWebhookPair(t, stored)
	})
}

func testUnusableWebhookCA(t *testing.T, reconcile func(context.Context, client.Client) error) {
	t.Helper()
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, *corev1.Secret)
	}{
		{name: "certificate only", mutate: func(_ *testing.T, ca *corev1.Secret) { delete(ca.Data, certs.CASignerKeyMapKey) }},
		{name: "key only", mutate: func(_ *testing.T, ca *corev1.Secret) { delete(ca.Data, certs.CASignerCertMapKey) }},
		{name: "malformed", mutate: func(_ *testing.T, ca *corev1.Secret) { ca.Data[certs.CASignerCertMapKey] = []byte("invalid PEM") }},
		{name: "mismatched key", mutate: func(t *testing.T, ca *corev1.Secret) {
			ca.Data[certs.CASignerKeyMapKey] = generatedPair(t).ca.Data[certs.CASignerKeyMapKey]
		}},
		{name: "expired", mutate: func(t *testing.T, ca *corev1.Secret) {
			changeWebhookCA(t, ca, func(certificate *x509.Certificate) { certificate.NotAfter = time.Now().Add(-time.Hour) })
		}},
		{name: "not a signer", mutate: func(t *testing.T, ca *corev1.Secret) {
			changeWebhookCA(t, ca, func(certificate *x509.Certificate) { certificate.IsCA = false })
		}},
	} {
		t.Run("When the CA is "+testCase.name+" it should return an error and preserve both Secrets", func(t *testing.T) {
			pair := generatedPair(t)
			testCase.mutate(t, pair.ca)
			cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(pair.ca, pair.serving).Build()
			before, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil {
				t.Fatal(err)
			}
			if err := reconcile(t.Context(), cl); err == nil {
				t.Fatal("accepted unusable CA")
			}
			after, err := readWebhookCertificatePair(t.Context(), cl, "hypershift")
			if err != nil {
				t.Fatal(err)
			}
			if !apiequality.Semantic.DeepEqual(before.ca, after.ca) || !apiequality.Semantic.DeepEqual(before.serving, after.serving) {
				t.Fatal("modified authoritative material on CA error")
			}
		})
	}
}

func testWebhookBootstrapReadErrors(t *testing.T) {
	t.Helper()
	readError := errors.New("API read unavailable")
	for _, testCase := range []struct {
		name      string
		intercept interceptor.Funcs
	}{
		{name: "CA read fails", intercept: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == CASecretName {
				return readError
			}
			return c.Get(ctx, key, obj, opts...)
		}}},
		{name: "leaf read fails", intercept: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == ServingCertSecretName {
				return readError
			}
			return c.Get(ctx, key, obj, opts...)
		}}},
	} {
		t.Run("When "+testCase.name+" it should propagate the error without writes", func(t *testing.T) {
			pair := generatedPair(t)
			base := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(pair.ca, pair.serving).Build()
			before, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
			if err != nil {
				t.Fatal(err)
			}
			testCase.intercept.Create = func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				t.Fatal("unexpected Create")
				return nil
			}
			testCase.intercept.Update = func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				t.Fatal("unexpected Update")
				return nil
			}
			cl := interceptor.NewClient(base, testCase.intercept)
			if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); !errors.Is(err, readError) {
				t.Fatalf("lost read error: %v", err)
			}
			after, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
			if err != nil || !apiequality.Semantic.DeepEqual(before.ca, after.ca) || !apiequality.Semantic.DeepEqual(before.serving, after.serving) {
				t.Fatalf("changed Secrets: %v", err)
			}
		})
	}
	t.Run("When the CA reread fails after conflict it should propagate the error without replaying the leaf", func(t *testing.T) {
		current, stale := generatedPair(t), generatedPair(t)
		base := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(current.ca, stale.serving).Build()
		reads, updates := 0, 0
		cl := interceptor.NewClient(base, interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if key.Name == CASecretName {
					reads++
					if reads == 2 {
						return readError
					}
				}
				return c.Get(ctx, key, obj, opts...)
			},
			Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
				updates++
				return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, obj.GetName(), errors.New("competing leaf update"))
			},
		})
		if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); !errors.Is(err, readError) || updates != 1 {
			t.Fatalf("error=%v updates=%d", err, updates)
		}
		stored, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
		if err != nil {
			t.Fatal(err)
		}
		if !apiequality.Semantic.DeepEqual(current.ca.Data, stored.ca.Data) || !apiequality.Semantic.DeepEqual(stale.serving.Data, stored.serving.Data) {
			t.Fatal("changed authoritative material after read failure")
		}
	})
}

func testWebhookBootstrap(t *testing.T) {
	t.Helper()
	testWebhookBootstrapReadErrors(t)
	t.Run("When another initializer wins CA creation it should reread and sign with the winning CA", func(t *testing.T) {
		winner := generatedPair(t)
		base := fake.NewClientBuilder().WithScheme(newScheme(t)).Build()
		injected := false
		cl := interceptor.NewClient(base, interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == CASecretName && !injected {
				injected = true
				if err := c.Create(ctx, winner.ca); err != nil {
					return err
				}
			}
			return c.Create(ctx, obj, opts...)
		}})
		if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); err != nil {
			t.Fatal(err)
		}
		stored, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
		if err != nil {
			t.Fatal(err)
		}
		if !injected || !apiequality.Semantic.DeepEqual(winner.ca.Data, stored.ca.Data) {
			t.Fatal("lost winning CA")
		}
		assertWebhookPair(t, stored)
	})
	t.Run("When another initializer creates a stale leaf it should reread and repair it", func(t *testing.T) {
		current, stale := generatedPair(t), generatedPair(t)
		base := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(current.ca).Build()
		injected := false
		cl := interceptor.NewClient(base, interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == ServingCertSecretName && !injected {
				injected = true
				if err := c.Create(ctx, stale.serving); err != nil {
					return err
				}
			}
			return c.Create(ctx, obj, opts...)
		}})
		if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); err != nil {
			t.Fatal(err)
		}
		stored, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
		if err != nil {
			t.Fatal(err)
		}
		if !injected || !apiequality.Semantic.DeepEqual(current.ca.Data, stored.ca.Data) {
			t.Fatal("replaced CA")
		}
		assertWebhookPair(t, stored)
	})
	t.Run("When a leaf conflict accompanies CA replacement it should discard the old candidate", func(t *testing.T) {
		original, winner := generatedPair(t), generatedPair(t)
		stale := generatedPair(t).serving
		base := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(original.ca, stale).Build()
		injected := false
		cl := interceptor.NewClient(base, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if obj.GetName() == ServingCertSecretName && !injected {
				injected = true
				ca := &corev1.Secret{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(original.ca), ca); err != nil {
					return err
				}
				ca.Data = winner.ca.Data
				if err := c.Update(ctx, ca); err != nil {
					return err
				}
				return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, obj.GetName(), errors.New("competing leaf update"))
			}
			return c.Update(ctx, obj, opts...)
		}})
		if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); err != nil {
			t.Fatal(err)
		}
		stored, err := readWebhookCertificatePair(t.Context(), base, "hypershift")
		if err != nil {
			t.Fatal(err)
		}
		if !injected || !apiequality.Semantic.DeepEqual(winner.ca.Data, stored.ca.Data) {
			t.Fatal("lost replacement CA")
		}
		assertWebhookPair(t, stored)
	})
	t.Run("When leaf conflicts persist it should stop after five attempts", func(t *testing.T) {
		current, stale := generatedPair(t), generatedPair(t)
		attempts := 0
		cl := fake.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(current.ca, stale.serving).WithInterceptorFuncs(interceptor.Funcs{Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
			attempts++
			return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, obj.GetName(), errors.New("persistent conflict"))
		}}).Build()
		if err := EnsureWebhookCerts(t.Context(), cl, "hypershift", "operator"); !apierrors.IsConflict(err) || attempts != 5 {
			t.Fatalf("error=%v attempts=%d", err, attempts)
		}
	})
}
