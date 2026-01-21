package pki

import (
	"testing"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

// TestReconcileAWSEBSCsiDriverControllerMetricsServingCertSecret verifies that the AWS EBS CSI driver
// controller metrics serving certificate is properly generated with the correct DNS names.
func TestReconcileAWSEBSCsiDriverControllerMetricsServingCertSecret(t *testing.T) {
	namespace := "test-namespace"

	ownerRef := config.OwnerRef{
		Reference: &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "HostedControlPlane",
			Name:       "test-hcp",
			UID:        types.UID("test-uid"),
			Controller: ptr.To(true),
		},
	}

	// Create a self-signed CA
	ca := &corev1.Secret{}
	ca.Name = "test-ca"
	ca.Namespace = namespace
	err := reconcileSelfSignedCA(ca, ownerRef, "test-org", "test-ca")
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}

	// Create the CSI driver controller metrics serving cert
	secret := &corev1.Secret{}
	secret.Name = "aws-ebs-csi-driver-controller-metrics-serving-cert"
	secret.Namespace = namespace

	err = ReconcileAWSEBSCsiDriverControllerMetricsServingCertSecret(secret, ca, ownerRef)
	if err != nil {
		t.Fatalf("failed to reconcile cert: %v", err)
	}

	// Verify the certificate was created
	if secret.Data == nil {
		t.Fatal("secret data is nil")
	}

	if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
		t.Fatal("secret missing tls.crt")
	}

	if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
		t.Fatal("secret missing tls.key")
	}

	// Parse and verify the certificate
	cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	// Expected DNS names
	expectedDNSNames := map[string]bool{
		"aws-ebs-csi-driver-controller-metrics." + namespace + ".svc":               true,
		"aws-ebs-csi-driver-controller-metrics." + namespace + ".svc.cluster.local": true,
		"aws-ebs-csi-driver-controller-metrics":                                     true,
		"localhost":                                                                 true,
	}

	// Verify all expected DNS names are present
	if len(cert.DNSNames) != len(expectedDNSNames) {
		t.Errorf("expected %d DNS names, got %d", len(expectedDNSNames), len(cert.DNSNames))
	}

	for _, name := range cert.DNSNames {
		if !expectedDNSNames[name] {
			t.Errorf("unexpected DNS name: %s", name)
		}
	}

	// Verify common name
	expectedCN := "aws-ebs-csi-driver-controller-metrics"
	if cert.Subject.CommonName != expectedCN {
		t.Errorf("expected CN %s, got %s", expectedCN, cert.Subject.CommonName)
	}

	// Verify organization
	if len(cert.Subject.Organization) == 0 || cert.Subject.Organization[0] != "openshift" {
		t.Errorf("expected Organization [openshift], got %v", cert.Subject.Organization)
	}
}
