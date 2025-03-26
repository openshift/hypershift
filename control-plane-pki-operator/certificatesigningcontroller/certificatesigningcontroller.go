package certificatesigningcontroller

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	"github.com/openshift/library-go/pkg/controller/factory"
	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	certificatesv1applyconfigurations "k8s.io/client-go/applyconfigurations/certificates/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/certificate/csr"
	"k8s.io/klog/v2"
)

type CertificateSigningController struct {
	kubeClient kubernetes.Interface

	fieldManager              string
	signerName                string
	validator                 certificates.ValidatorFunc
	getCSR                    func(name string) (*certificatesv1.CertificateSigningRequest, error)
	getCurrentCABundleContent func(context.Context) (*librarygocrypto.CA, error)
	certTTL                   time.Duration
}

func NewCertificateSigningController(
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane,
	signer certificates.SignerClass,
	getCurrentCABundleContent func(context.Context) (*librarygocrypto.CA, error),
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
	certTTL time.Duration,
) factory.Controller {
	c := &CertificateSigningController{
		fieldManager: string(signer) + "-certificate-signing-controller",
		kubeClient:   kubeClient,
		signerName:   certificates.SignerNameForHCP(hostedControlPlane, signer),
		validator:    certificates.Validator(hostedControlPlane, signer),
		getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
			return kubeInformersForNamespaces.InformersFor(corev1.NamespaceAll).Certificates().V1().CertificateSigningRequests().Lister().Get(name)
		},
		getCurrentCABundleContent: getCurrentCABundleContent,
		certTTL:                   certTTL,
	}

	csrInformer := kubeInformersForNamespaces.InformersFor(corev1.NamespaceAll).Certificates().V1().CertificateSigningRequests().Informer()

	return factory.New().
		WithInformersQueueKeysFunc(enqueueCertificateSigningRequest, csrInformer).
		WithSync(c.syncCertificateSigningRequest).
		ResyncEvery(time.Minute).
		ToController("CertificateSigningController", eventRecorder.WithComponentSuffix(c.fieldManager))
}

func enqueueCertificateSigningRequest(obj runtime.Object) []string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func (c *CertificateSigningController) syncCertificateSigningRequest(ctx context.Context, syncContext factory.SyncContext) error {
	_, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

	cfg, requeue, validationErr, err := c.processCertificateSigningRequest(ctx, name, nil)
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}
	if cfg != nil {
		if validationErr != nil {
			syncContext.Recorder().Eventf("CertificateSigningRequestInvalid", "%q is invalid: %s", name, validationErr.Error())
		} else {
			syncContext.Recorder().Eventf("CertificateSigningRequestValid", "%q is valid", name)
		}
		_, err := c.kubeClient.CertificatesV1().CertificateSigningRequests().ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: c.fieldManager})
		if err != nil && validationErr == nil {
			syncContext.Recorder().Eventf("CertificateSigningRequestFulfilled", "%q in %q is fulfilled", name)
		}
		return err
	}

	return nil
}

const backdate = 5 * time.Minute

func (c *CertificateSigningController) processCertificateSigningRequest(ctx context.Context, name string, now func() time.Time) (*certificatesv1applyconfigurations.CertificateSigningRequestApplyConfiguration, bool, error, error) {
	csr, err := c.getCSR(name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil, nil // nothing to be done, CSR is gone
	}
	if err != nil {
		return nil, false, nil, err
	}

	// Ignore the CSR in the following conditions:
	if !certificates.IsCertificateRequestApproved(csr) || // it's not yet approved
		certificates.HasTrueCondition(csr, certificatesv1.CertificateFailed) || // it's already failed
		csr.Spec.SignerName != c.signerName || // it doesn't match our signer
		csr.Status.Certificate != nil { // it's already signed
		return nil, false, nil, nil
	}

	x509cr, err := certificates.ParseCSR(csr.Spec.Request)
	if err != nil {
		return nil, false, nil, fmt.Errorf("unable to parse csr %q: %v", csr.Name, err)
	}
	if validationErr := c.validator(csr, x509cr); validationErr != nil {
		cfg := certificatesv1applyconfigurations.CertificateSigningRequest(name)
		cfg.Status = certificatesv1applyconfigurations.CertificateSigningRequestStatus().WithConditions(
			certificatesv1applyconfigurations.CertificateSigningRequestCondition().
				WithType(certificatesv1.CertificateFailed).
				WithStatus(corev1.ConditionTrue).
				WithReason("SignerValidationFailure").
				WithMessage(validationErr.Error()).
				WithLastUpdateTime(metav1.Now()),
		)
		return cfg, false, validationErr, nil
	}

	ca, err := c.getCurrentCABundleContent(ctx)
	if err != nil {
		return nil, false, nil, err
	}

	raw, err := sign(ca, x509cr, csr.Spec.Usages, c.certTTL, csr.Spec.ExpirationSeconds, now)
	if err != nil {
		return nil, false, nil, err
	}

	cfg := certificatesv1applyconfigurations.CertificateSigningRequest(name)
	cfg.Status = certificatesv1applyconfigurations.CertificateSigningRequestStatus().WithCertificate(raw...)
	return cfg, false, nil, nil
}

