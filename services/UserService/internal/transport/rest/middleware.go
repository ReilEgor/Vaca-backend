package rest

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ReilEgor/Vaca/services/UserService/internal/auth"
	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

const (
	claimsKey  = "claims"
	profileKey = "profile"
)

// requestLogger writes one structured line per request.
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.FullPath()),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
		)
	}
}

// authenticate verifies the bearer token, registers the profile on the first call and
// rejects blocked accounts. On success the claims and the profile are put into the context.
func authenticate(verifier auth.Verifier, uc domain.UserUsecase) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			abort(c, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := verifier.Verify(c.Request.Context(), strings.TrimSpace(token))
		if err != nil {
			abort(c, http.StatusUnauthorized, "invalid token")
			return
		}
		profile, err := uc.Authenticate(c.Request.Context(), claims)
		switch {
		case errors.Is(err, domain.ErrAccountBlocked):
			abort(c, http.StatusForbidden, "account is blocked")
			return
		case err != nil:
			slog.Error("authenticate failed", slog.Any("error", err))
			abort(c, http.StatusInternalServerError, "internal error")
			return
		}
		c.Set(claimsKey, claims)
		c.Set(profileKey, profile)
		c.Next()
	}
}

// requireRole lets through only accounts of at least the given role.
func requireRole(minRole domain.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		if claimsOf(c).Role.Rank() < minRole.Rank() {
			abort(c, http.StatusForbidden, "insufficient permissions")
			return
		}
		c.Next()
	}
}

// requireInternalToken protects service-to-service routes with a shared secret.
// An empty secret closes the routes completely instead of leaving them open.
func requireInternalToken(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		got := c.GetHeader("X-Internal-Token")
		if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			abort(c, http.StatusUnauthorized, "invalid internal token")
			return
		}
		c.Next()
	}
}

func claimsOf(c *gin.Context) domain.Claims {
	v, _ := c.Get(claimsKey)
	claims, ok := v.(domain.Claims)
	if !ok {
		return domain.Claims{}
	}
	return claims
}

func profileOf(c *gin.Context) *domain.Profile {
	v, _ := c.Get(profileKey)
	p, ok := v.(*domain.Profile)
	if !ok {
		return nil
	}
	return p
}

type errorBody struct {
	Error string `json:"error"`
}

func abort(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, errorBody{Error: msg})
}
