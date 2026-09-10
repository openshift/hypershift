//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	configmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/netutil"

	v1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"
	userv1 "github.com/openshift/api/user/v1"
	userv1client "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	restclient "k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateOAuthWithIdentityProviderViaLoadBalancer validates kubeadmin and
// htpasswd authentication through the OAuth LoadBalancer endpoint.
func ValidateOAuthWithIdentityProviderViaLoadBalancer(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, restConfig *restclient.Config) error {
	oauthHost, err := waitForOAuthLoadBalancerReady(ctx, client, restConfig, hostedCluster)
	if err != nil {
		return err
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	kubeadminPasswordSecret := configmanifests.KubeadminPasswordSecret(hcpNamespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
		return fmt.Errorf("failed to get kubeadmin password secret: %w", err)
	}
	password := string(kubeadminPasswordSecret.Data["password"])
	accessToken, err := waitForOAuthTokenByHost(ctx, oauthHost, restConfig, "kubeadmin", password)
	if err != nil {
		return err
	}
	user, err := getUserForToken(ctx, restConfig, accessToken)
	if err != nil {
		return fmt.Errorf("failed to get user for kubeadmin token: %w", err)
	}
	if user.Name != "kube:admin" {
		return fmt.Errorf("kubeadmin token authenticated as %q, want %q", user.Name, "kube:admin")
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "htpasswd",
			Namespace: hostedCluster.Namespace,
		},
		Data: map[string][]byte{
			"htpasswd": []byte("testuser:$2y$05$0Fk2s.0FbLy0FZ82JAqajOV/kbT/wqKX5/QFKgps6J69J2jY6r5ZG"),
		},
	}
	if err := client.Create(ctx, &secret); err != nil {
		return fmt.Errorf("failed to create htpasswd secret: %w", err)
	}

	if err := UpdateObject(ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
		if obj.Spec.Configuration == nil {
			obj.Spec.Configuration = &hyperv1.ClusterConfiguration{}
		}
		obj.Spec.Configuration.OAuth = &v1.OAuthSpec{
			IdentityProviders: []v1.IdentityProvider{
				{
					Name:          "my_htpasswd_provider",
					MappingMethod: v1.MappingMethodClaim,
					IdentityProviderConfig: v1.IdentityProviderConfig{
						Type: v1.IdentityProviderTypeHTPasswd,
						HTPasswd: &v1.HTPasswdIdentityProvider{
							FileData: v1.SecretNameReference{Name: secret.Name},
						},
					},
				},
			},
		}
	}); err != nil {
		return fmt.Errorf("failed to update HostedCluster identity providers: %w", err)
	}

	if err := waitForOAuthConfig(ctx, client, hostedCluster); err != nil {
		return err
	}
	accessToken, err = waitForOAuthTokenByHost(ctx, oauthHost, restConfig, "testuser", "password")
	if err != nil {
		return err
	}
	user, err = getUserForToken(ctx, restConfig, accessToken)
	if err != nil {
		return fmt.Errorf("failed to get user for testuser token: %w", err)
	}
	if user.Name != "testuser" {
		return fmt.Errorf("testuser token authenticated as %q, want %q", user.Name, "testuser")
	}

	return validateClusterPostIDP(ctx, client, hostedCluster)
}

func waitForOAuthLoadBalancerReady(ctx context.Context, client crclient.Client, restConfig *restclient.Config, hostedCluster *hyperv1.HostedCluster) (string, error) {
	oauthStrategy := netutil.ServicePublishingStrategyByTypeByHC(hostedCluster, hyperv1.OAuthServer)
	if oauthStrategy == nil {
		return "", fmt.Errorf("OAuth service publishing strategy not found in HostedCluster spec")
	}
	if oauthStrategy.LoadBalancer == nil {
		return "", fmt.Errorf("OAuth LoadBalancer strategy not found")
	}
	oauthHost := oauthStrategy.LoadBalancer.Hostname
	if oauthHost == "" {
		return "", fmt.Errorf("OAuth LoadBalancer hostname is empty")
	}
	GinkgoWriter.Printf("OAuth hostname from HostedCluster spec: %s\n", oauthHost)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	svc := hcpmanifests.OauthServerService(hcpNamespace)
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(svc), svc); err != nil {
			return false, nil //nolint:nilerr // retry until the OAuth service is available
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			GinkgoWriter.Printf("Waiting for oauth-openshift Service type to be LoadBalancer, got %s\n", svc.Spec.Type)
			return false, nil
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			GinkgoWriter.Println("Waiting for oauth-openshift LoadBalancer to get an external endpoint")
			return false, nil
		}
		ingress := svc.Status.LoadBalancer.Ingress[0]
		if ingress.IP == "" && ingress.Hostname == "" {
			return false, nil
		}
		GinkgoWriter.Printf("OAuth LoadBalancer has external endpoint: %s%s\n", ingress.IP, ingress.Hostname)
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed waiting for oauth-openshift LoadBalancer endpoint: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodHead, fmt.Sprintf("https://%s/healthz", oauthHost), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create OAuth LoadBalancer health request: %w", err)
	}
	transport, err := restclient.TransportFor(restclient.AnonymousClientConfig(restConfig))
	if err != nil {
		return "", fmt.Errorf("error getting OAuth LoadBalancer transport: %w", err)
	}
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		resp, err := transport.RoundTrip(request.Clone(ctx))
		if resp != nil {
			defer resp.Body.Close()
		}
		if resp != nil && resp.StatusCode == http.StatusOK {
			return true, nil
		}
		if resp != nil {
			GinkgoWriter.Printf("Waiting for OAuth LoadBalancer %s to be ready: %v\n", oauthHost, resp.Status)
		}
		if err != nil {
			GinkgoWriter.Printf("Waiting for OAuth LoadBalancer %s to be ready: %v\n", oauthHost, err)
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed waiting for OAuth LoadBalancer %s to be healthy: %w", oauthHost, err)
	}
	GinkgoWriter.Printf("Observed OAuth LoadBalancer %s to be healthy\n", oauthHost)
	return oauthHost, nil
}

