package oauth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"

	osinv1 "github.com/openshift/api/osin/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SessionSecretsFileKey = "v4-0-config-system-session"
)

func ReconcileSessionSecret(secret *corev1.Secret, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	if value, exists := secret.Data[SessionSecretsFileKey]; exists {
		if validateSessionSecrets(value) {
			return nil
		}
	}
	sessionSecrets := generateSessionSecrets()
	encodedSessionSecrets := &bytes.Buffer{}
	if err := api.YamlSerializer.Encode(sessionSecrets, encodedSessionSecrets); err != nil {
		return fmt.Errorf("cannot encode session secrets: %w", err)
	}
	secret.Data[SessionSecretsFileKey] = encodedSessionSecrets.Bytes()
	return nil
}

func generateSessionSecrets() *osinv1.SessionSecrets {
	return &osinv1.SessionSecrets{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SessionSecrets",
			APIVersion: osinv1.GroupVersion.String(),
		},
		Secrets: []osinv1.SessionSecret{
			{
				Authentication: randomString(64),
				Encryption:     randomString(32),
			},
		},
	}
}

func validateSessionSecrets(value []byte) bool {
	sessionSecrets := &osinv1.SessionSecrets{}
	if _, _, err := api.YamlSerializer.Decode(value, nil, sessionSecrets); err != nil {
		return false
	}
	if len(sessionSecrets.Secrets) == 0 {
		return false
	}
	if len(sessionSecrets.Secrets[0].Authentication) == 0 || len(sessionSecrets.Secrets[0].Encryption) == 0 {
		return false
	}
	return true
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
