package validations

import (
	"context"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	supportpki "github.com/openshift/hypershift/support/pki"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KASServerPrivateCertSecretName = "kas-server-private-crt"
	KASServerCertSecretName        = "kas-server-crt"
)

func ValidateOCPAPIServerSANs(ctx context.Context, hc *hyperv1.HostedCluster, client client.Client) field.ErrorList {
	var (
		errs              field.ErrorList
		err               error
		entryCertDNSNames = make([]string, 0)
		entryCertIPs      = make([]string, 0)
		kasNames          = make([]string, 0)
		kasIPs            = make([]string, 0)
		log               = ctrl.LoggerFrom(ctx)
	)

	// Only validate if PKI is being reconciled by Hypershift
	if _, exists := hc.Annotations[hyperv1.DisablePKIReconciliationAnnotation]; exists {
		return errs
	}

	// At this point, maybe the HCP is not there yet
	if hc.Spec.Configuration != nil && hc.Spec.Configuration.APIServer != nil && hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates != nil {
		for _, cert := range hc.Spec.Configuration.APIServer.ServingCerts.NamedCertificates {
			entryCertDNSNames = append(entryCertDNSNames, cert.Names...)
			if len(cert.ServingCertificate.Name) > 0 {
				secret := &corev1.Secret{}
				err = client.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: cert.ServingCertificate.Name}, secret)
				if err != nil {
					errs = append(errs, field.Invalid(field.NewPath("NamedCertificates get secret"), cert.ServingCertificate.Name, err.Error()))
					return errs
				}
				entryCertDNSNames, entryCertIPs, err = getSANsFromSecretCert(entryCertDNSNames, entryCertIPs, secret)
				if err != nil {
					errs = append(errs, field.Invalid(field.NewPath("KAS TLS private cert decrypt"), KASServerPrivateCertSecretName, err.Error()))
					return errs
				}
			}
		}

		if _, exists := hc.Annotations[hyperv1.SkipKASConflicSANValidation]; exists {
			log.Info("Skipping KAS certificate SANs validation due to annotation", "annotation", hyperv1.SkipKASConflicSANValidation)
			return errs
		}

		if hc.Spec.Platform.AWS != nil && (hc.Spec.Platform.AWS.EndpointAccess == hyperv1.Private || hc.Spec.Platform.AWS.EndpointAccess == hyperv1.PublicAndPrivate) {
			log.Info("Skipping KAS certificate SANs validation due to AWS endpoint access value", "endpointAccess", hc.Spec.Platform.AWS.EndpointAccess)
			return errs
		}

		kasNames, kasIPs, err = supportpki.GetKASServerCertificatesSANs("", fmt.Sprintf("api.%s.hypershift.local", hc.Name), []string{}, "")
		if err != nil {
			errs = append(errs, field.Invalid(field.NewPath("Hypershift KAS SANs"), entryCertDNSNames, err.Error()))
		}

		if err := checkConflictingSANs(entryCertDNSNames, kasNames, "DNS names"); err != nil {
			errs = append(errs, field.Invalid(field.NewPath("conflicting entries with KAS SANs"), entryCertDNSNames, err.Error()))
			return errs
		}

		if err := checkConflictingSANs(entryCertIPs, kasIPs, "IP addresses"); err != nil {
			errs = append(errs, field.Invalid(field.NewPath("conflicting entries with KAS SANs"), entryCertIPs, err.Error()))
			return errs
		}
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)

	// Check the KAS TLS private secret
	kasServerPrivateSecret := &corev1.Secret{}
	err = client.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: KASServerPrivateCertSecretName}, kasServerPrivateSecret)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, field.Invalid(field.NewPath("KAS TLS secret"), "error grabbing KAS TLS secret", err.Error()))
		}
		// return early, we can assume that the KAS is not there yet
		return errs
	}
	kasNames, kasIPs, err = getSANsFromSecretCert(kasNames, kasIPs, kasServerPrivateSecret)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("KAS TLS cert decrypt"), KASServerPrivateCertSecretName, err.Error()))
	}

	// Check the KAS TLS certificate secret
	kasServerCertSecret := &corev1.Secret{}
	err = client.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: KASServerCertSecretName}, kasServerCertSecret)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			errs = append(errs, field.Invalid(field.NewPath("KAS TLS secret"), "error grabbing KAS TLS secret", err.Error()))
		}
		// return early, we can assume that the KAS is not there yet
		return errs
	}

	kasNames, kasIPs, err = getSANsFromSecretCert(kasNames, kasIPs, kasServerCertSecret)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("KAS TLS cert decrypt"), KASServerCertSecretName, err.Error()))
	}

	if err := checkConflictingSANs(entryCertDNSNames, kasNames, "DNS names"); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("custom serving cert"), entryCertDNSNames, err.Error()))
		return errs
	}

	if err := checkConflictingSANs(entryCertIPs, kasIPs, "IP addresses"); err != nil {
		errs = append(errs, field.Invalid(field.NewPath("custom serving cert"), entryCertIPs, err.Error()))
		return errs
	}

	return errs
}

