// Package auth verifies the JWT access tokens issued by Keycloak.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// ErrInvalidToken is returned for any token that cannot be trusted.
var ErrInvalidToken = errors.New("invalid token")

// Verifier turns a bearer token into verified claims.
//
//go:generate mockery --name Verifier --output ../mocks/auth --outpkg auth --case=underscore
type Verifier interface {
	Verify(ctx context.Context, token string) (domain.Claims, error)
}

// KeycloakVerifier checks the signature against the JWKS of the realm and reads realm roles.
type KeycloakVerifier struct {
	keyFn  jwt.Keyfunc
	parser *jwt.Parser
}

// NewKeycloakVerifier starts a background refresh of the realm keys.
// An empty issuer disables the "iss" check, which is only meant for local development.
func NewKeycloakVerifier(ctx context.Context, jwksURL, issuer string) (*KeycloakVerifier, error) {
	keys, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("load jwks: %w", err)
	}
	return newVerifier(keys.Keyfunc, issuer), nil
}

func newVerifier(keyFn jwt.Keyfunc, issuer string) *KeycloakVerifier {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512"}),
		jwt.WithExpirationRequired(),
	}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	return &KeycloakVerifier{keyFn: keyFn, parser: jwt.NewParser(opts...)}
}

type keycloakClaims struct {
	jwt.RegisteredClaims

	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	RealmAccess       struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Verify checks signature, expiry and issuer, and maps the token to domain claims.
func (v *KeycloakVerifier) Verify(_ context.Context, token string) (domain.Claims, error) {
	var kc keycloakClaims
	if _, err := v.parser.ParseWithClaims(token, &kc, v.keyFn); err != nil {
		return domain.Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if kc.Subject == "" {
		return domain.Claims{}, fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	return domain.Claims{
		Subject:  kc.Subject,
		Email:    kc.Email,
		Username: kc.PreferredUsername,
		Role:     highestRole(kc.RealmAccess.Roles),
	}, nil
}

// highestRole picks the most powerful known role; a token without one is an ordinary user.
func highestRole(roles []string) domain.Role {
	best := domain.RoleUser
	for _, r := range roles {
		role := domain.Role(strings.ToLower(r))
		if role.Rank() > best.Rank() {
			best = role
		}
	}
	return best
}