func sign(ca *librarygocrypto.CA, x509cr *x509.CertificateRequest, usages []certificatesv1.KeyUsage, certTTL time.Duration, expirationSeconds *int32, now func() time.Time) ([]byte, error) {
	if err := x509cr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("unable to verify certificate request signature: %v", err)
	}

	notBefore, notAfter, err := boundaries(
		now,
		duration(certTTL, expirationSeconds),
		backdate,     // this must always be less than the minimum TTL requested by a user (see sanity check requestedDuration below)
		100*backdate, // roughly 8 hours
		ca.Config.Certs[0].NotAfter,
	)
	if err != nil {
		return nil, err
	}

	x509usages, extUsages, err := certificates.KeyUsagesFromStrings(usages)
	if err != nil {
		return nil, err
	}

	cert, err := ca.SignCertificate(&x509.Certificate{
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		Subject:               x509cr.Subject,
		DNSNames:              x509cr.DNSNames,
		IPAddresses:           x509cr.IPAddresses,
		EmailAddresses:        x509cr.EmailAddresses,
		URIs:                  x509cr.URIs,
		PublicKeyAlgorithm:    x509cr.PublicKeyAlgorithm,
		PublicKey:             x509cr.PublicKey,
		KeyUsage:              x509usages,
		ExtKeyUsage:           extUsages,
		BasicConstraintsValid: true,
		IsCA:                  false,
	}, x509cr.PublicKey)
	if err != nil {
		return nil, err
	}

	return librarygocrypto.EncodeCertificates(cert)
}

func duration(certTTL time.Duration, expirationSeconds *int32) time.Duration {
	if expirationSeconds == nil {
		return certTTL
	}

	// honor requested duration is if it is less than the default TTL
	// use 10 min (2x hard coded backdate above) as a sanity check lower bound
	const min = 2 * backdate
	switch requestedDuration := csr.ExpirationSecondsToDuration(*expirationSeconds); {
	case requestedDuration > certTTL:
		return certTTL

	case requestedDuration < min:
		return min

	default:
		return requestedDuration
	}
}

// boundaries computes NotBefore and NotAfter:
//
//	All certificates set NotBefore = Now() - Backdate.
//	Long-lived certificates set NotAfter = Now() + TTL - Backdate.
//	Short-lived certificates set NotAfter = Now() + TTL.
//	All certificates truncate NotAfter to the expiration date of the signer.
func boundaries(now func() time.Time, ttl, backdate, horizon time.Duration, signerNotAfter time.Time) (time.Time, time.Time, error) {
	if now == nil {
		now = time.Now
	}

	instant := now()

	var notBefore, notAfter time.Time
	if ttl < horizon {
		// do not backdate the end time if we consider this to be a short-lived certificate
		notAfter = instant.Add(ttl)
	} else {
		notAfter = instant.Add(ttl - backdate)
	}

	if !notAfter.Before(signerNotAfter) {
		notAfter = signerNotAfter
	}

	if !notBefore.Before(signerNotAfter) {
		return notBefore, notAfter, fmt.Errorf("the signer has expired: NotAfter=%v", signerNotAfter)
	}

	if !instant.Before(signerNotAfter) {
		return notBefore, notAfter, fmt.Errorf("refusing to sign a certificate that expired in the past: NotAfter=%v", signerNotAfter)
	}

	return notBefore, notAfter, nil
}