func getSANsFromSecretCert(entryCertDNSNames []string, entryCertIPs []string, secretCert *corev1.Secret) ([]string, []string, error) {
	if secretCert == nil || secretCert.Data == nil || len(secretCert.Data["tls.crt"]) == 0 {
		return nil, nil, fmt.Errorf("TLS secret or certificate entries are empty")
	}

	// Try to parse the certificate as PEM
	block, _ := pem.Decode(secretCert.Data["tls.crt"])
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode PEM block from certificate")
	}

	certSANsDNS, certSANsIPs, err := supportpki.GetSANsFromCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("error decrypting TLS certificate: %w", err)
	}

	tempEntryCertDNSNames := appendEntriesIfNotExists(entryCertDNSNames, certSANsDNS)
	tempEntryCertIPs := appendEntriesIfNotExists(entryCertIPs, certSANsIPs)

	return tempEntryCertDNSNames, tempEntryCertIPs, nil
}

func appendEntriesIfNotExists(slice []string, entries []string) []string {
	for _, entry := range entries {
		if !slices.Contains(slice, entry) {
			slice = append(slice, entry)
		}
	}
	return slice
}

// isDNSNameMatch checks if a DNS name matches a pattern, supporting wildcards.
// Wildcards are only allowed at the beginning of a domain and can only match one label.
// Examples:
// - "*.example.com" matches "sub.example.com" but not "sub.sub.example.com"
// - "*.foo.bar.com" matches "baz.foo.bar.com" but not "qux.baz.foo.bar.com"
func isDNSNameMatch(name, pattern string) bool {
	// Early return for exact matches
	if name == pattern {
		return true
	}

	// Early return for non-wildcard patterns
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}

	// Extract the domain part after the wildcard
	wildcardDomain := pattern[2:] // Remove "*."

	// Early return if name doesn't end with the wildcard domain
	if !strings.HasSuffix(name, wildcardDomain) {
		return false
	}

	// Split both names into labels for comparison
	nameLabels := strings.Split(name, ".")
	wildcardLabels := strings.Split(wildcardDomain, ".")

	// Wildcard can only match one label, so name must have exactly one more label
	if len(nameLabels) != len(wildcardLabels)+1 {
		return false
	}

	// Verify that all wildcard labels match the corresponding name labels
	// Skip the first name label (which is what the wildcard matches)
	return slices.Equal(nameLabels[1:], wildcardLabels)
}

// isDNSNameConflicting checks if two DNS names conflict, considering wildcards.
// A conflict occurs if either name matches the other pattern.
func isDNSNameConflicting(name1, name2 string) bool {
	return isDNSNameMatch(name1, name2) || isDNSNameMatch(name2, name1)
}

func checkConflictingSANs(customEntries []string, kasSANEntries []string, entryType string) error {
	for _, customEntry := range customEntries {
		// Check for exact matches first
		if slices.Contains(kasSANEntries, customEntry) {
			return fmt.Errorf("conflicting %s found in KAS SANs. kasEntries: %s, customEntry: %s. The configuration is invalid because the custom %s is conflicting with the KAS SANs", entryType, kasSANEntries, customEntry, entryType)
		}

		// Check for wildcard matches
		for _, kasEntry := range kasSANEntries {
			if isDNSNameConflicting(customEntry, kasEntry) {
				return fmt.Errorf("conflicting %s found in KAS SANs. kasEntry: %s, customEntry: %s. The configuration is invalid because the custom %s is conflicting with the KAS SANs", entryType, kasEntry, customEntry, entryType)
			}
		}
	}
	return nil
}
