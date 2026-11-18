// Package postgres implements the repositories of the UserService on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxConns        = 20
	maxConnIdleTime = 30 * time.Second
	healthCheck     = time.Minute
)

// NewPostgresDB opens the pool, applies the migrations and returns the pool with its cleanup function.
func NewPostgresDB(ctx context.Context, dsn string) (*pgxpool.Pool, func(), error) {
	slog.Info("connecting to database", slog.String("dsn", maskDSN(dsn)))
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.HealthCheckPeriod = healthCheck

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open db: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("ping db: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return pool, pool.Close, nil
}

func maskDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "invalid-dsn"
	}
	return u.Redacted()
}

// NewProfileRepository builds the account repository.
func NewProfileRepository(db *pgxpool.Pool) *ProfileRepository { return &ProfileRepository{db: db} }

// NewPreferenceRepository builds the settings and subscriptions repository.
func NewPreferenceRepository(db *pgxpool.Pool) *PreferenceRepository {
	return &PreferenceRepository{db: db}
}

// NewLibraryRepository builds the saved list and view history repository.
func NewLibraryRepository(db *pgxpool.Pool) *LibraryRepository { return &LibraryRepository{db: db} }

// NewTelegramRepository builds the Telegram binding repository.
func NewTelegramRepository(db *pgxpool.Pool) *TelegramRepository { return &TelegramRepository{db: db} }

// NewActivityRepository builds the audit repository.
func NewActivityRepository(db *pgxpool.Pool) *ActivityRepository { return &ActivityRepository{db: db} }

// mapErr converts driver errors to domain errors.
func mapErr(err, notFound error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound
	}
	return err
}

// rollback aborts a transaction that was not committed; "already closed" is the normal case after a commit.
func rollback(ctx context.Context, tx pgx.Tx) {
	if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("transaction rollback failed", slog.Any("error", err))
	}
}

// ensureAffected turns "no row changed" into the given not-found error.
func ensureAffected(tag pgconn.CommandTag, notFound error) error {
	if tag.RowsAffected() == 0 {
		return notFound
	}
	return nil
}
