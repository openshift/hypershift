package dockercredentials

import (
	"net/url"
	"strings"

	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"

	"github.com/docker/distribution/registry/client/auth"
)

// NewFromFile creates a new credential store for the provided Docker config.json
// authentication file.
func NewFromFile(path string) (auth.CredentialStore, error) {
	cfg, err := credentialprovider.ReadSpecificDockerConfigJSONFile(path)
	if err != nil {
		return nil, err
	}
	keyring := &credentialprovider.BasicDockerKeyring{}
	keyring.Add(cfg)
	return &keyringCredentialStore{
		DockerKeyring:     keyring,
		RefreshTokenStore: registryclient.NewRefreshTokenStore(),
	}, nil
}

func NewFromBytes(b []byte) (auth.CredentialStore, error) {
	cfg, err := credentialprovider.ReadDockerConfigJSONFileFromBytes(b)
	if err != nil {
		return nil, err
	}
	keyring := &credentialprovider.BasicDockerKeyring{}
	keyring.Add(cfg)
	return &keyringCredentialStore{
		DockerKeyring:     keyring,
		RefreshTokenStore: registryclient.NewRefreshTokenStore(),
	}, nil
}

type keyringCredentialStore struct {
	credentialprovider.DockerKeyring
	registryclient.RefreshTokenStore
}

func (s *keyringCredentialStore) Basic(url *url.URL) (string, string) {
	return BasicFromKeyring(s.DockerKeyring, url)
}

// BasicFromKeyring finds Basic authorization credentials from a Docker keyring for the given URL as username and
// password. It returns empty strings if no such URL matches.
func BasicFromKeyring(keyring credentialprovider.DockerKeyring, target *url.URL) (string, string) {
	// TODO: compare this logic to Docker authConfig in v2 configuration
	var value string
	if len(target.Scheme) == 0 || target.Scheme == "https" {
		value = target.Host + target.Path
	} else {
		// always require an explicit port to look up HTTP credentials
		if !strings.Contains(target.Host, ":") {
			value = target.Host + ":80" + target.Path
		} else {
			value = target.Host + target.Path
		}
	}

	// Lookup(...) expects an image (not a URL path).
	// The keyring strips /v1/ and /v2/ version prefixes,
	// so we should also when selecting a valid auth for a URL.
	pathWithSlash := target.Path + "/"
	if strings.HasPrefix(pathWithSlash, "/v1/") || strings.HasPrefix(pathWithSlash, "/v2/") {
		value = target.Host + target.Path[3:]
	}

	configs, found := keyring.Lookup(value)

	if !found || len(configs) == 0 {
		// do a special case check for docker.io to match historical lookups when we respond to a challenge
		if value == "auth.docker.io/token" {
			return BasicFromKeyring(keyring, &url.URL{Host: "index.docker.io", Path: "/v1"})
		}
		// docker 1.9 saves 'docker.io' in config in f23, see https://bugzilla.redhat.com/show_bug.cgi?id=1309739
		if value == "index.docker.io" {
			return BasicFromKeyring(keyring, &url.URL{Host: "docker.io"})
		}

		// try removing the canonical ports for the given requests
		if (strings.HasSuffix(target.Host, ":443") && target.Scheme == "https") ||
			(strings.HasSuffix(target.Host, ":80") && target.Scheme == "http") {
			host := strings.SplitN(target.Host, ":", 2)[0]

			return BasicFromKeyring(keyring, &url.URL{Scheme: target.Scheme, Host: host, Path: target.Path})
		}
		return "", ""
	}
	return configs[0].Username, configs[0].Password
}
