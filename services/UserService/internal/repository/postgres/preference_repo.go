package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// PreferenceRepository stores notification settings and subscriptions.
type PreferenceRepository struct {
	db *pgxpool.Pool
}

// GetSettings returns the saved settings or domain.ErrNotFound when there are none.
func (r *PreferenceRepository) GetSettings(ctx context.Context, userID string) (*domain.NotificationSettings, error) {
	s := domain.NotificationSettings{UserID: userID}
	var types []string
	err := r.db.QueryRow(ctx,
		`SELECT frequency, types, muted, muted_until FROM user_notification_settings WHERE user_id = $1`, userID,
	).Scan(&s.Frequency, &types, &s.Muted, &s.MutedUntil)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", mapErr(err, domain.ErrNotFound))
	}
	s.Types = make([]domain.NotificationType, len(types))
	for i, t := range types {
		s.Types[i] = domain.NotificationType(t)
	}
	return &s, nil
}

const saveSettingsQuery = `
INSERT INTO user_notification_settings (user_id, frequency, types, muted, muted_until)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE
SET frequency = EXCLUDED.frequency, types = EXCLUDED.types, muted = EXCLUDED.muted, muted_until = EXCLUDED.muted_until`

// SaveSettings inserts or replaces the settings of a user.
func (r *PreferenceRepository) SaveSettings(ctx context.Context, s domain.NotificationSettings) error {
	types := make([]string, len(s.Types))
	for i, t := range s.Types {
		types[i] = string(t)
	}
	if _, err := r.db.Exec(ctx, saveSettingsQuery, s.UserID, s.Frequency, types, s.Muted, s.MutedUntil); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

const subscriptionColumns = `id, user_id, keyword, location, min_salary, is_active, created_at`

func scanSubscription(row scanner) (*domain.Subscription, error) {
	var s domain.Subscription
	if err := row.Scan(&s.ID, &s.UserID, &s.Keyword, &s.Location, &s.MinSalary, &s.IsActive, &s.CreatedAt); err != nil {
		return nil, err //nolint:wrapcheck // callers add the context.
	}
	return &s, nil
}

// ListSubscriptions returns the subscriptions of a user, newest first.
func (r *PreferenceRepository) ListSubscriptions(ctx context.Context, userID string) ([]domain.Subscription, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+subscriptionColumns+` FROM user_subscriptions WHERE user_id = $1 ORDER BY created_at DESC, id DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()

	out := []domain.Subscription{}
	for rows.Next() {
		s, sErr := scanSubscription(rows)
		if sErr != nil {
			return nil, fmt.Errorf("scan subscription: %w", sErr)
		}
		out = append(out, *s)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read subscriptions: %w", err)
	}
	return out, nil
}

// CountActiveSubscriptions returns how many active subscriptions the user has.
func (r *PreferenceRepository) CountActiveSubscriptions(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM user_subscriptions WHERE user_id = $1 AND is_active`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions: %w", err)
	}
	return n, nil
}

// CreateSubscription inserts a subscription.
func (r *PreferenceRepository) CreateSubscription(
	ctx context.Context, s domain.Subscription,
) (*domain.Subscription, error) {
	created, err := scanSubscription(r.db.QueryRow(ctx,
		`INSERT INTO user_subscriptions (user_id, keyword, location, min_salary, is_active)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+subscriptionColumns,
		s.UserID, s.Keyword, s.Location, s.MinSalary, s.IsActive))
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	return created, nil
}

const updateSubscriptionQuery = `
UPDATE user_subscriptions SET
    keyword    = COALESCE($3, keyword),
    location   = COALESCE($4, location),
    min_salary = COALESCE($5, min_salary),
    is_active  = COALESCE($6, is_active)
WHERE id = $1 AND user_id = $2
RETURNING ` + subscriptionColumns

// UpdateSubscription applies a partial update to a subscription owned by the user.
func (r *PreferenceRepository) UpdateSubscription(
	ctx context.Context, userID string, id int64, upd domain.SubscriptionUpdate,
) (*domain.Subscription, error) {
	s, err := scanSubscription(r.db.QueryRow(ctx, updateSubscriptionQuery,
		id, userID, upd.Keyword, upd.Location, upd.MinSalary, upd.IsActive))
	if err != nil {
		return nil, fmt.Errorf("update subscription: %w", mapErr(err, domain.ErrSubscriptionNotFound))
	}
	return s, nil
}

// DeleteSubscription removes a subscription owned by the user.
func (r *PreferenceRepository) DeleteSubscription(ctx context.Context, userID string, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM user_subscriptions WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	return ensureAffected(tag, domain.ErrSubscriptionNotFound)
}
