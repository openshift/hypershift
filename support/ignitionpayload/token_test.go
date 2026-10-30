package ignitionpayload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/golang-jwt/jwt/v5"
)

func signToken(t *testing.T, key *ecdsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(key)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return signed
}

func validClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":                 TokenIssuer,
		"aud":                 TokenAudience,
		"sub":                 "hcp/np-1",
		ClaimPayloadNamespace: "hcp",
		ClaimPayloadName:      "np-1",
		"iat":                 now.Unix(),
		"nbf":                 now.Add(-DefaultTokenClockSkew).Unix(),
		"exp":                 now.Add(time.Hour).Unix(),
	}
}

func TestTokenVerifier_Verify(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	v := NewTokenVerifier(&key.PublicKey)
	now := time.Now()

	t.Run("a valid token resolves to its scoped owner", func(t *testing.T) {
		g := NewWithT(t)
		owner, err := v.Verify(signToken(t, key, validClaims(now)))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(owner).To(Equal(OwnerRef{Namespace: "hcp", Name: "np-1"}))
	})

	t.Run("a token signed by a different key is rejected", func(t *testing.T) {
		g := NewWithT(t)
		other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = v.Verify(signToken(t, other, validClaims(now)))
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})

	t.Run("a token for a different audience is rejected", func(t *testing.T) {
		g := NewWithT(t)
		c := validClaims(now)
		c["aud"] = "someone-else"
		_, err := v.Verify(signToken(t, key, c))
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})

	t.Run("a token from a different issuer is rejected", func(t *testing.T) {
		g := NewWithT(t)
		c := validClaims(now)
		c["iss"] = "someone-else"
		_, err := v.Verify(signToken(t, key, c))
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})

	t.Run("an expired token is rejected", func(t *testing.T) {
		g := NewWithT(t)
		c := validClaims(now)
		c["exp"] = now.Add(-24 * time.Hour).Unix()
		_, err := v.Verify(signToken(t, key, c))
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})

	t.Run("a token missing its owner scope is rejected", func(t *testing.T) {
		g := NewWithT(t)
		c := validClaims(now)
		delete(c, ClaimPayloadName)
		_, err := v.Verify(signToken(t, key, c))
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})

	t.Run("an HS256 token is rejected (algorithm confusion)", func(t *testing.T) {
		g := NewWithT(t)
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(now)).SignedString([]byte("secret"))
		g.Expect(err).ToNot(HaveOccurred())
		_, err = v.Verify(signed)
		g.Expect(err).To(MatchError(ErrTokenInvalid))
	})
}

func TestLoadECDSAPublicKey(t *testing.T) {
	g := NewWithT(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	g.Expect(err).ToNot(HaveOccurred())
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	g.Expect(err).ToNot(HaveOccurred())
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	loaded, err := LoadECDSAPublicKey(pemBytes)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(loaded.Equal(&key.PublicKey)).To(BeTrue())

	_, err = LoadECDSAPublicKey([]byte("not a key"))
	g.Expect(err).To(HaveOccurred())
}
