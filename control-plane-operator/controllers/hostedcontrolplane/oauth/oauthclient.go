package oauth

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileBrowserClientWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, externalHost string, externalPort int32) error {
	ownerRef.ApplyTo(cm)
	browserClient := manifests.OAuthServerBrowserClient()
	if data, exists := cm.Data[util.UserDataKey]; exists {
		// Ignore a decoding error. If not decoded, content will be overwritten
		json.Unmarshal([]byte(data), browserClient)
	}
	redirectURIs := []string{
		fmt.Sprintf("https://%s:%d/oauth/token/display", externalHost, externalPort),
	}
	if err := reconcileOAuthClient(browserClient, redirectURIs, false, true); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, browserClient)
}

func ReconcileChallengingClientWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, externalHost string, externalPort int32) error {
	ownerRef.ApplyTo(cm)
	challengingClient := manifests.OAuthServerChallengingClient()
	redirectURIs := []string{
		fmt.Sprintf("https://%s:%d/oauth/token/implicit", externalHost, externalPort),
	}
	if err := reconcileOAuthClient(challengingClient, redirectURIs, true, false); err != nil {
		return err
	}
	return util.ReconcileWorkerManifest(cm, challengingClient)
}

func reconcileOAuthClient(client *oauthv1.OAuthClient, redirectURIs []string, respondWithChallenges bool, setSecret bool) error {
	client.RedirectURIs = redirectURIs
	client.RespondWithChallenges = respondWithChallenges
	client.GrantMethod = oauthv1.GrantHandlerAuto
	if setSecret && len(client.Secret) == 0 {
		client.Secret = randomString(32)
	}
	return nil
}
