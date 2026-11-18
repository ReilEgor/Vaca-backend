package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// ProfileRepository stores accounts in user_profiles.
type ProfileRepository struct {
	db *pgxpool.Pool
}

const profileColumns = `id, email, display_name, role, status, plan, plan_expires_at, tech_stack, experience,
	location, expected_salary, telegram_chat_id IS NOT NULL, created_at, last_seen_at`

// The display name falls back to the Keycloak username on the first insert only, so a later edit is kept.
const upsertProfileQuery = `
INSERT INTO user_profiles (id, email, display_name, role)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET email = CASE WHEN EXCLUDED.email <> '' THEN EXCLUDED.email ELSE user_profiles.email END,
    role = EXCLUDED.role,
    last_seen_at = NOW()
RETURNING ` + profileColumns + `, (xmax = 0) AS inserted`

type scanner interface{ Scan(dest ...any) error }

func scanProfile(row scanner, extra ...any) (*domain.Profile, error) {
	var p domain.Profile
	dest := []any{
		&p.ID, &p.Email, &p.DisplayName, &p.Role, &p.Status, &p.Plan, &p.PlanExpiresAt, &p.TechStack,
		&p.Experience, &p.Location, &p.ExpectedSalary, &p.TelegramLinked, &p.CreatedAt, &p.LastSeenAt,
	}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, err //nolint:wrapcheck // callers add the context and map pgx.ErrNoRows.
	}
	return &p, nil
}

// Upsert registers the profile or refreshes e-mail, role and the last-seen time.
func (r *ProfileRepository) Upsert(ctx context.Context, c domain.Claims) (*domain.Profile, bool, error) {
	role := c.Role
	if role == "" {
		role = domain.RoleUser
	}
	var inserted bool
	p, err := scanProfile(r.db.QueryRow(ctx, upsertProfileQuery, c.Subject, c.Email, c.Username, role), &inserted)
	if err != nil {
		return nil, false, fmt.Errorf("upsert profile: %w", err)
	}
	return p, inserted, nil
}

// Get returns one account.
func (r *ProfileRepository) Get(ctx context.Context, id string) (*domain.Profile, error) {
	p, err := scanProfile(r.db.QueryRow(ctx, `SELECT `+profileColumns+` FROM user_profiles WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", mapErr(err, domain.ErrUserNotFound))
	}
	return p, nil
}

const updateProfileQuery = `
UPDATE user_profiles SET
    display_name    = COALESCE($2, display_name),
    tech_stack      = COALESCE($3, tech_stack),
    experience      = COALESCE($4, experience),
    location        = COALESCE($5, location),
    expected_salary = COALESCE($6, expected_salary)
WHERE id = $1
RETURNING ` + profileColumns

// Update applies a partial update; nil fields stay unchanged.
func (r *ProfileRepository) Update(
	ctx context.Context, id string, upd domain.ProfileUpdate,
) (*domain.Profile, error) {
	var stack []string
	if upd.TechStack != nil {
		stack = *upd.TechStack
		if stack == nil {
			stack = []string{}
		}
	}
	p, err := scanProfile(r.db.QueryRow(ctx, updateProfileQuery,
		id, upd.DisplayName, stack, upd.Experience, upd.Location, upd.ExpectedSalary))
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", mapErr(err, domain.ErrUserNotFound))
	}
	return p, nil
}

const listProfilesQuery = `
SELECT ` + profileColumns + `, COUNT(*) OVER () AS total
FROM user_profiles
WHERE ($1 = '' OR LOWER(email) LIKE $1 OR LOWER(display_name) LIKE $1)
  AND ($2 = '' OR role = $2)
  AND ($3 = '' OR status = $3)
ORDER BY created_at DESC, id
LIMIT $4 OFFSET $5`

// List returns a page of accounts matching the filter and the total number of matches.
func (r *ProfileRepository) List(ctx context.Context, f domain.UserFilter) ([]domain.Profile, int64, error) {
	search := ""
	if s := strings.ToLower(strings.TrimSpace(f.Search)); s != "" {
		search = "%" + escapeLike(s) + "%"
	}
	rows, err := r.db.Query(ctx, listProfilesQuery, search, string(f.Role), string(f.Status), f.Limit, f.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	var (
		out   []domain.Profile
		total int64
	)
	for rows.Next() {
		p, sErr := scanProfile(rows, &total)
		if sErr != nil {
			return nil, 0, fmt.Errorf("scan profile: %w", sErr)
		}
		out = append(out, *p)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read profiles: %w", err)
	}
	return out, total, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// SetPlan changes the tariff plan and its expiry.
func (r *ProfileRepository) SetPlan(ctx context.Context, id string, plan domain.Plan, expiresAt *time.Time) error {
	tag, err := r.db.Exec(ctx, `UPDATE user_profiles SET plan = $2, plan_expires_at = $3 WHERE id = $1`,
		id, plan, expiresAt)
	if err != nil {
		return fmt.Errorf("set plan: %w", err)
	}
	return ensureAffected(tag, domain.ErrUserNotFound)
}

// SetStatus blocks or unblocks an account.
func (r *ProfileRepository) SetStatus(ctx context.Context, id string, status domain.Status) error {
	tag, err := r.db.Exec(ctx, `UPDATE user_profiles SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	return ensureAffected(tag, domain.ErrUserNotFound)
}

// Delete removes the account; settings, subscriptions, lists and codes go with it by cascade.
func (r *ProfileRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM user_profiles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	return ensureAffected(tag, domain.ErrUserNotFound)
}
