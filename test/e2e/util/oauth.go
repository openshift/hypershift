package util

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	v1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	configmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	userv1 "github.com/openshift/api/user/v1"
	userv1client "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	restclient "k8s.io/client-go/rest"
)

func EnsureOAuthWithIdentityProvider(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureOAuthWithIdentityProvider", func(t *testing.T) {
		g := NewWithT(t)

		// secret containing htpasswd "file": `htpasswd -cbB htpasswd.tmp testuser password`
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "htpasswd",
				Namespace: hostedCluster.Namespace},
			Data: map[string][]byte{
				"htpasswd": []byte("testuser:$2y$05$0Fk2s.0FbLy0FZ82JAqajOV/kbT/wqKX5/QFKgps6J69J2jY6r5ZG"),
			},
		}

		err := client.Create(ctx, &secret)
		g.Expect(err).ToNot(HaveOccurred(), "failed to create htpasswd secret")

		if hostedCluster.Spec.Configuration == nil {
			hostedCluster.Spec.Configuration = &hyperv1.ClusterConfiguration{}
		}
		hostedCluster.Spec.Configuration.OAuth = &v1.OAuthSpec{
			IdentityProviders: []v1.IdentityProvider{
				{
					Name:          "my_htpasswd_provider",
					MappingMethod: v1.MappingMethodClaim,
					IdentityProviderConfig: v1.IdentityProviderConfig{
						Type: v1.IdentityProviderTypeHTPasswd,
						HTPasswd: &v1.HTPasswdIdentityProvider{
							FileData: v1.SecretNameReference{
								Name: secret.Name,
							},
						},
					},
				},
			},
		}

		err = client.Update(ctx, hostedCluster)
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster identity providers")

		guestKubeConfigSecretData, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")
		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).ToNot(HaveOccurred())

		// wait for oauth route to be ready
		oauthRoute := WaitForOAuthRouteReady(t, ctx, client, guestConfig, hostedCluster)

		// wait for oauth token request to succeed
		access_token := WaitForOAuthToken(t, ctx, oauthRoute, guestConfig, "testuser", "password")

		user, err := GetUserForToken(guestConfig, access_token)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(user.Name).To(Equal("testuser"))
	})
}

func WaitForOAuthToken(t *testing.T, ctx context.Context, oauthRoute *routev1.Route, restConfig *restclient.Config, username, password string) string {
	g := NewWithT(t)

	oauthClient := configmanifests.OAuthServerChallengingClient().Name
	tokenReqUrl := fmt.Sprintf("https://%s/oauth/authorize?response_type=token&client_id=%s", oauthRoute.Spec.Host, oauthClient)
	request, err := http.NewRequest(http.MethodGet, tokenReqUrl, nil)
	g.Expect(err).ToNot(HaveOccurred())

	request.Header.Set("Authorization", getBasicHeader(username, password))
	request.Header.Set("X-CSRF-Token", "1")

	transport, err := restclient.TransportFor(restclient.AnonymousClientConfig(restConfig))
	g.Expect(err).ToNot(HaveOccurred(), "error getting transport")

	httpClient := &http.Client{Transport: transport}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// don't resolve redirects and return the response instead
		return http.ErrUseLastResponse
	}

	var access_token string
	err = wait.PollImmediateWithContext(ctx, time.Second, time.Minute, func(ctx context.Context) (done bool, err error) {
		resp, err := httpClient.Do(request)
		if err != nil {
			t.Logf("Waiting for OAuth token request to succeed")
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusFound {
			t.Logf("Waiting for OAuth token request status code %v, got %v", http.StatusFound, resp.StatusCode)
			return false, nil
		}

		// extract access_token from redirect URL
		access_token, err = extractAccessToken(resp)
		if err != nil {
			t.Logf("Failed to extract access token from redirect url")
			return false, nil
		}

		return true, nil
	})
	g.Expect(err).ToNot(HaveOccurred(), "failed to request oauth token")
	t.Logf("OAuth token retrieved successfully for user %s", username)

	return access_token
}

func WaitForOAuthRouteReady(t *testing.T, ctx context.Context, client crclient.Client, restConfig *restclient.Config, hostedCluster *hyperv1.HostedCluster) *routev1.Route {
	g := NewWithT(t)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
	route := hcpmanifests.OauthServerExternalPublicRoute(hcpNamespace)

	err := wait.PollImmediateWithContext(ctx, time.Second, time.Minute, func(ctx context.Context) (done bool, err error) {
		err = client.Get(context.Background(), crclient.ObjectKeyFromObject(route), route)
		if err != nil {
			return false, nil
		}
		return true, nil
	})
	g.Expect(err).ToNot(HaveOccurred(), "failed retrieving oauth route")
	t.Logf("Found OAuth route %s", route.Spec.Host)

	request, err := http.NewRequest(http.MethodHead, fmt.Sprintf("https://%s/healthz", route.Spec.Host), nil)
	g.Expect(err).ToNot(HaveOccurred())

	transport, err := restclient.TransportFor(restclient.AnonymousClientConfig(restConfig))
	g.Expect(err).ToNot(HaveOccurred(), "Error getting transport")

	err = wait.PollImmediateWithContext(ctx, time.Second, time.Minute, func(ctx context.Context) (done bool, err error) {
		resp, err := transport.RoundTrip(request)
		if resp != nil && resp.StatusCode == http.StatusOK {
			return true, nil
		}
		if resp != nil {
			t.Logf("Waiting for OAuth route %s to be ready: %v", route.Spec.Host, resp.Status)
		}
		if err != nil {
			t.Logf("Waiting for OAuth route %s to be ready: %v", route.Spec.Host, err)
		}
		return false, nil
	})
	g.Expect(err).ToNot(HaveOccurred(), "failed waiting for OAuth route %s", route.Spec.Host)
	t.Logf("Observed OAuth route %s to be healthy", route.Spec.Host)

	return route
}

func GetUserForToken(config *restclient.Config, token string) (*userv1.User, error) {
	userConfig := restclient.AnonymousClientConfig(config)
	userConfig.BearerToken = token
	userClient, err := userv1client.NewForConfig(userConfig)
	if err != nil {
		return nil, err
	}

	user, err := userClient.Users().Get(context.Background(), "~", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return user, err
}

func getBasicHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

func extractAccessToken(resp *http.Response) (string, error) {
	location, err := resp.Location()
	if err != nil {
		return "", err
	}

	fragments, err := url.ParseQuery(location.Fragment)
	if err != nil {
		return "", err
	}
	if len(fragments["access_token"]) == 0 {
		return "", fmt.Errorf("access_token not found")
	}

	return fragments["access_token"][0], nil
}
