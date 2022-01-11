package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	oauthv1 "github.com/openshift/api/oauth/v1"
)

func ReconcileOAuthServerCertCABundle(cm *corev1.ConfigMap, sourceSecret *corev1.Secret) error {
	if _, hasCertKey := sourceSecret.Data[corev1.TLSCertKey]; !hasCertKey {
		return fmt.Errorf("source secret %s/%s does not have a cert key", sourceSecret.Namespace, sourceSecret.Name)
	}
	cm.Data = map[string]string{}
	cm.Data["ca-bundle.crt"] = string(sourceSecret.Data[corev1.TLSCertKey])
	return nil
}

func ReconcileBrowserClient(client *oauthv1.OAuthClient, externalHost string, externalPort int32) error {
	redirectURIs := []string{
		fmt.Sprintf("https://%s:%d/oauth/token/display", externalHost, externalPort),
	}
	return reconcileOAuthClient(client, redirectURIs, false, true)
}

func ReconcileChallengingClient(client *oauthv1.OAuthClient, externalHost string, externalPort int32) error {
	redirectURIs := []string{
		fmt.Sprintf("https://%s:%d/oauth/token/implicit", externalHost, externalPort),
	}
	return reconcileOAuthClient(client, redirectURIs, true, false)
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

// randomString uses RawURLEncoding to ensure we do not get / characters or trailing ='s
func randomString(size int) string {
	// each byte (8 bits) gives us 4/3 base64 (6 bits) characters
	// we account for that conversion and add one to handle truncation
	b64size := base64.RawURLEncoding.DecodedLen(size) + 1
	// trim down to the original requested size since we added one above
	return base64.RawURLEncoding.EncodeToString(randomBytes(b64size))[:size]
}

func randomBytes(size int) []byte {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}
