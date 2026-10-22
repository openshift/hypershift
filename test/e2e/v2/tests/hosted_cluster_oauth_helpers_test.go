//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDeleteOAuthIdentitySecret(t *testing.T) {
	for _, testCase := range []struct {
		name                                                                                             string
		verified, referenced, readError, deleteError, canceled, clusterMissing, secretMissing, wantError bool
	}{
		{name: "When restoration was not verified, it should retain the owned Secret", wantError: true},
		{name: "When restoration failed and a live reference remains, it should retain the owned Secret", referenced: true, wantError: true},
		{name: "When restoration was verified and the reference detached, it should delete the owned Secret", verified: true},
		{name: "When a reference reappears after restoration, it should retain the owned Secret", verified: true, referenced: true, wantError: true},
		{name: "When reference verification fails, it should retain the owned Secret", verified: true, readError: true, wantError: true},
		{name: "When deletion fails, it should preserve the cleanup error", verified: true, deleteError: true, wantError: true},
		{name: "When cleanup is canceled, it should retain the owned Secret", verified: true, canceled: true, wantError: true},
		{name: "When the cluster was removed, it should delete the detached owned Secret", verified: true, clusterMissing: true},
		{name: "When the Secret was already deleted, it should tolerate NotFound", verified: true, secretMissing: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "cluster"}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: "owned-htpasswd"}}
			if testCase.referenced {
				cluster.Spec.Configuration = &hyperv1.ClusterConfiguration{OAuth: &configv1.OAuthSpec{IdentityProviders: []configv1.IdentityProvider{{IdentityProviderConfig: configv1.IdentityProviderConfig{HTPasswd: &configv1.HTPasswdIdentityProvider{FileData: configv1.SecretNameReference{Name: secret.Name}}}}}}}
			}
			objects := []crclient.Object{}
			if !testCase.clusterMissing {
				objects = append(objects, cluster)
			}
			if !testCase.secretMissing {
				objects = append(objects, secret)
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
					if _, isCluster := object.(*hyperv1.HostedCluster); isCluster && testCase.readError {
						return errors.New("reference verification unavailable")
					}
					return client.Get(ctx, key, object, opts...)
				},
				Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.DeleteOption) error {
					if testCase.deleteError {
						return errors.New("Secret deletion unavailable")
					}
					return client.Delete(ctx, object, opts...)
				},
			}).Build()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			err := deleteOAuthIdentitySecret(ctx, client, crclient.ObjectKeyFromObject(cluster), secret, testCase.verified)
			if (err != nil) != testCase.wantError {
				t.Fatalf("cleanup error=%v", err)
			}
			readErr := client.Get(t.Context(), crclient.ObjectKeyFromObject(secret), &corev1.Secret{})
			wantRetained := testCase.wantError && !testCase.secretMissing
			if wantRetained && readErr != nil || !wantRetained && !apierrors.IsNotFound(readErr) {
				t.Fatalf("unexpected Secret retention: %v", readErr)
			}
		})
	}
}

func TestOauthAccessToken(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		status   int
		location string
		want     string
		wantErr  bool
	}{
		{name: "When authorization redirects with a token, it should return the token", status: http.StatusFound, location: "https://oauth.example.test/#access_token=synthetic-test-token&token_type=Bearer", want: "synthetic-test-token"},
		{name: "When authorization fails, it should reject the response", status: http.StatusUnauthorized, wantErr: true},
		{name: "When redirect is missing, it should return an error", status: http.StatusFound, wantErr: true},
		{name: "When token is missing, it should return an error", status: http.StatusFound, location: "https://oauth.example.test/#error=access_denied", wantErr: true},
		{name: "When fragment is malformed, it should return a redacted error", status: http.StatusFound, location: "https://oauth.example.test/#access_token=synthetic-test-token&bad=%zz", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := &http.Response{StatusCode: testCase.status, Header: http.Header{}}
			if testCase.location != "" {
				response.Header.Set("Location", testCase.location)
			}
			got, err := oauthAccessToken(response)
			if (err != nil) != testCase.wantErr || got != testCase.want {
				t.Fatal("unexpected token parsing result")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-test-token") {
				t.Fatal("token must not be included in errors")
			}
		})
	}
}
