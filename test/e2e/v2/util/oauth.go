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
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	hcpmanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	configmanifests "github.com/openshift/hypershift/pkg/manifests/hcco"
	pkgoauth "github.com/openshift/hypershift/pkg/oauth"
	"github.com/openshift/hypershift/support/api"
	supportforwarder "github.com/openshift/hypershift/support/forwarder"
	"github.com/openshift/hypershift/support/netutil"

	v1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"
	userv1 "github.com/openshift/api/user/v1"
	userv1client "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

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
	if _, ok := oauthDeployment.Spec.Template.ObjectMeta.Annotations[pkgoauth.KubeadminSecretHashAnnotation]; ok {
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

// guestRestConfig polls until the HostedCluster publishes its kubeconfig and returns
// a REST config pointing at the guest KAS. Client-side throttling is disabled.
func guestRestConfig(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) (*restclient.Config, error) {
	var kubeConfigData []byte
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		hc := &hyperv1.HostedCluster{}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc); err != nil {
			return false, nil //nolint:nilerr
		}
		if hc.Status.KubeConfig == nil || hc.Status.KubeConfig.Name == "" {
			return false, nil
		}
		secret := &corev1.Secret{}
		secretKey := crclient.ObjectKey{Namespace: hostedCluster.Namespace, Name: hc.Status.KubeConfig.Name}
		if err := client.Get(ctx, secretKey, secret); err != nil {
			return false, nil //nolint:nilerr
		}
		data, ok := secret.Data["kubeconfig"]
		if !ok || len(data) == 0 {
			return false, nil
		}
		kubeConfigData = data
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed waiting for kubeconfig to be published for HostedCluster %s/%s: %w", hostedCluster.Namespace, hostedCluster.Name, err)
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse guest kubeconfig: %w", err)
	}
	cfg.QPS = -1
	cfg.Burst = -1
	return cfg, nil
}

// waitForRunningPod polls until a running, non-terminating pod matching labels exists
// in namespace and returns it. The componentName is used only in log messages.
func waitForRunningPod(ctx context.Context, client crclient.Client, namespace string, labels crclient.MatchingLabels, componentName string) (*corev1.Pod, error) {
	var found *corev1.Pod
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		podList := &corev1.PodList{}
		if err := client.List(ctx, podList, crclient.InNamespace(namespace), labels); err != nil {
			GinkgoWriter.Printf("Waiting for %s pod: %v\n", componentName, err)
			return false, nil
		}
		for i := range podList.Items {
			if podList.Items[i].Status.Phase == corev1.PodRunning && podList.Items[i].DeletionTimestamp == nil {
				found = &podList.Items[i]
				return true, nil
			}
		}
		GinkgoWriter.Printf("Waiting for a running %s pod in namespace %s\n", componentName, namespace)
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("no running %s pod found in namespace %s: %w", componentName, namespace, err)
	}
	GinkgoWriter.Printf("Found running %s pod %s/%s\n", componentName, found.Namespace, found.Name)
	return found, nil
}

// waitForOAuthTokenWithTransport performs the OAuth token request flow against oauthHost
// using an explicit transport. Unlike waitForOAuthTokenByHost, it does not derive a
// transport from a REST config, making it suitable for port-forward-based OAuth access.
func waitForOAuthTokenWithTransport(ctx context.Context, oauthHost string, transport http.RoundTripper, username, password string) (string, error) {
	oauthClientName := configmanifests.OAuthServerChallengingClient().Name
	tokenRequestURL := fmt.Sprintf("https://%s/oauth/authorize?response_type=token&client_id=%s", oauthHost, oauthClientName)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenRequestURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
	request.Header.Set("X-CSRF-Token", "1")
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

// WaitForOAuthLoadBalancerEndpoint waits for the oauth-openshift Service to become a
// LoadBalancer with an allocated endpoint and returns the OAuth hostname from the
// HostedCluster's service publishing strategy. Unlike waitForOAuthLoadBalancerReady,
// it does not perform a /healthz check, making it suitable for private topology clusters
// where the endpoint is not directly reachable from the test runner.
func WaitForOAuthLoadBalancerEndpoint(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) (string, error) {
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
			return false, nil //nolint:nilerr // retry until service exists
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			GinkgoWriter.Printf("Waiting for oauth-openshift Service type to be LoadBalancer, got %s\n", svc.Spec.Type)
			return false, nil
		}
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			GinkgoWriter.Println("Waiting for oauth-openshift LoadBalancer to get an endpoint")
			return false, nil
		}
		ingress := svc.Status.LoadBalancer.Ingress[0]
		if ingress.IP == "" && ingress.Hostname == "" {
			return false, nil
		}
		GinkgoWriter.Printf("OAuth LoadBalancer endpoint ready (IP=%s, Hostname=%s)\n", ingress.IP, ingress.Hostname)
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed waiting for oauth-openshift LoadBalancer endpoint: %w", err)
	}
	return oauthHost, nil
}

