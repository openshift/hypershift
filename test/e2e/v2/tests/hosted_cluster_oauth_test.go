//go:build e2ev2

package tests

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	hcpmanifests "github.com/openshift/hypershift/pkg/manifests/cpo"
	configmanifests "github.com/openshift/hypershift/pkg/manifests/hcco"
	oauthconst "github.com/openshift/hypershift/pkg/oauth"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"
	oauthv1 "github.com/openshift/api/oauth/v1"
	osinv1 "github.com/openshift/api/osin/v1"
	userv1client "github.com/openshift/client-go/user/clientset/versioned/typed/user/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"golang.org/x/crypto/bcrypt"
)

func RegisterHostedClusterOAuthTests(getTestCtx internal.TestContextGetter) {
	It("should replace kubeadmin login with an HTPasswd identity provider and restore the original configuration", func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if hc.Spec.Configuration != nil && hc.Spec.Configuration.OAuth != nil && len(hc.Spec.Configuration.OAuth.IdentityProviders) > 0 {
			Skip("OAuth lifecycle requires a hosted cluster with the initial kubeadmin identity provider")
		}
		Expect(hc.Status.KubeadminPassword).NotTo(BeNil())
		config, err := tc.GetHostedClusterRESTConfig(hc)
		Expect(err).NotTo(HaveOccurred())
		client, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		userAdministrator, err := userv1client.NewForConfig(config)
		Expect(err).NotTo(HaveOccurred())
		passwordSecret := configmanifests.KubeadminPasswordSecret(tc.ControlPlaneNamespace)
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(passwordSecret), passwordSecret)).To(Succeed())
		deployment := configmanifests.OAuthDeployment(tc.ControlPlaneNamespace)
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
		Expect(deployment.Spec.Template.Annotations).To(HaveKey(oauthconst.KubeadminSecretHashAnnotation))
		route := hcpmanifests.OauthServerExternalPublicRoute(tc.ControlPlaneNamespace)
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(route), route)).To(Succeed())
		Expect(route.Spec.Host).NotTo(BeEmpty())
		authenticateOAuthUser(tc, config, client, route.Spec.Host, "kubeadmin", string(passwordSecret.Data["password"]), "kube:admin")
		username := "hypershift-e2e-" + utilrand.String(8)
		password := utilrand.String(32)
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, GenerateName: "e2e-htpasswd-"}, Data: map[string][]byte{"htpasswd": []byte(username + ":" + string(hashedPassword))}}
		Expect(tc.MgmtClient.Create(tc.Context, secret)).To(Succeed())
		restorationVerified := false
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
			defer cancel()
			cleanupTC := *tc
			cleanupTC.Context = cleanupContext
			tc := &cleanupTC
			Expect(deleteOAuthIdentitySecret(tc.Context, tc.MgmtClient, crclient.ObjectKeyFromObject(hc), secret, restorationVerified)).To(Succeed())
		})
		originalConfiguration := hc.Spec.Configuration.DeepCopy()
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
			defer cancel()
			cleanupTC := *tc
			cleanupTC.Context = cleanupContext
			tc := &cleanupTC
			Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
				latest, err := tc.GetHostedCluster()
				if err != nil {
					return err
				}
				latest.Spec.Configuration = originalConfiguration.DeepCopy()
				return tc.MgmtClient.Update(tc.Context, latest)
			})).To(Succeed())
			Eventually(func(g Gomega) {
				latest, err := tc.GetHostedCluster()
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(latest.Status.KubeadminPassword).NotTo(BeNil())
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(passwordSecret), passwordSecret)).To(Succeed())
				g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
				g.Expect(deployment.Spec.Template.Annotations).To(HaveKey(oauthconst.KubeadminSecretHashAnnotation))
			}, 10*time.Minute, 5*time.Second).Should(Succeed())
			restorationVerified = true
		})
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest, err := tc.GetHostedCluster()
			if err != nil {
				return err
			}
			if latest.Spec.Configuration == nil {
				latest.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			}
			latest.Spec.Configuration.OAuth = &configv1.OAuthSpec{IdentityProviders: []configv1.IdentityProvider{{
				Name: "e2e-htpasswd", MappingMethod: configv1.MappingMethodClaim,
				IdentityProviderConfig: configv1.IdentityProviderConfig{Type: configv1.IdentityProviderTypeHTPasswd, HTPasswd: &configv1.HTPasswdIdentityProvider{FileData: configv1.SecretNameReference{Name: secret.Name}}},
			}}}
			return tc.MgmtClient.Update(tc.Context, latest)
		})).To(Succeed())
		Eventually(func(g Gomega) {
			configMap := &corev1.ConfigMap{}
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "oauth-openshift"}, configMap)).To(Succeed())
			serverConfig := &osinv1.OsinServerConfig{}
			_, _, err := hyperapi.YamlSerializer.Decode([]byte(configMap.Data["config.yaml"]), nil, serverConfig)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(serverConfig.OAuthConfig.IdentityProviders).To(ContainElement(HaveField("Name", "e2e-htpasswd")))
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
		DeferCleanup(func() {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
			defer cancel()
			cleanupTC := *tc
			cleanupTC.Context = cleanupContext
			tc := &cleanupTC
			identityError := userAdministrator.Identities().Delete(tc.Context, "e2e-htpasswd:"+username, metav1.DeleteOptions{})
			if !apierrors.IsNotFound(identityError) {
				Expect(identityError).NotTo(HaveOccurred())
			}
			userError := userAdministrator.Users().Delete(tc.Context, username, metav1.DeleteOptions{})
			if !apierrors.IsNotFound(userError) {
				Expect(userError).NotTo(HaveOccurred())
			}
		})
		authenticateOAuthUser(tc, config, client, route.Spec.Host, username, password, username)
		Eventually(func(g Gomega) {
			latest, err := tc.GetHostedCluster()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(latest.Status.KubeadminPassword).To(BeNil())
			g.Expect(apierrors.IsNotFound(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(passwordSecret), &corev1.Secret{}))).To(BeTrue())
			g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
			g.Expect(deployment.Spec.Template.Annotations).NotTo(HaveKey(oauthconst.KubeadminSecretHashAnnotation))
		}, 10*time.Minute, 5*time.Second).Should(Succeed())
	})
}