func waitForOAuthTokenByHost(ctx context.Context, oauthHost string, restConfig *restclient.Config, username, password string) (string, error) {
	oauthClient := configmanifests.OAuthServerChallengingClient().Name
	tokenRequestURL := fmt.Sprintf("https://%s/oauth/authorize?response_type=token&client_id=%s", oauthHost, oauthClient)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenRequestURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	request.Header.Set("X-CSRF-Token", "1")
	transport, err := restclient.TransportFor(restclient.AnonymousClientConfig(restConfig))
	if err != nil {
		return "", fmt.Errorf("error getting OAuth token transport: %w", err)
	}
	httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	var accessToken string
	err = wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		resp, err := httpClient.Do(request.Clone(ctx))
		if err != nil {
			GinkgoWriter.Printf("Waiting for OAuth token request to succeed for user %s\n", username)
			return false, nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			GinkgoWriter.Printf("Waiting for OAuth token request status code %v, got %v\n", http.StatusFound, resp.StatusCode)
			return false, nil
		}
		accessToken, err = extractAccessToken(resp)
		if err != nil {
			GinkgoWriter.Printf("Failed to extract access token from redirect URL: %v\n", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to request OAuth token for user %s: %w", username, err)
	}
	GinkgoWriter.Printf("OAuth token retrieved successfully for user %s\n", username)
	return accessToken, nil
}

func getUserForToken(ctx context.Context, config *restclient.Config, token string) (*userv1.User, error) {
	userConfig := restclient.AnonymousClientConfig(config)
	userConfig.BearerToken = token
	userClient, err := userv1client.NewForConfig(userConfig)
	if err != nil {
		return nil, err
	}
	return userClient.Users().Get(ctx, "~", metav1.GetOptions{})
}

func waitForOAuthConfig(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) error {
	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	oauthConfigCM := hcpmanifests.OAuthServerConfig(hcpNamespace)
	err := wait.PollUntilContextTimeout(ctx, time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(oauthConfigCM), oauthConfigCM); err != nil {
			return false, nil //nolint:nilerr // retry until the OAuth config exists
		}
		data, ok := oauthConfigCM.Data["config.yaml"]
		if !ok || data == "" {
			return false, nil
		}
		oauthConfig := &osinv1.OsinServerConfig{}
		if _, _, err := api.YamlSerializer.Decode([]byte(data), nil, oauthConfig); err != nil {
			return false, nil //nolint:nilerr // retry until the OAuth config is valid
		}
		return len(oauthConfig.OAuthConfig.IdentityProviders) > 0, nil
	})
	if err != nil {
		return fmt.Errorf("failed validating OAuth config: %w", err)
	}
	return nil
}

func validateClusterPostIDP(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) error {
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
		return err
	}
	if hostedCluster.Status.KubeadminPassword != nil {
		return fmt.Errorf("HostedCluster status kubeadmin password should be nil after adding an identity provider")
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	kubeadminPasswordSecret := configmanifests.KubeadminPasswordSecret(hcpNamespace)
	err := client.Get(ctx, crclient.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret)
	if !apierrors.IsNotFound(err) {
		if err == nil {
			return fmt.Errorf("kubeadmin password secret should be deleted after adding an identity provider")
		}
		return fmt.Errorf("expected kubeadmin password secret to be deleted: %w", err)
	}

	oauthDeployment := configmanifests.OAuthDeployment(hcpNamespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(oauthDeployment), oauthDeployment); err != nil {
		return err
	}
	if _, ok := oauthDeployment.Spec.Template.ObjectMeta.Annotations[oauth.KubeadminSecretHashAnnotation]; ok {
		return fmt.Errorf("OAuth deployment still has kubeadmin password hash annotation")
	}
	return nil
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
