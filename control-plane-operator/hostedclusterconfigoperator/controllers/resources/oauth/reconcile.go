package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/certs"

	oauthv1 "github.com/openshift/api/oauth/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func ReconcileOAuthServerCertCABundle(cm *corev1.ConfigMap, sourceBundle *corev1.ConfigMap) error {
	cm.Data = map[string]string{}
	cm.Data["ca-bundle.crt"] = string(sourceBundle.Data[certs.CASignerCertMapKey])
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

func ReconcileCLIClient(client *oauthv1.OAuthClient, externalHost string, externalPort int32) error {
	redirectURIs := []string{
		"http://127.0.0.1/callback",
		"http://[::1]/callback",
	}
	return reconcileOAuthClient(client, redirectURIs, false, false)
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

func ReconcileOauthServingCertRole(role *rbacv1.Role) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			ResourceNames: []string{"oauth-serving-cert"},
			Resources:     []string{"configmaps"},
			Verbs:         []string{"get", "list", "watch"},
		},
	}
	return nil
}

func ReconcileOauthServingCertRoleBinding(role *rbacv1.RoleBinding) error {
	role.RoleRef = rbacv1.RoleRef{
		APIGroup: "rbac.authorization.k8s.io",
		Kind:     "Role",
		Name:     manifests.OAuthServingCertRole().Name,
	}
	role.Subjects = []rbacv1.Subject{
		{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Group",
			Name:     "system:authenticated",
		},
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
