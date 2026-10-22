//go:build e2ev2

package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func sharedOIDCSDKEnvironment(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_SHARED_CREDENTIALS_FILE"} {
		t.Setenv(name, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("HOME", directory)
	configFile := filepath.Join(directory, "empty-config")
	if err := os.WriteFile(configFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configFile)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", configFile)
	return directory
}

func sharedOIDCSDKFile(t *testing.T, directory, name, section, identity string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	contents := "[" + section + "]\naws_access_key_id = " + identity + "\naws_secret_access_key = synthetic-not-a-secret\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSharedOIDCClients(t *testing.T) {
	for _, test := range []struct {
		name     string
		section  string
		profile  string
		fallback bool
		env      bool
	}{
		{name: "When explicit default files are supplied, it should resolve both independent principals", section: "default"},
		{name: "When AWS_PROFILE selects a shared config profile, it should preserve that profile for both clients", section: "profile ci", profile: "ci"},
		{name: "When AWS_PROFILE selects a credentials profile, it should read credentials sections too", section: "ci", profile: "ci"},
		{name: "When only AWS_SHARED_CREDENTIALS_FILE supplies a named shared config, it should use it as both file types", section: "profile ci", profile: "ci", fallback: true},
		{name: "When only environment credentials are supplied, it should use the SDK default chain", env: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := sharedOIDCSDKEnvironment(t)
			t.Setenv("AWS_PROFILE", test.profile)
			options := sharedOIDCTestOptions()
			wantIAM, wantS3 := "synthetic-iam", "synthetic-s3"
			if test.env {
				t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-environment")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-not-a-secret")
				options.IAMCredentialsFile, options.S3CredentialsFile = "", ""
				wantIAM, wantS3 = "synthetic-environment", "synthetic-environment"
			} else {
				options.IAMCredentialsFile = sharedOIDCSDKFile(t, directory, "iam", test.section, wantIAM)
				options.S3CredentialsFile = sharedOIDCSDKFile(t, directory, "s3", test.section, wantS3)
				if test.fallback {
					t.Setenv("AWS_SHARED_CREDENTIALS_FILE", options.IAMCredentialsFile)
					options.IAMCredentialsFile, options.S3CredentialsFile = "", ""
					wantS3 = wantIAM
				} else {
					t.Setenv("AWS_SHARED_CREDENTIALS_FILE", sharedOIDCSDKFile(t, directory, "unused-fallback", test.section, "synthetic-unused"))
				}
			}
			iamClient, s3Client, err := sharedOIDCClients(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			iamOptions := iamClient.(*iam.Client).Options()
			s3Options := s3Client.(*s3.Client).Options()
			for _, client := range []struct {
				region      string
				wantRegion  string
				credentials aws.CredentialsProvider
				identity    string
			}{{iamOptions.Region, options.IAMRegion, iamOptions.Credentials, wantIAM}, {s3Options.Region, options.S3Region, s3Options.Credentials, wantS3}} {
				credentials, err := client.credentials.Retrieve(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if credentials.AccessKeyID != client.identity || client.region != client.wantRegion {
					t.Fatal("real SDK selected a different principal or region")
				}
			}
		})
	}
	for _, target := range []string{"IAM", "S3"} {
		t.Run("When the "+target+" selected profile is missing, it should return the real SDK configuration error", func(t *testing.T) {
			directory := sharedOIDCSDKEnvironment(t)
			t.Setenv("AWS_PROFILE", "ci")
			options := sharedOIDCTestOptions()
			options.IAMCredentialsFile = sharedOIDCSDKFile(t, directory, "iam", "profile ci", "synthetic-iam")
			options.S3CredentialsFile = sharedOIDCSDKFile(t, directory, "s3", "profile ci", "synthetic-s3")
			invalid := sharedOIDCSDKFile(t, directory, "invalid", "default", "synthetic-default")
			if target == "IAM" {
				options.IAMCredentialsFile = invalid
			} else {
				options.S3CredentialsFile = invalid
			}
			_, _, err := sharedOIDCClients(t.Context(), options)
			var profileError config.SharedConfigProfileNotExistError
			if err == nil || !strings.Contains(err.Error(), target+" configuration") || !errors.As(err, &profileError) || profileError.Profile != "ci" {
				t.Fatal("real SDK profile error was not returned with client context")
			}
		})
	}
}

func TestSharedOIDCConfigOptions(t *testing.T) {
	t.Run("When no credential file is supplied anywhere, it should leave file and profile selection to the SDK", func(t *testing.T) {
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
		options := config.LoadOptions{}
		for _, option := range sharedOIDCConfigOptions("us-east-1", "") {
			if err := option(&options); err != nil {
				t.Fatal(err)
			}
		}
		if options.SharedConfigFiles != nil || options.SharedCredentialsFiles != nil || options.SharedConfigProfile != "" || options.Region != "us-east-1" {
			t.Fatal("empty credentials input overrode the SDK chain or profile")
		}
	})
}

func TestCleanupSharedOIDCCredentialRetry(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
	}{
		{name: "When owned cleanup is retried with explicit named-profile inputs, it should reload both persisted principals", mode: "explicit"},
		{name: "When owned cleanup is retried with an environment shared file, it should reload the named-profile fallback", mode: "fallback"},
		{name: "When owned cleanup is retried with environment credentials, it should retain SDK chain selection", mode: "environment"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := sharedOIDCSDKEnvironment(t)
			t.Setenv("AWS_PROFILE", "ci")
			options := sharedOIDCTestOptions()
			options.IAMCredentialsFile = sharedOIDCSDKFile(t, directory, "iam", "profile ci", "synthetic-iam")
			options.S3CredentialsFile = sharedOIDCSDKFile(t, directory, "s3", "profile ci", "synthetic-s3")
			wantIAM, wantS3 := "synthetic-iam", "synthetic-s3"
			switch test.mode {
			case "fallback":
				t.Setenv("AWS_SHARED_CREDENTIALS_FILE", options.IAMCredentialsFile)
				options.IAMCredentialsFile, options.S3CredentialsFile = "", ""
				wantS3 = wantIAM
			case "environment":
				t.Setenv("AWS_PROFILE", "")
				t.Setenv("AWS_ACCESS_KEY_ID", "synthetic-environment")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic-not-a-secret")
				options.IAMCredentialsFile, options.S3CredentialsFile = "", ""
				wantIAM, wantS3 = "synthetic-environment", "synthetic-environment"
			}
			cloud := &sharedOIDCFake{}
			loads := 0
			clients := func(ctx context.Context, options SharedOIDCOptions) (sharedOIDCIAM, sharedOIDCS3, error) {
				iamClient, s3Client, err := sharedOIDCClients(ctx, options)
				if err != nil {
					return nil, nil, err
				}
				for _, client := range []struct {
					credentials aws.CredentialsProvider
					identity    string
				}{{iamClient.(*iam.Client).Options().Credentials, wantIAM}, {s3Client.(*s3.Client).Options().Credentials, wantS3}} {
					credentials, err := client.credentials.Retrieve(ctx)
					if err != nil || credentials.AccessKeyID != client.identity {
						t.Fatal("cleanup retry failed to select the persisted principal")
					}
				}
				loads++
				return cloud.clients(ctx, options)
			}
			inputs, err := setupSharedOIDC(t.Context(), directory, options, clients)
			if err != nil {
				t.Fatal(err)
			}
			cloud.deleteError = errSharedOIDCFake
			if err := cleanupSharedOIDC(t.Context(), directory, clients); !errors.Is(err, errSharedOIDCFake) {
				t.Fatal("cleanup failure lost its original error")
			}
			for _, path := range []string{inputs.SigningKeyFile, filepath.Join(directory, SharedOIDCStateFileName)} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("failed cleanup did not preserve retry ownership state")
				}
			}
			cloud.deleteError = nil
			if err := cleanupSharedOIDC(t.Context(), directory, clients); err != nil {
				t.Fatal(err)
			}
			if loads != 3 || len(cloud.objects) != 0 || cloud.providerARN != "" {
				t.Fatal("cleanup retry did not reload credentials and finish owned deletion")
			}
		})
	}
}
