package certificatesigningcontroller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/controller/factory"
	librarygocrypto "github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/clock"
)

func TestCertificateLoadingController_CurrentCA(t *testing.T) {
	key, crt := certificateAuthorityRaw(t)
	syncCtx := factory.NewSyncContext("whatever", events.NewLoggingEventRecorder("test", clock.RealClock{}))

	controller := CertificateLoadingController{
		caValue:   atomic.Value{},
		loaded:    make(chan interface{}),
		setLoaded: &sync.Once{},
	}

	t.Log("ask for the current CA before we've loaded anything")
	caChan := make(chan *librarygocrypto.CA, 1)
	errChan := make(chan error, 1)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		ca, err := controller.CurrentCA(context.Background())
		caChan <- ca
		errChan <- err
		wg.Done()
	}()

	t.Log("configure the controller not to find the CA (it does not yet exist)")
	controller.getSigningCertKeyPairSecret = func() (*corev1.Secret, error) {
		return nil, apierrors.NewNotFound(corev1.SchemeGroupVersion.WithResource("secrets").GroupResource(), "whatever")
	}

	t.Log("expect that a sync does not error")
	if err := controller.sync(context.Background(), syncCtx); err != nil {
		t.Fatalf("expected no error from sync, got %v", err)
	}

	t.Log("configure the controller to get the CA")
	controller.getSigningCertKeyPairSecret = func() (*corev1.Secret, error) {
		return &corev1.Secret{
			Data: map[string][]byte{
				"tls.crt": crt,
				"tls.key": key,
			},
		}, nil
	}

	t.Log("expect that a sync does not error")
	if err := controller.sync(context.Background(), syncCtx); err != nil {
		t.Fatalf("expected no error from sync, got %v", err)
	}

	t.Log("expect that our CurrentCA() call completed and loaded the correct thing")
	wg.Wait()
	close(caChan)
	close(errChan)
	var errs []error
	for err := range errChan {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		t.Fatalf("expected no error from CurrentCA(), got %v", errs)
	}

	var cas []*librarygocrypto.CA
	for ca := range caChan {
		if ca != nil {
			cas = append(cas, ca)
		}
	}
	if len(cas) > 1 {
		t.Fatalf("got more than one CA: %v", cas)
	}
	rawCert, rawKey, err := cas[0].Config.GetPEMBytes()
	if err != nil {
		t.Fatalf("unexpected error marshalling pem: %v", err)
	}
	if diff := cmp.Diff(rawCert, crt); diff != "" {
		t.Fatalf("got incorrect cert: %v", diff)
	}
	if diff := cmp.Diff(rawKey, key); diff != "" {
		t.Fatalf("got incorrect key: %v", diff)
	}

	t.Log("expect that subsequent calls to CurrentCA() return quickly and load the correct thing")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	ca, err := controller.CurrentCA(ctx)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	rawCert, rawKey, err = ca.Config.GetPEMBytes()
	if err != nil {
		t.Fatalf("unexpected error marshalling pem: %v", err)
	}
	if diff := cmp.Diff(rawCert, crt); diff != "" {
		t.Fatalf("got incorrect cert: %v", diff)
	}
	if diff := cmp.Diff(rawKey, key); diff != "" {
		t.Fatalf("got incorrect key: %v", diff)
	}
}