// SetupOAuthPortForwardTransport creates a SPDY port-forward tunnel to the
// oauth-openshift pod and returns an http.RoundTripper that routes requests through
// the tunnel. TLS verification uses the hosted cluster CA with ServerName set to
// oauthHost so the certificate SAN check passes over the localhost tunnel address.
// After establishing the tunnel, it polls /healthz to confirm the OAuth server is ready.
// The port-forward is torn down via DeferCleanup.
func SetupOAuthPortForwardTransport(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, oauthHost string) (http.RoundTripper, error) {
	mgmtConfig, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get management cluster REST config: %w", err)
	}
	kubeClient, err := kubernetes.NewForConfig(mgmtConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	oauthPod, err := waitForRunningPod(ctx, mgmtClient, hcpNamespace,
		crclient.MatchingLabels{hyperv1.ControlPlaneComponentLabel: "oauth-openshift"}, "oauth-openshift")
	if err != nil {
		return nil, err
	}

	stopCh := make(chan struct{})
	pf := &supportforwarder.PortForwarder{
		Namespace: oauthPod.Namespace,
		PodName:   oauthPod.Name,
		Client:    kubeClient,
		Config:    mgmtConfig,
		Out:       io.Discard,
		ErrOut:    io.Discard,
	}
	if err := pf.ForwardPorts([]string{"0:6443"}, stopCh); err != nil {
		close(stopCh)
		return nil, fmt.Errorf("failed to start port-forward to oauth-openshift pod %s/%s: %w", oauthPod.Namespace, oauthPod.Name, err)
	}
	DeferCleanup(func() { close(stopCh) })

	ports, err := pf.GetPorts()
	if err != nil || len(ports) == 0 {
		return nil, fmt.Errorf("failed to get forwarded ports for oauth-openshift: %w", err)
	}
	localPort := ports[0].Local
	GinkgoWriter.Printf("oauth-openshift port-forward established: localhost:%d -> %s:6443\n", localPort, oauthPod.Name)

	guestCfg, err := guestRestConfig(ctx, mgmtClient, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get guest REST config: %w", err)
	}
	tlsConfig, err := restclient.TLSConfigFor(restclient.AnonymousClientConfig(guestCfg))
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS config from guest config: %w", err)
	}
	tlsConfig.ServerName = oauthHost

	localAddr := fmt.Sprintf("localhost:%d", localPort)
	oauthTransport := &http.Transport{
		TLSClientConfig: tlsConfig,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", localAddr)
		},
	}

	healthRequest, err := http.NewRequestWithContext(ctx, http.MethodHead, fmt.Sprintf("https://%s/healthz", oauthHost), nil)
	if err != nil {
		return nil, err
	}
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		resp, err := oauthTransport.RoundTrip(healthRequest.Clone(ctx))
		if resp != nil {
			defer resp.Body.Close()
		}
		if resp != nil && resp.StatusCode == http.StatusOK {
			return true, nil
		}
		if resp != nil {
			GinkgoWriter.Printf("Waiting for OAuth server healthcheck via port-forward: %v\n", resp.Status)
		}
		if err != nil {
			GinkgoWriter.Printf("Waiting for OAuth server healthcheck via port-forward: %v\n", err)
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed OAuth server healthcheck via port-forward to %s: %w", oauthHost, err)
	}
	GinkgoWriter.Printf("OAuth server healthy via port-forward at localhost:%d\n", localPort)
	return oauthTransport, nil
}

