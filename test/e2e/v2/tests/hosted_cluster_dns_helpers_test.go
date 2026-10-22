//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDeleteCustomDNSCertificate(t *testing.T) {
	for _, test := range []struct {
		name                                           string
		verified, absent, denied, nilSecret, wantError bool
	}{
		{name: "When restoration is unverified, it should retain the serving certificate and fail cleanup", wantError: true},
		{name: "When restoration is verified, it should delete the serving certificate", verified: true},
		{name: "When the certificate was already removed, it should accept NotFound", verified: true, absent: true},
		{name: "When deletion is denied, it should report cleanup failure", verified: true, denied: true, wantError: true},
		{name: "When the certificate input is nil, it should fail without deleting anything", verified: true, nilSecret: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "custom-serving-cert", Namespace: "clusters"}}
			builder := fake.NewClientBuilder().WithScheme(hyperapi.Scheme)
			if !test.absent {
				builder = builder.WithObjects(secret.DeepCopy())
			}
			deleteCalled := false
			builder = builder.WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, options ...crclient.DeleteOption) error {
				deleteCalled = true
				if test.denied {
					return errors.New("delete denied")
				}
				return client.Delete(ctx, object, options...)
			}})
			client := builder.Build()
			input := secret
			if test.nilSecret {
				input = nil
			}
			err := deleteCustomDNSCertificate(t.Context(), client, input, test.verified)
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected cleanup result: %v", err)
			}
			if !test.verified || test.nilSecret {
				if deleteCalled {
					t.Fatal("unsafe certificate deletion was attempted")
				}
				if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(secret), &corev1.Secret{}); err != nil {
					t.Fatalf("serving certificate was not retained: %v", err)
				}
			} else if !test.denied {
				if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(secret), &corev1.Secret{}); !apierrors.IsNotFound(err) {
					t.Fatalf("expected certificate deletion: %v", err)
				}
			}
		})
	}
}
