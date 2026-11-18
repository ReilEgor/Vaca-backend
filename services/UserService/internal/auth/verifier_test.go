package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

const testIssuer = "http://keycloak/realms/vaca"

// staticKey verifies tokens with one public key, like a JWKS that holds a single key.
func staticKey(key *rsa.PublicKey) jwt.Keyfunc {
	return func(*jwt.Token) (any, error) { return key, nil }
}

func sign(t *testing.T, key *rsa.PrivateKey, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	return tok
}

func validClaims(roles ...string) jwt.MapClaims {
	return jwt.MapClaims{
		"sub":                "user-1",
		"iss":                testIssuer,
		"exp":                time.Now().Add(time.Hour).Unix(),
		"email":              "a@b.c",
		"preferred_username": "ann",
		"realm_access":       map[string]any{"roles": roles},
	}
}

func TestKeycloakVerifier(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	v := newVerifier(staticKey(&key.PublicKey), testIssuer)
	ctx := context.Background()

	t.Run("valid token maps to claims", func(t *testing.T) {
		got, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, validClaims("offline_access", "manager")))

		require.NoError(t, vErr)
		assert.Equal(t, domain.Claims{
			Subject: "user-1", Email: "a@b.c", Username: "ann", Role: domain.RoleManager,
		}, got)
	})

	t.Run("highest role wins and unknown roles are ignored", func(t *testing.T) {
		got, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, validClaims("user", "ADMIN", "manager", "x")))

		require.NoError(t, vErr)
		assert.Equal(t, domain.RoleAdmin, got.Role)
	})

	t.Run("no known role means user", func(t *testing.T) {
		got, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, validClaims("offline_access")))

		require.NoError(t, vErr)
		assert.Equal(t, domain.RoleUser, got.Role)
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		c := validClaims()
		c["exp"] = time.Now().Add(-time.Minute).Unix()

		_, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, c))

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("token without expiry is rejected", func(t *testing.T) {
		c := validClaims()
		delete(c, "exp")

		_, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, c))

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("wrong issuer is rejected", func(t *testing.T) {
		c := validClaims()
		c["iss"] = "http://evil/realms/vaca"

		_, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, c))

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("foreign signature is rejected", func(t *testing.T) {
		_, vErr := v.Verify(ctx, sign(t, other, jwt.SigningMethodRS256, validClaims()))

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("symmetric algorithm is rejected", func(t *testing.T) {
		tok, sErr := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims()).SignedString([]byte("secret"))
		require.NoError(t, sErr)

		_, vErr := v.Verify(ctx, tok)

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("token without subject is rejected", func(t *testing.T) {
		c := validClaims()
		delete(c, "sub")

		_, vErr := v.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, c))

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("garbage is rejected", func(t *testing.T) {
		_, vErr := v.Verify(ctx, "not-a-jwt")

		require.ErrorIs(t, vErr, ErrInvalidToken)
	})

	t.Run("an empty issuer disables the issuer check", func(t *testing.T) {
		lax := newVerifier(staticKey(&key.PublicKey), "")
		c := validClaims()
		c["iss"] = "anything"

		_, vErr := lax.Verify(ctx, sign(t, key, jwt.SigningMethodRS256, c))

		require.NoError(t, vErr)
	})
}
