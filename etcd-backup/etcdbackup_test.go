package etcdbackup

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestNewStartCommand(t *testing.T) {
	g := NewWithT(t)
	cmd := NewStartCommand()

	g.Expect(cmd.Use).To(Equal("etcd-backup"))

	for _, flag := range []string{"backup-dir", "etcd-endpoint", "etcd-client-cert", "etcd-client-key", "etcd-ca-cert", "s3-bucket-name", "s3-bucket-region", "s3-key-prefix", "s3-object-tags"} {
		g.Expect(cmd.Flags().Lookup(flag)).ToNot(BeNil(), "expected flag %q to exist", flag)
	}

	// etcd-endpoint, s3-bucket-name and s3-key-prefix should be required
	err := cmd.ValidateRequiredFlags()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("etcd-endpoint"))
	g.Expect(err.Error()).To(ContainSubstring("s3-bucket-name"))
	g.Expect(err.Error()).To(ContainSubstring("s3-key-prefix"))
}

func TestNewStartCommandDefaults(t *testing.T) {
	g := NewWithT(t)
	cmd := NewStartCommand()

	g.Expect(cmd.Flags().Lookup("backup-dir").DefValue).To(Equal("/tmp"))
	g.Expect(cmd.Flags().Lookup("etcd-client-cert").DefValue).To(Equal(filepath.Join(DefaultCertsDir, "etcd-client.crt")))
	g.Expect(cmd.Flags().Lookup("etcd-client-key").DefValue).To(Equal(filepath.Join(DefaultCertsDir, "etcd-client.key")))
	g.Expect(cmd.Flags().Lookup("etcd-ca-cert").DefValue).To(Equal(filepath.Join(DefaultCertsDir, "ca.crt")))
}

func TestMapToTags(t *testing.T) {
	tests := []struct {
		name         string
		input        map[string]string
		validateFunc func(t *testing.T, result string)
	}{
		{
			name: "When tags are provided it should URL-encode them correctly",
			input: map[string]string{
				"env":            "production",
				"team":           "platform",
				"key with space": "value&special=chars@test",
			},
			validateFunc: func(t *testing.T, result string) {
				values, err := url.ParseQuery(result)
				if err != nil {
					t.Fatalf("result is not valid URL query string: %v", err)
				}

				if values.Get("env") != "production" {
					t.Errorf("expected env=production, got %s", values.Get("env"))
				}
				if values.Get("team") != "platform" {
					t.Errorf("expected team=platform, got %s", values.Get("team"))
				}
				if values.Get("key with space") != "value&special=chars@test" {
					t.Errorf("special chars not decoded correctly: %q", values.Get("key with space"))
				}

				if !strings.Contains(result, "%") {
					t.Errorf("expected URL encoding with %% escapes, got %q", result)
				}

				if strings.HasSuffix(result, "&") {
					t.Errorf("result should not have trailing &: %q", result)
				}
			},
		},
		{
			name:  "When map is empty or nil it should return empty string",
			input: nil,
			validateFunc: func(t *testing.T, result string) {
				if result != "" {
					t.Errorf("expected empty string, got %q", result)
				}

				emptyResult := aws.ToString(mapToTags(map[string]string{}))
				if emptyResult != "" {
					t.Errorf("expected empty string for empty map, got %q", emptyResult)
				}
			},
		},
		{
			name: "When single tag is provided it should not have ampersand",
			input: map[string]string{
				"env": "prod",
			},
			validateFunc: func(t *testing.T, result string) {
				if strings.Contains(result, "&") {
					t.Errorf("single tag should not contain &: %q", result)
				}
				if result != "env=prod" {
					t.Errorf("expected 'env=prod', got %q", result)
				}
			},
		},
		{
			name: "When tags contain complex values it should preserve them in round-trip",
			input: map[string]string{
				"url": "https://example.com?key=value",
				"key": "value&special=chars@test",
			},
			validateFunc: func(t *testing.T, result string) {
				decoded, err := url.ParseQuery(result)
				if err != nil {
					t.Fatalf("failed to parse encoded result: %v", err)
				}

				for k, expectedV := range map[string]string{
					"url": "https://example.com?key=value",
					"key": "value&special=chars@test",
				} {
					actualV := decoded.Get(k)
					if actualV != expectedV {
						t.Errorf("round-trip failed for key %q: expected %q, got %q", k, expectedV, actualV)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aws.ToString(mapToTags(tt.input))
			tt.validateFunc(t, result)
		})
	}
}
