//go:build wireinject

package main

import (
	"context"
	"fmt"

	"github.com/google/wire"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ReilEgor/Vaca/services/UserService/internal/auth"
	"github.com/ReilEgor/Vaca/services/UserService/internal/config"
	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
	"github.com/ReilEgor/Vaca/services/UserService/internal/repository/postgres"
	"github.com/ReilEgor/Vaca/services/UserService/internal/transport/rest"
	"github.com/ReilEgor/Vaca/services/UserService/internal/usecase"
)

// App is everything main needs to run the service.
type App struct {
	Server *rest.Server
}

func providePool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, func(), error) {
	return postgres.NewPostgresDB(ctx, cfg.DSN)
}

func provideVerifier(ctx context.Context, cfg *config.Config) (auth.Verifier, error) {
	v, err := auth.NewKeycloakVerifier(ctx, cfg.KeycloakJWKSURL, cfg.KeycloakIssuer)
	if err != nil {
		return nil, fmt.Errorf("keycloak verifier: %w", err)
	}
	return v, nil
}

func provideLimits(cfg *config.Config) usecase.Limits {
	return usecase.Limits{
		DefaultPlanSubscriptions: cfg.DefaultPlanSubscriptions,
		ProPlanSubscriptions:     cfg.ProPlanSubscriptions,
		TelegramCodeTTL:          cfg.TelegramCodeTTL,
	}
}

func provideServer(uc domain.UserUsecase, v auth.Verifier, cfg *config.Config) *rest.Server {
	return rest.NewServer(uc, v, cfg.InternalToken)
}

var repositorySet = wire.NewSet(
	providePool,
	postgres.NewProfileRepository,
	postgres.NewPreferenceRepository,
	postgres.NewLibraryRepository,
	postgres.NewTelegramRepository,
	postgres.NewActivityRepository,
	wire.Bind(new(domain.ProfileRepository), new(*postgres.ProfileRepository)),
	wire.Bind(new(domain.PreferenceRepository), new(*postgres.PreferenceRepository)),
	wire.Bind(new(domain.LibraryRepository), new(*postgres.LibraryRepository)),
	wire.Bind(new(domain.TelegramRepository), new(*postgres.TelegramRepository)),
	wire.Bind(new(domain.ActivityRepository), new(*postgres.ActivityRepository)),
)

var usecaseSet = wire.NewSet(
	provideLimits,
	wire.Struct(new(usecase.Deps), "*"),
	usecase.NewUserInteractor,
	wire.Bind(new(domain.UserUsecase), new(*usecase.UserInteractor)),
)

// InitializeApp builds the service graph.
func InitializeApp(ctx context.Context, cfg *config.Config) (*App, func(), error) {
	wire.Build(
		repositorySet,
		usecaseSet,
		provideVerifier,
		provideServer,
		wire.Struct(new(App), "*"),
	)
	return nil, nil, nil
}
