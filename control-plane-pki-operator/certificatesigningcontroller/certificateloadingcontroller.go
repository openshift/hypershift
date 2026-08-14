package certificatesigningcontroller

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
) (func(ctx context.Context) (*librarygocrypto.CA, error), factory.Controller) {
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

// SetCA unconditionally stores the current cert/key content
func (c *CertificateLoadingController) SetCA(certPEM, keyPEM []byte) (bool, error) {
	ca, err := librarygocrypto.GetCAFromBytes(certPEM, keyPEM)
	if err != nil {
		return false, fmt.Errorf("error parsing CA cert and key: %w", err)
	}
	c.caValue.Store(ca)
	c.setLoaded.Do(func() {
		close(c.loaded)
	})

	return true, nil
}

// CurrentCA provides the current value of the CA. This is a blocking call as the value being loaded may
// not exist at the time it's being requested.
func (c *CertificateLoadingController) CurrentCA(ctx context.Context) (*librarygocrypto.CA, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("failed to wait for current CA: %w", ctx.Err())
	case <-c.loaded:
		break
	}
	return c.caValue.Load().(*librarygocrypto.CA), nil
}
