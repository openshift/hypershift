package dockercredentials

import (
	"net/url"
	"testing"

	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
)

func TestBasicFromKeyringWithPathHierarchy(t *testing.T) {
	tests := []struct {
		name       string
		pullSecret credentialprovider.DockerConfig
		lookupURL  *url.URL
		expectUser string
		expectPass string
	}{
		{
			name: "FQDN hostname-only key matches sub-path challenge URL",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com": {Username: "user1", Password: "pass1"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "harbor.example.com", Path: "/service/token"},
			expectUser: "user1",
			expectPass: "pass1",
		},
		{
			name: "FQDN hostname-only key matches multi-level v2 image path",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com": {Username: "user2", Password: "pass2"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "harbor.example.com", Path: "/v2/redhat/operator-index/manifests/v4.20"},
			expectUser: "user2",
			expectPass: "pass2",
		},
		{
			name: "exact path key matches directly without hierarchy walk",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com/redhat/operator-index": {Username: "exact", Password: "exact-pass"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "harbor.example.com", Path: "/redhat/operator-index"},
			expectUser: "exact",
			expectPass: "exact-pass",
		},
		{
			name: "intermediate path key found via hierarchy walk",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com/redhat": {Username: "mid", Password: "mid-pass"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "harbor.example.com", Path: "/redhat/operator-index"},
			expectUser: "mid",
			expectPass: "mid-pass",
		},
		{
			name: "no match when hostname differs",
			pullSecret: credentialprovider.DockerConfig{
				"other-registry.example.com": {Username: "user3", Password: "pass3"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "harbor.example.com", Path: "/service/token"},
			expectUser: "",
			expectPass: "",
		},
		{
			name: "quay.io direct match works without hierarchy walk",
			pullSecret: credentialprovider.DockerConfig{
				"quay.io": {Username: "quay-user", Password: "quay-pass"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "quay.io", Path: ""},
			expectUser: "quay-user",
			expectPass: "quay-pass",
		},
		{
			name: "registry with port matches sub-path via hierarchy",
			pullSecret: credentialprovider.DockerConfig{
				"registry.example.com:5000": {Username: "port-user", Password: "port-pass"},
			},
			lookupURL:  &url.URL{Scheme: "https", Host: "registry.example.com:5000", Path: "/v2/library/nginx/manifests/latest"},
			expectUser: "port-user",
			expectPass: "port-pass",
		},
		{
			name: "HTTP with explicit port matches sub-path via hierarchy",
			pullSecret: credentialprovider.DockerConfig{
				"registry.example.com:8080": {Username: "http-user", Password: "http-pass"},
			},
			lookupURL:  &url.URL{Scheme: "http", Host: "registry.example.com:8080", Path: "/myapp/image"},
			expectUser: "http-user",
			expectPass: "http-pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring := &credentialprovider.BasicDockerKeyring{}
			keyring.Add(tt.pullSecret)

			user, pass := BasicFromKeyring(keyring, tt.lookupURL)

			if user != tt.expectUser {
				t.Errorf("expected username %q, got %q", tt.expectUser, user)
			}
			if pass != tt.expectPass {
				t.Errorf("expected password %q, got %q", tt.expectPass, pass)
			}
		})
	}
}

func TestLookupByPathHierarchy(t *testing.T) {
	tests := []struct {
		name       string
		pullSecret credentialprovider.DockerConfig
		value      string
		expectUser string
		expectPass string
	}{
		{
			name: "walks up to hostname from two-level path",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com": {Username: "found", Password: "found-pass"},
			},
			value:      "harbor.example.com/redhat/operator-index",
			expectUser: "found",
			expectPass: "found-pass",
		},
		{
			name: "finds match at intermediate path level",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com/redhat": {Username: "mid", Password: "mid-pass"},
			},
			value:      "harbor.example.com/redhat/operator-index",
			expectUser: "mid",
			expectPass: "mid-pass",
		},
		{
			name: "no match returns empty",
			pullSecret: credentialprovider.DockerConfig{
				"other.example.com": {Username: "nope", Password: "nope"},
			},
			value:      "harbor.example.com/redhat/operator-index",
			expectUser: "",
			expectPass: "",
		},
		{
			name: "single path component walks to hostname",
			pullSecret: credentialprovider.DockerConfig{
				"harbor.example.com": {Username: "host", Password: "host-pass"},
			},
			value:      "harbor.example.com/myimage",
			expectUser: "host",
			expectPass: "host-pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring := &credentialprovider.BasicDockerKeyring{}
			keyring.Add(tt.pullSecret)

			user, pass := lookupByPathHierarchy(keyring, tt.value)
			if user != tt.expectUser {
				t.Errorf("expected username %q, got %q", tt.expectUser, user)
			}
			if pass != tt.expectPass {
				t.Errorf("expected password %q, got %q", tt.expectPass, pass)
			}
		})
	}
}
