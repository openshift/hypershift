package ignitionpayload

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// The bearer-token contract is shared between the generator (which mints tokens) and the serving
// tier (which verifies them). Both sides reference these constants so the audience, issuer, claim
// names, and selector header can never drift apart. The token authenticates the fetch and
// authorizes the owner (name-scope); it never identifies the payload — the request path and the
// rollout-generation header do — and it is never written to the PayloadStore or to status.
const (
	// TokenIssuer identifies the generator that signed a bearer token.
	TokenIssuer = "ignition-payload-controller"
	// TokenAudience is the audience the serving tier validates a presented token against.
	TokenAudience = "ignition-server"

	// RolloutGenerationHeader is the static HTTP header baked into a node's userData that selects
	// which rollout generation the server resolves. It is a plain (unsigned) request input: the
	// token's name-scope is the authorization boundary, and only the two retained generations
	// resolve to a live store entry, so an unsigned selector introduces no escalation.
	RolloutGenerationHeader = "rollout-generation"

	// DefaultTokenClockSkew is the leeway applied to the time claims so a first-booting node whose
	// clock is wrong before NTP is not rejected.
	DefaultTokenClockSkew = 10 * time.Minute
)

// HashOwner returns the opaque subject a token carries for owner: a hash of "namespace/name". The
// token carries this hash rather than the namespace and name in cleartext, so the control-plane
// namespace (internal information that the data plane has no need for) is never disclosed to a node.
// The serving tier matches a presented token by hashing the owner it expects for the requested path
// and comparing against the token's subject.
func HashOwner(owner OwnerRef) string {
	sum := sha256.Sum256([]byte(owner.Namespace + "/" + owner.Name))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ErrTokenInvalid is returned by TokenVerifier.Verify for any token that fails signature, issuer,
// audience, time, or scope validation. The serving tier maps it to HTTP 401.
var ErrTokenInvalid = errors.New("invalid ignition token")

// TokenVerifier validates generator-signed bearer tokens statelessly against a public key. It holds
// only the public half of the signing key, so the stateless, horizontally-scaled serving tier can
// authenticate node fetches without a shared secret and without any store lookup.
type TokenVerifier struct {
	key      *ecdsa.PublicKey
	issuer   string
	audience string
	leeway   time.Duration
}

// NewTokenVerifier builds a verifier for ES256 tokens signed by the matching private key.
func NewTokenVerifier(pub *ecdsa.PublicKey) *TokenVerifier {
	return &TokenVerifier{
		key:      pub,
		issuer:   TokenIssuer,
		audience: TokenAudience,
		leeway:   DefaultTokenClockSkew,
	}
}

// Verify validates the token's signature, issuer, audience, and time claims and returns its opaque
// subject — a HashOwner of the owner the token is scoped to. It deliberately does NOT check the
// rollout generation: the serving selector is an unsigned request input (the rollout-generation
// header), and the token's name-scope — not the generation — is the authorization boundary (see
// RolloutGenerationHeader). The caller must still confirm the returned subject matches HashOwner of
// the owner authorized for the requested path. WithValidMethods already restricts the accepted signing
// method to ES256, so the key function does not re-check it.
func (v *TokenVerifier) Verify(tokenString string) (string, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(*jwt.Token) (any, error) {
		return v.key, nil
	},
		jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithLeeway(v.leeway),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	subject, _ := claims["sub"].(string)
	if subject == "" {
		return "", fmt.Errorf("%w: token is missing its subject", ErrTokenInvalid)
	}
	return subject, nil
}

// LoadECDSAPublicKey parses a PEM-encoded ECDSA public key (the token-verification key the serving
// tier is provisioned with). It accepts a PKIX public key or an X.509 certificate PEM.
func LoadECDSAPublicKey(pemBytes []byte) (*ecdsa.PublicKey, error) {
	key, err := jwt.ParseECPublicKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ECDSA token-verification key: %w", err)
	}
	return key, nil
}
