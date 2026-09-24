package resources

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAWSConfigForRoleKeepsWebIdentityTokenOnSTSEndpoint(t *testing.T) {
	const projectedToken = "projected-web-identity-token"

	var stsRequests atomic.Int32
	stsToken := make(chan string, 1)
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stsRequests.Add(1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		stsToken <- r.Form.Get("WebIdentityToken")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>ASIATESTACCESSKEY</AccessKeyId>
      <SecretAccessKey>test-secret-key</SecretAccessKey>
      <SessionToken>test-session-token</SessionToken>
      <Expiration>2099-01-01T00:00:00Z</Expiration>
    </Credentials>
    <AssumedRoleUser>
      <AssumedRoleId>test-role-id</AssumedRoleId>
      <Arn>arn:aws:sts::123456789012:assumed-role/test-role/test-session</Arn>
    </AssumedRoleUser>
  </AssumeRoleWithWebIdentityResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</AssumeRoleWithWebIdentityResponse>`)
	}))
	defer stsServer.Close()

	var serviceEndpointRequests atomic.Int32
	serviceEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serviceEndpointRequests.Add(1)
		http.Error(w, "unexpected request to service endpoint", http.StatusInternalServerError)
	}))
	defer serviceEndpoint.Close()

	configDir := t.TempDir()
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("AWS_ENDPOINT_URL_STS", stsServer.URL)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(configDir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(configDir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(projectedToken), 0600); err != nil {
		t.Fatalf("failed to write projected token: %v", err)
	}
	config, err := awsConfigForRole(t.Context(), "us-east-1", "arn:aws:iam::123456789012:role/test-role", tokenFile, serviceEndpoint.URL)
	if err != nil {
		t.Fatalf("failed to configure AWS role: %v", err)
	}
	if _, err := config.Credentials.Retrieve(t.Context()); err != nil {
		t.Fatalf("failed to exchange projected token with STS: %v", err)
	}

	select {
	case got := <-stsToken:
		if got != projectedToken {
			t.Fatalf("expected STS to receive the projected token, got %q", got)
		}
	default:
		t.Fatal("expected STS to receive the web-identity request")
	}
	if got := stsRequests.Load(); got != 1 {
		t.Fatalf("expected one request to STS, got %d", got)
	}
	if got := serviceEndpointRequests.Load(); got != 0 {
		t.Fatalf("expected no requests to the ELB service endpoint, got %d", got)
	}
}

func TestAWSConfigForRoleWrapsDefaultConfigErrorWithRegion(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(configPath, []byte("[default"), 0600); err != nil {
		t.Fatalf("failed to write invalid AWS config: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	t.Setenv("AWS_PROFILE", "default")
	t.Setenv("AWS_SDK_LOAD_CONFIG", "1")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")

	_, err := awsConfigForRole(t.Context(), "us-east-1", "arn:aws:iam::123456789012:role/test-role", "", "")
	if err == nil || !strings.Contains(err.Error(), "failed to load AWS default config for region us-east-1") {
		t.Fatalf("expected AWS config error to include the region, got %v", err)
	}
}
