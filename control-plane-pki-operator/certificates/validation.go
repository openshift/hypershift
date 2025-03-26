package certificates

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// SignerClass is a well-known identifier for a certificate signer known to the HostedControlPlane
type SignerClass string

const (
	// CustomerBreakGlassSigner is the signer class used to mint break-glass credentials for customers.
	CustomerBreakGlassSigner SignerClass = "customer-break-glass"
	// SREBreakGlassSigner is the signer class used to mint break-glass credentials for SRE.
	SREBreakGlassSigner SignerClass = "sre-break-glass"
)

func ValidSignerClass(input string) bool {
	switch SignerClass(input) {
	case CustomerBreakGlassSigner, SREBreakGlassSigner:
		return true
	default:
		return false
	}
}

// ValidUsagesFor declares the valid usages for a CertificateSigningRequest, given a signer.
func ValidUsagesFor(signer SignerClass) (required, optional sets.Set[certificatesv1.KeyUsage]) {
	switch signer {
	case CustomerBreakGlassSigner, SREBreakGlassSigner:
		return sets.New[certificatesv1.KeyUsage](certificatesv1.UsageClientAuth),
			sets.New[certificatesv1.KeyUsage](certificatesv1.UsageDigitalSignature, certificatesv1.UsageKeyEncipherment)
	default:
		return sets.Set[certificatesv1.KeyUsage]{}, sets.Set[certificatesv1.KeyUsage]{}
	}
}

// SignerNameForHCP derives a signer name that's unique to this signer class for this specific HostedControlPlane.
func SignerNameForHCP(hcp *hypershiftv1beta1.HostedControlPlane, signer SignerClass) string {
	return fmt.Sprintf("%s/%s.%s", SignerDomain, hcp.Namespace, signer)
}

// SignerNameForHC derives a signer name that's unique to this signer class for this specific HostedControlPlane.
func SignerNameForHC(hc *hypershiftv1beta1.HostedCluster, signer SignerClass) string {
	return fmt.Sprintf("%s/%s.%s", SignerDomain, manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), signer)
}

// SignerDomain is the domain all certificate signers identify under for HyperShift
const SignerDomain string = "hypershift.openshift.io"

func CommonNamePrefix(signer SignerClass) string {
	return fmt.Sprintf("system:%s:", signer)
}

// ValidatorFunc knows how to validate a CertificateSigningRequest
type ValidatorFunc func(csr *certificatesv1.CertificateSigningRequest, x509cr *x509.CertificateRequest) error

// Validator returns a function that validates CertificateSigningRequests
func Validator(hcp *hypershiftv1beta1.HostedControlPlane, signer SignerClass) ValidatorFunc {
	signerName := SignerNameForHCP(hcp, signer)
	requiredUsages, optionalUsages := ValidUsagesFor(signer)
	validUsages := optionalUsages.Union(requiredUsages)
	return func(csr *certificatesv1.CertificateSigningRequest, x509cr *x509.CertificateRequest) error {
		if csr == nil {
			return errors.New("the Kubernetes CertificateSigningRequest object is missing - programmer error")
		}
		if x509cr == nil {
			return errors.New("the x509 CertificateRequest object is missing - programmer error")
		}

		if csr.Spec.SignerName != signerName {
			return fmt.Errorf("signer name %q does not match %q", csr.Spec.SignerName, signerName)
		}

		if prefix := CommonNamePrefix(signer); !strings.HasPrefix(x509cr.Subject.CommonName, prefix) {
			return fmt.Errorf("invalid certificate request: subject CommonName must begin with %q", prefix)
		}

		requestedUsages := sets.New[certificatesv1.KeyUsage](csr.Spec.Usages...)
		if !requestedUsages.IsSuperset(requiredUsages) {
			return fmt.Errorf("missing required usages: %v", requiredUsages.Difference(requestedUsages))
		}
		if !validUsages.IsSuperset(requestedUsages) {
			return fmt.Errorf("invalid usages: %v", requestedUsages.Difference(validUsages))
		}

		if len(x509cr.DNSNames) > 0 {
			return errors.New("invalid certificate request: DNS subjectAltNames are not allowed")
		}
		if len(x509cr.EmailAddresses) > 0 {
			return errors.New("invalid certificate request: Email subjectAltNames are not allowed")
		}
		if len(x509cr.IPAddresses) > 0 {
			return errors.New("invalid certificate request: IP subjectAltNames are not allowed")
		}
		if len(x509cr.URIs) > 0 {
			return errors.New("invalid certificate request: URI subjectAltNames are not allowed")
		}

		return nil
	}
}
