package globalps

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	corev1 "k8s.io/api/core/v1"
)

func TestValidateAdditionalPullSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr bool
	}{
		{
			name: "valid pull secret",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: composePullSecretBytes("quay.io", "dXNlcjpwYXNz"),
				},
			},
			wantErr: false,
		},
		{
			name: "missing docker config key",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"wrong-key": composePullSecretBytes("quay.io", "dXNlcjpwYXNz"),
				},
			},
			wantErr: true,
		},
		{
			name: "invalid json",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`invalid json`),
				},
			},
			wantErr: true,
		},
		{
			name: "empty auths",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateAdditionalPullSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAdditionalPullSecret() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMergePullSecrets(t *testing.T) {
	validAuth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	oldAuth := base64.StdEncoding.EncodeToString([]byte("olduser:oldpass"))

	tests := []struct {
		name             string
		originalSecret   []byte
		additionalSecret []byte
		wantErr          bool
		validateResult   func(t *testing.T, result []byte)
	}{
		{
			name:             "successful merge",
			originalSecret:   composePullSecretBytes("registry1", validAuth),
			additionalSecret: composePullSecretBytes("registry2", validAuth),
			wantErr:          false,
			validateResult: func(t *testing.T, result []byte) {
				var config credentialprovider.DockerConfigJSON
				if err := json.Unmarshal(result, &config); err != nil {
					t.Errorf("failed to unmarshal result: %v", err)
				}
				if len(config.Auths) != 2 {
					t.Errorf("expected 2 auth entries, got %d", len(config.Auths))
				}
				if _, ok := config.Auths["registry1"]; !ok {
					t.Error("missing registry1 in merged config")
				}
				if _, ok := config.Auths["registry2"]; !ok {
					t.Error("missing registry2 in merged config")
				}
			},
		},
		{
			name:             "overwrite existing registry",
			originalSecret:   composePullSecretBytes("registry1", oldAuth),
			additionalSecret: composePullSecretBytes("registry1", validAuth),
			wantErr:          false,
			validateResult: func(t *testing.T, result []byte) {
				var config credentialprovider.DockerConfigJSON
				if err := json.Unmarshal(result, &config); err != nil {
					t.Errorf("failed to unmarshal result: %v", err)
				}
				if len(config.Auths) != 1 {
					t.Errorf("expected 1 auth entry, got %d", len(config.Auths))
				}
				entry := config.Auths["registry1"]
				if entry.Username != "user" || entry.Password != "pass" {
					t.Errorf("registry1 credentials were not overwritten correctly, got username=%s, password=%s", entry.Username, entry.Password)
				}
			},
		},
		{
			name:             "invalid original secret",
			originalSecret:   []byte(`invalid json`),
			additionalSecret: composePullSecretBytes("registry1", validAuth),
			wantErr:          true,
		},
		{
			name:             "invalid additional secret",
			originalSecret:   composePullSecretBytes("registry1", validAuth),
			additionalSecret: []byte(`invalid json`),
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := MergePullSecrets(tt.originalSecret, tt.additionalSecret)
			if (err != nil) != tt.wantErr {
				t.Errorf("MergePullSecrets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validateResult != nil {
				tt.validateResult(t, result)
			}
		})
	}
}

func composePullSecretBytes(registry, authEntry string) []byte {
	return []byte(fmt.Sprintf(`{"auths":{"%s":{"auth":"%s"}}}`, registry, authEntry))
}
