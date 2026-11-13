package ignitionpayload

import (
	"crypto/ecdsa"
	"encoding/base64"
	"fmt"
	"time"

	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	"k8s.io/utils/ptr"

	"github.com/clarketm/json"
	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/golang-jwt/jwt/v5"
)

const (
	// tokenIssuer identifies the generator that signed a bearer token.
	tokenIssuer = "ignition-payload-controller"
	// tokenAudience is the audience the serving tier validates a presented token against.
	tokenAudience = "ignition-server"

	// rolloutGenerationHeader is the static HTTP header baked into a node's userData that selects
	// which rollout generation the server resolves. It is a plain (unsigned) request input: the
	// token's name-scope is the authorization boundary, and only the two retained generations
	// resolve to a live store entry, so an unsigned selector introduces no escalation.
	rolloutGenerationHeader = "rollout-generation"

	// defaultTokenClockSkew is the leeway applied to the not-before claim so a first-booting node
	// whose clock is wrong before NTP is not rejected.
	defaultTokenClockSkew = 10 * time.Minute

	// userData Secret keys, matching the pointer-ignition contract CAPI consumes at first boot.
	userDataValueKey             = "value"
	userDataDisableTemplatingKey = "disableTemplating"

	// tokenIssuedAtAnnotation records when the userData Secret's token was last minted, so the
	// re-issue timer re-mints only once the token is past half its TTL.
	tokenIssuedAtAnnotation = "hypershift.openshift.io/ignition-payload-token-issued-at"
)

// TokenMinter mints bearer tokens for node ignition fetches. The token authenticates the fetch and
// authorizes the owner (name-scope); it never identifies the payload (the request path + the
// rollout-generation header do), and it is never written to the PayloadStore or to status — it lives
// only inside the generator-authored userData Secret.
type TokenMinter interface {
	// Mint returns a signed bearer token scoped to owner, valid for ttl.
	Mint(owner payloadstore.OwnerRef, ttl time.Duration) (string, error)
}

// jwtMinter mints ES256-signed JWT bearer tokens. The serving tier holds only the matching public
// key and verifies signature, audience, and expiry statelessly.
type jwtMinter struct {
	key       *ecdsa.PrivateKey
	issuer    string
	audience  string
	clockSkew time.Duration
	now       func() time.Time
}

// LoadECDSAPrivateKey parses a PEM-encoded ECDSA private key (the generator's token-signing key).
func LoadECDSAPrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	key, err := jwt.ParseECPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ECDSA token-signing key: %w", err)
	}
	return key, nil
}

// NewJWTMinter builds a minter that signs tokens with the given ECDSA private key.
func NewJWTMinter(key *ecdsa.PrivateKey) *jwtMinter {
	return &jwtMinter{
		key:       key,
		issuer:    tokenIssuer,
		audience:  tokenAudience,
		clockSkew: defaultTokenClockSkew,
		now:       time.Now,
	}
}

// Mint returns an ES256-signed JWT carrying the issuer, audience, the owner it is scoped to
// (subject + payloadNamespace/payloadName claims), and iat/nbf/exp. It deliberately does NOT carry
// the rollout generation: the serving selector is an unsigned request input (see
// rolloutGenerationHeader).
func (m *jwtMinter) Mint(owner payloadstore.OwnerRef, ttl time.Duration) (string, error) {
	now := m.now()
	claims := jwt.MapClaims{
		"iss":              m.issuer,
		"aud":              m.audience,
		"sub":              owner.Namespace + "/" + owner.Name,
		"payloadNamespace": owner.Namespace,
		"payloadName":      owner.Name,
		"iat":              now.Unix(),
		"nbf":              now.Add(-m.clockSkew).Unix(),
		"exp":              now.Add(ttl).Unix(),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(m.key)
	if err != nil {
		return "", fmt.Errorf("failed to sign ignition token: %w", err)
	}
	return signed, nil
}

// pointerIgnitionConfig builds the pointer-ignition config a node fetches with at first boot. It
// embeds the node-facing CA, the per-payload endpoint GET /ignition/<payloadName>, the bearer token
// (Authorization header), and the static rollout-generation selector header.
func pointerIgnitionConfig(caCert []byte, token, endpoint, payloadName string, rolloutGeneration int64) ignitionapi.Config {
	encodedCACert := base64.StdEncoding.EncodeToString(caCert)
	return ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
			Security: ignitionapi.Security{
				TLS: ignitionapi.TLS{
					CertificateAuthorities: []ignitionapi.Resource{
						{Source: ptr.To(fmt.Sprintf("data:text/plain;base64,%s", encodedCACert))},
					},
				},
			},
			Config: ignitionapi.IgnitionConfig{
				Merge: []ignitionapi.Resource{
					{
						Source: ptr.To(fmt.Sprintf("https://%s/ignition/%s", endpoint, payloadName)),
						HTTPHeaders: []ignitionapi.HTTPHeader{
							{Name: "Authorization", Value: ptr.To(fmt.Sprintf("Bearer %s", token))},
							{Name: rolloutGenerationHeader, Value: ptr.To(fmt.Sprintf("%d", rolloutGeneration))},
						},
					},
				},
			},
		},
	}
}

// marshalPointerIgnition serializes a pointer-ignition config into the userData Secret "value" bytes.
func marshalPointerIgnition(cfg ignitionapi.Config) ([]byte, error) {
	value, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pointer ignition config: %w", err)
	}
	return value, nil
}
