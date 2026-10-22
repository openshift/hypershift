//go:build e2ev2

package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
)

func TestLoadAWSNodePoolConfig(t *testing.T) {
	for _, test := range []struct {
		name     string
		section  string
		profile  string
		fallback bool
		env      bool
		invalid  bool
	}{
		{name: "When the default credentials section is supplied, it should resolve its credentials", section: "default"},
		{name: "When AWS_PROFILE selects a profile-prefixed config, it should load it as shared config", section: "profile ci", profile: "ci"},
		{name: "When AWS_PROFILE selects a credentials section, it should load it as shared credentials", section: "ci", profile: "ci"},
		{name: "When the explicit file is empty, it should load AWS_SHARED_CREDENTIALS_FILE as both file types", section: "profile ci", profile: "ci", fallback: true},
		{name: "When environment credentials are supplied, it should preserve default chain precedence over files", section: "default", env: true},
		{name: "When AWS_PROFILE is missing from the file, it should return the SDK profile error", section: "default", profile: "ci", invalid: true},
		{name: "When the selected profile has incomplete credentials, it should return the SDK configuration error", section: "profile ci", profile: "ci", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI"} {
				t.Setenv(name, "")
			}
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("HOME", directory)
			emptyFile := filepath.Join(directory, "empty-config")
			if err := os.WriteFile(emptyFile, nil, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_CONFIG_FILE", emptyFile)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", emptyFile)
			t.Setenv("AWS_PROFILE", test.profile)
			credentialsFile := filepath.Join(directory, "credentials")
			contents := "[" + test.section + "]\naws_access_key_id = synthetic-nodepool\n"
			if !test.invalid || test.section == "default" {
				contents += "aws_secret_access_key = synthetic-not-a-secret\n"
			}
			if err := os.WriteFile(credentialsFile, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			wantIdentity := "synthetic-nodepool"
			if test.fallback {
				t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentialsFile)
				credentialsFile = ""
			}
			if test.env {
				t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-environment")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-not-a-secret")
				credentialsFile = ""
				wantIdentity = "synthetic-environment"
			}
			cloudConfig, err := loadAWSNodePoolConfig(t.Context(), "us-west-2", credentialsFile)
			if test.invalid {
				if err == nil {
					t.Fatal("real SDK configuration error was discarded")
				}
				if test.section == "default" {
					var profileError config.SharedConfigProfileNotExistError
					if !errors.As(err, &profileError) || profileError.Profile != "ci" {
						t.Fatal("SDK profile error lost its original type or selected profile")
					}
				} else if !strings.Contains(err.Error(), "partial credentials") {
					t.Fatal("SDK incomplete-credentials error was not propagated")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := cloudConfig.Credentials.Retrieve(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if credentials.AccessKeyID != wantIdentity || cloudConfig.Region != "us-west-2" {
				t.Fatal("real SDK selected a different principal or region")
			}
		})
	}
}
