package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

const (
	migrationsTable = "user_service_schema_migrations"
	// advisoryLockKey serializes concurrent starts of several replicas.
	advisoryLockKey = 7_245_001
)

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies every embedded *.up.sql that has not been applied yet, in version order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer func() {
		if _, uErr := conn.Exec(
			context.WithoutCancel(ctx),
			"SELECT pg_advisory_unlock($1)",
			advisoryLockKey,
		); uErr != nil {
			slog.Warn("failed to release migration lock", slog.Any("error", uErr))
		}
	}()

	const ddl = `CREATE TABLE IF NOT EXISTS ` + migrationsTable + ` (
		version    INT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`
	if _, err = conn.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, m := range migs {
		var done bool
		err = conn.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM "+migrationsTable+" WHERE version = $1)", m.version).Scan(&done)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", m.name, err)
		}
		if done {
			continue
		}
		if err = applyMigration(ctx, pool, m); err != nil {
			return err
		}
		slog.Info("migration applied", slog.String("name", m.name))
	}
	return nil
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer rollback(ctx, tx)

	if _, err = tx.Exec(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+migrationsTable+" (version) VALUES ($1)", m.version); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}

func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	migs := make([]migration, 0, len(entries))
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no version prefix", e.Name())
		}
		version, convErr := strconv.Atoi(prefix)
		if convErr != nil {
			return nil, fmt.Errorf("migration %q has a bad version: %w", e.Name(), convErr)
		}
		body, rErr := fs.ReadFile(fsys, "migrations/"+e.Name())
		if rErr != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), rErr)
		}
		migs = append(migs, migration{version: version, name: e.Name(), sql: string(body)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	return migs, nil
}
