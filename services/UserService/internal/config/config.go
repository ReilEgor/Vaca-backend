// Package config reads the UserService settings from the environment.
package config

import "time"

// Config is the process configuration.
type Config struct {
	HTTPPort string `env:"HTTP_PORT"      envDefault:"8080"`
	DSN      string `env:"USER_DB_SOURCE" envDefault:"postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"`

	// KeycloakJWKSURL is where the signing keys of the realm are published.
	KeycloakJWKSURL string `env:"KEYCLOAK_JWKS_URL" envDefault:"http://keycloak:8080/realms/vaca_realm/protocol/openid-connect/certs"`
	// KeycloakIssuer is the expected "iss" claim; an empty value disables the check (local development only).
	KeycloakIssuer string `env:"KEYCLOAK_ISSUER"`

	// InternalToken protects /internal routes called by other services (the Telegram bot).
	InternalToken string `env:"INTERNAL_TOKEN"`

	DefaultPlanSubscriptions int           `env:"DEFAULT_PLAN_SUBSCRIPTIONS" envDefault:"3"`
	ProPlanSubscriptions     int           `env:"PRO_PLAN_SUBSCRIPTIONS"     envDefault:"20"`
	TelegramCodeTTL          time.Duration `env:"TELEGRAM_CODE_TTL"          envDefault:"10m"`
}