func deleteOAuthIdentitySecret(ctx context.Context, management crclient.Client, clusterKey crclient.ObjectKey, secret *corev1.Secret, restorationVerified bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !restorationVerified {
		return fmt.Errorf("retaining OAuth Secret %s/%s because configuration restoration was not verified", secret.Namespace, secret.Name)
	}
	cluster := &hyperv1.HostedCluster{}
	if err := management.Get(ctx, clusterKey, cluster); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("verify restored OAuth Secret references: %w", err)
	}
	if cluster.Spec.Configuration != nil && slices.Contains(configrefs.SecretRefs(cluster.Spec.Configuration), secret.Name) {
		return fmt.Errorf("retaining OAuth Secret %s/%s because the HostedCluster still references it", secret.Namespace, secret.Name)
	}
	if err := management.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete detached OAuth Secret: %w", err)
	}
	return nil
}

func authenticateOAuthUser(tc *internal.TestContext, config *rest.Config, administrator crclient.Client, hostname, username, password, expectedUser string) {
	oauthConfig := rest.AnonymousClientConfig(config)
	oauthConfig.ServerName = hostname
	transport, err := rest.TransportFor(oauthConfig)
	Expect(err).NotTo(HaveOccurred())
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	Eventually(func() error {
		request, err := http.NewRequestWithContext(tc.Context, http.MethodHead, "https://"+hostname+"/healthz", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("OAuth health endpoint returned HTTP %d", response.StatusCode)
		}
		return nil
	}, 5*time.Minute, time.Second).Should(Succeed())
	var accessToken string
	Eventually(func() error {
		request, err := http.NewRequestWithContext(tc.Context, http.MethodGet, "https://"+hostname+"/oauth/authorize?response_type=token&client_id=openshift-challenging-client", nil)
		if err != nil {
			return err
		}
		request.SetBasicAuth(username, password)
		request.Header.Set("X-CSRF-Token", "1")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		accessToken, err = oauthAccessToken(response)
		return err
	}, 5*time.Minute, time.Second).Should(Succeed(), "identity provider must issue an OAuth access token")
	tokenHash := sha256.Sum256([]byte(accessToken))
	tokenName := "sha256~" + base64.RawURLEncoding.EncodeToString(tokenHash[:])
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
		defer cancel()
		cleanupTC := *tc
		cleanupTC.Context = cleanupContext
		tc := &cleanupTC
		err := administrator.Delete(tc.Context, &oauthv1.OAuthAccessToken{ObjectMeta: metav1.ObjectMeta{Name: tokenName}})
		if !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})
	userConfig := rest.AnonymousClientConfig(config)
	userConfig.BearerToken = accessToken
	userClient, err := userv1client.NewForConfig(userConfig)
	Expect(err).NotTo(HaveOccurred())
	user, err := userClient.Users().Get(tc.Context, "~", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	Expect(user.Name).To(Equal(expectedUser))
}

func oauthAccessToken(response *http.Response) (string, error) {
	if response.StatusCode != http.StatusFound {
		return "", fmt.Errorf("OAuth authorization returned HTTP %d", response.StatusCode)
	}
	location, err := response.Location()
	if err != nil {
		return "", fmt.Errorf("OAuth authorization did not supply a valid redirect")
	}
	fragment, err := url.ParseQuery(location.Fragment)
	if err != nil || fragment.Get("access_token") == "" {
		return "", fmt.Errorf("OAuth redirect did not supply an access token")
	}
	return fragment.Get("access_token"), nil
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:OAuth] Hosted Cluster OAuth Lifecycle", Label("hosted-cluster-oauth"), Serial, func() {
	RegisterHostedClusterOAuthTests(internal.GetTestContext)
})
