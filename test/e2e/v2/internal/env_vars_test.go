//go:build e2ev2

package internal

import "testing"

func TestGetEnvVarValueSharedOIDC(t *testing.T) {
	for _, name := range []string{"AWS_OIDC_S3_BUCKET_NAME", "AWS_OIDC_S3_CREDENTIALS_FILE", "AWS_OIDC_S3_REGION", "SSH_SOURCE_CIDR"} {
		t.Run("When "+name+" is registered, it should remain optional and preserve supplied configuration", func(t *testing.T) {
			spec, registered := envVarRegistry[name]
			if !registered || spec.Required || spec.Default != "" || spec.Description == "" {
				t.Fatalf("invalid optional shared OIDC input registration for %s", name)
			}
			t.Setenv(name, "")
			if value := GetEnvVarValue(name); value != "" {
				t.Fatalf("unset %s must retain consumer-selected fallback", name)
			}
			t.Setenv(name, "synthetic-configuration")
			if value := GetEnvVarValue(name); value != "synthetic-configuration" {
				t.Fatalf("configured %s did not reach its consumer", name)
			}
		})
	}
}
