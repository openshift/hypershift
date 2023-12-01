package certificatesigningcontroller

import (
	"bytes"
	"context"
	"crypto"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/hypershift/control-plane-pki-operator/certificates/authority"
)

type CertificateLoadingController struct {
	caValue   atomic.Value
	loaded    chan interface{}
	setLoaded *sync.Once

	getSigningCertKeyPairSecret func() (*corev1.Secret, error)
}

func NewCertificateLoadingController(
	rotatedSigningCASecretNamespace, rotatedSigningCASecretName string,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
) (func(ctx context.Context) (*authority.CertificateAuthority, error), factory.Controller) {
	c := &CertificateLoadingController{
		loaded:    make(chan interface{}),
		setLoaded: &sync.Once{},
		getSigningCertKeyPairSecret: func() (*corev1.Secret, error) {
			return kubeInformersForNamespaces.InformersFor(rotatedSigningCASecretNamespace).Core().V1().Secrets().Lister().Secrets(rotatedSigningCASecretNamespace).Get(rotatedSigningCASecretName)
		},
	}

	return c.CurrentCA, factory.New().
		WithInformers(kubeInformersForNamespaces.InformersFor(rotatedSigningCASecretNamespace).Core().V1().Secrets().Informer()).
		WithSync(c.sync).
		ResyncEvery(time.Minute).
		ToController("CertificateLoadingController", eventRecorder.WithComponentSuffix("certificate-loading-controller"))
}

func (c *CertificateLoadingController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	signingCertKeyPairSecret, err := c.getSigningCertKeyPairSecret()
	if apierrors.IsNotFound(err) {
		return nil // we need to wait for the secret to exist
	}
	if err != nil {
		return err
	}
	if updated, err := c.SetCA(signingCertKeyPairSecret.Data["tls.crt"], signingCertKeyPairSecret.Data["tls.key"]); err != nil {
		syncContext.Recorder().Warningf("CertificateLoadingFailed", "failed to load certificate: %v", err)
		return nil // retrying this won't help
	} else if updated {
		syncContext.Recorder().Event("CertificateLoadingSucceeded", "loaded certificate")
	}

	return nil
}

// The following code comes from core Kube, which can't be imported, unfortunately. These methods are copied from:
// https://github.com/kubernetes/kubernetes/blob/ec5096fa869b801d6eb1bf019819287ca61edc4d/pkg/controller/certificates/signer/ca_provider.go#L17-L99

// SetCA unconditionally stores the current cert/key content
func (c *CertificateLoadingController) SetCA(certPEM, keyPEM []byte) (bool, error) {
	currCA, ok := c.caValue.Load().(*authority.CertificateAuthority)
	if ok && bytes.Equal(currCA.RawCert, certPEM) && bytes.Equal(currCA.RawKey, keyPEM) {
		return false, nil
	}

	certs, err := cert.ParseCertsPEM(certPEM)
	if err != nil {
		return false, fmt.Errorf("error reading CA cert: %w", err)
	}
	if len(certs) != 1 {
		return false, fmt.Errorf("error reading CA cert: expected 1 certificate, found %d", len(certs))
	}

	key, err := keyutil.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return false, fmt.Errorf("error reading CA key: %w", err)
	}
	priv, ok := key.(crypto.Signer)
	if !ok {
		return false, fmt.Errorf("error reading CA key: key did not implement crypto.Signer")
	}

	ca := &authority.CertificateAuthority{
		RawCert: certPEM,
		RawKey:  keyPEM,

		Certificate: certs[0],
		PrivateKey:  priv,
	}
	c.caValue.Store(ca)
	c.setLoaded.Do(func() {
		close(c.loaded)
	})

	return true, nil
}

// CurrentCA provides the current value of the CA. This is a blocking call as the value being loaded may
// not exist at the time it's being requested.
// It always checks for a stale value.  This is cheap because it's all an in memory cache of small slices.
func (c *CertificateLoadingController) CurrentCA(ctx context.Context) (*authority.CertificateAuthority, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to wait for current CA: %w", ctx.Err())
	case <-c.loaded:
		break
	}
	signingCertKeyPairSecret, err := c.getSigningCertKeyPairSecret()
	if err != nil {
		return nil, err
	}
	certPEM, keyPEM := signingCertKeyPairSecret.Data["tls.crt"], signingCertKeyPairSecret.Data["tls.key"]
	currCA := c.caValue.Load().(*authority.CertificateAuthority)
	if bytes.Equal(currCA.RawCert, certPEM) && bytes.Equal(currCA.RawKey, keyPEM) {
		return currCA, nil
	}

	// the bytes weren't equal, so we have to set and then load
	if _, err := c.SetCA(certPEM, keyPEM); err != nil {
		return currCA, err
	}
	return c.caValue.Load().(*authority.CertificateAuthority), nil
}
