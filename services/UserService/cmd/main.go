// Command user-service runs the Vaca UserService: profiles, saved vacancies, subscriptions and account management.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/caarlos0/env/v11"

	"github.com/ReilEgor/Vaca/services/UserService/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var cfg config.Config
	if err := env.Parse(&cfg); err != nil {
		logger.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, cleanup, err := InitializeApp(ctx, &cfg)
	if err != nil {
		logger.Error("failed to initialize app", slog.Any("error", err))
		stop()
		os.Exit(1)
	}
	defer cleanup()

	logger.Info("user-service is starting", slog.String("port", cfg.HTTPPort))
	if err := app.Server.Run(ctx, cfg.HTTPPort); err != nil {
		logger.Error("server stopped with error", slog.Any("error", err))
		cleanup()
		os.Exit(1)
	}
	logger.Info("shut down gracefully")
}