// SetupGuestKASPortForwardConfig creates a SPDY port-forward tunnel to the
// kube-apiserver pod and returns a *restclient.Config that routes hosted cluster API
// calls through the tunnel. TLS verification uses the hosted cluster CA with ServerName
// set to the original KAS hostname so the certificate SAN check passes over the localhost
// tunnel address. The port-forward is torn down via DeferCleanup.
func SetupGuestKASPortForwardConfig(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) (*restclient.Config, error) {
	mgmtConfig, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get management cluster REST config: %w", err)
	}
	kubeClient, err := kubernetes.NewForConfig(mgmtConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	kasPod, err := waitForRunningPod(ctx, mgmtClient, hcpNamespace,
		crclient.MatchingLabels{"app": "kube-apiserver", hyperv1.ControlPlaneComponentLabel: "kube-apiserver"}, "kube-apiserver")
	if err != nil {
		return nil, err
	}

	kasPort := netutil.KASPodPortFromHostedCluster(hostedCluster)
	stopCh := make(chan struct{})
	pf := &supportforwarder.PortForwarder{
		Namespace: kasPod.Namespace,
		PodName:   kasPod.Name,
		Client:    kubeClient,
		Config:    mgmtConfig,
		Out:       io.Discard,
		ErrOut:    io.Discard,
	}
	if err := pf.ForwardPorts([]string{fmt.Sprintf("0:%d", kasPort)}, stopCh); err != nil {
		close(stopCh)
		return nil, fmt.Errorf("failed to start port-forward to kube-apiserver pod %s/%s: %w", kasPod.Namespace, kasPod.Name, err)
	}
	DeferCleanup(func() { close(stopCh) })

	ports, err := pf.GetPorts()
	if err != nil || len(ports) == 0 {
		return nil, fmt.Errorf("failed to get forwarded ports for kube-apiserver: %w", err)
	}
	localPort := ports[0].Local
	GinkgoWriter.Printf("kube-apiserver port-forward established: localhost:%d -> %s:%d\n", localPort, kasPod.Name, kasPort)

	guestCfg, err := guestRestConfig(ctx, mgmtClient, hostedCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to get guest REST config: %w", err)
	}

	originalHost := guestCfg.Host
	u, err := url.Parse(originalHost)
	if err != nil {
		return nil, fmt.Errorf("failed to parse guest config host URL: %w", err)
	}
	serverName := u.Hostname()

	guestCfg.Host = fmt.Sprintf("https://localhost:%d", localPort)
	guestCfg.TLSClientConfig.ServerName = serverName

	kasClient, err := kubernetes.NewForConfig(guestCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for KAS port-forward connectivity check: %w", err)
	}
	err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		if _, err := kasClient.Discovery().ServerVersion(); err != nil {
			GinkgoWriter.Printf("Waiting for guest KAS connectivity via port-forward: %v\n", err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed guest KAS connectivity check via port-forward: %w", err)
	}
	GinkgoWriter.Printf("Guest KAS reachable via port-forward at localhost:%d (ServerName=%s)\n", localPort, serverName)
	return guestCfg, nil
}

// ValidateOAuthIdentityProviderFlow validates the full OAuth identity provider flow
// against oauthHost using port-forward transports: kubeadmin login, htpasswd IDP setup,
// testuser login, and kubeadmin secret removal. The transport is used for OAuth token
// requests; kasConfig routes guest API calls through a port-forward tunnel to the KAS.
// If transportFactory is non-nil, a fresh transport is established after the OAuth server
// restarts due to IDP configuration changes. This function mutates cluster state and
// should only be called from lifecycle tests.
func ValidateOAuthIdentityProviderFlow(
	ctx context.Context,
	client crclient.Client,
	hostedCluster *hyperv1.HostedCluster,
	oauthHost string,
	transport http.RoundTripper,
	kasConfig *restclient.Config,
	transportFactory func(context.Context) (http.RoundTripper, error),
) error {
	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	kubeadminPasswordSecret := configmanifests.KubeadminPasswordSecret(hcpNamespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(kubeadminPasswordSecret), kubeadminPasswordSecret); err != nil {
		return fmt.Errorf("failed to get kubeadmin password secret: %w", err)
	}
	password := string(kubeadminPasswordSecret.Data["password"])

	accessToken, err := waitForOAuthTokenWithTransport(ctx, oauthHost, transport, "kubeadmin", password)
	if err != nil {
		return err
	}
	user, err := getUserForToken(ctx, kasConfig, accessToken)
	if err != nil {
		return fmt.Errorf("failed to get user for kubeadmin token: %w", err)
	}
	if user.Name != "kube:admin" {
		return fmt.Errorf("kubeadmin token authenticated as %q, want %q", user.Name, "kube:admin")
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("htpasswd-%s", hostedCluster.Name),
			Namespace: hostedCluster.Namespace,
		},
		Data: map[string][]byte{
			"htpasswd": []byte("testuser:$2y$05$0Fk2s.0FbLy0FZ82JAqajOV/kbT/wqKX5/QFKgps6J69J2jY6r5ZG"),
		},
	}
	if err := client.Create(ctx, &secret); err != nil {
		return fmt.Errorf("failed to create htpasswd secret: %w", err)
	}
	DeferCleanup(func() {
		if err := client.Delete(context.Background(), &secret); err != nil && !apierrors.IsNotFound(err) {
			GinkgoWriter.Printf("Warning: failed to delete htpasswd secret: %v\n", err)
		}
	})

	if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
		return fmt.Errorf("failed to get fresh HostedCluster state: %w", err)
	}
	var originalOAuth *v1.OAuthSpec
	if hostedCluster.Spec.Configuration != nil && hostedCluster.Spec.Configuration.OAuth != nil {
		originalOAuth = hostedCluster.Spec.Configuration.OAuth.DeepCopy()
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
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := UpdateObject(cleanupCtx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			if obj.Spec.Configuration == nil {
				obj.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			}
			obj.Spec.Configuration.OAuth = originalOAuth
		}); err != nil {
			GinkgoWriter.Printf("Warning: failed to restore OAuth config: %v\n", err)
		}
	})

	if err := waitForOAuthConfig(ctx, client, hostedCluster); err != nil {
		return err
	}

	if transportFactory != nil {
		freshTransport, err := transportFactory(ctx)
		if err != nil {
			return fmt.Errorf("failed to create fresh OAuth transport after IDP rollout: %w", err)
		}
		transport = freshTransport
	}

	accessToken, err = waitForOAuthTokenWithTransport(ctx, oauthHost, transport, "testuser", "password")
	if err != nil {
		return err
	}
	user, err = getUserForToken(ctx, kasConfig, accessToken)
	if err != nil {
		return fmt.Errorf("failed to get user for testuser token: %w", err)
	}
	if user.Name != "testuser" {
		return fmt.Errorf("testuser token authenticated as %q, want %q", user.Name, "testuser")
	}

	return validateClusterPostIDP(ctx, client, hostedCluster)
}
