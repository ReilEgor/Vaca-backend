package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// TelegramRepository stores verification codes and the chat bound to an account.
type TelegramRepository struct {
	db *pgxpool.Pool
}

// SaveCode stores a code; an older unused code of the same user is dropped so only the latest one works.
func (r *TelegramRepository) SaveCode(ctx context.Context, c domain.TelegramCode) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer rollback(ctx, tx)

	if _, err = tx.Exec(ctx, `DELETE FROM user_telegram_codes WHERE user_id = $1`, c.UserID); err != nil {
		return fmt.Errorf("drop old codes: %w", err)
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO user_telegram_codes (code, user_id, expires_at) VALUES ($1, $2, $3)`,
		c.Code, c.UserID, c.ExpiresAt); err != nil {
		return fmt.Errorf("insert code: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ConsumeCode deletes a non-expired code and returns its owner; a code can be used only once.
func (r *TelegramRepository) ConsumeCode(ctx context.Context, code string, now time.Time) (string, error) {
	var userID string
	err := r.db.QueryRow(ctx,
		`DELETE FROM user_telegram_codes WHERE code = $1 AND expires_at > $2 RETURNING user_id`, code, now,
	).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("consume code: %w", mapErr(err, domain.ErrNotFound))
	}
	return userID, nil
}

// Link binds a chat to the account; a chat that is bound to another account is moved to this one.
func (r *TelegramRepository) Link(ctx context.Context, userID string, chatID int64) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer rollback(ctx, tx)

	if _, err = tx.Exec(ctx,
		`UPDATE user_profiles SET telegram_chat_id = NULL WHERE telegram_chat_id = $1 AND id <> $2`,
		chatID, userID); err != nil {
		return fmt.Errorf("release chat: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE user_profiles SET telegram_chat_id = $2 WHERE id = $1`, userID, chatID)
	if err != nil {
		return fmt.Errorf("bind chat: %w", err)
	}
	if err = ensureAffected(tag, domain.ErrUserNotFound); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Unlink detaches the chat from the account.
func (r *TelegramRepository) Unlink(ctx context.Context, userID string) error {
	tag, err := r.db.Exec(ctx, `UPDATE user_profiles SET telegram_chat_id = NULL WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("unlink: %w", err)
	}
	return ensureAffected(tag, domain.ErrUserNotFound)
}

// ActivityRepository stores the audit trail in user_activity.
type ActivityRepository struct {
	db *pgxpool.Pool
}

// Add appends a record.
func (r *ActivityRepository) Add(ctx context.Context, a domain.Activity) error {
	var meta []byte
	if len(a.Meta) > 0 {
		var err error
		if meta, err = json.Marshal(a.Meta); err != nil {
			return fmt.Errorf("marshal meta: %w", err)
		}
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO user_activity (user_id, actor_id, action, meta) VALUES ($1, $2, $3, $4)`,
		a.UserID, a.ActorID, a.Action, meta)
	if err != nil {
		return fmt.Errorf("add activity: %w", err)
	}
	return nil
}

const listActivityQuery = `
SELECT id, user_id, actor_id, action, meta, created_at, COUNT(*) OVER () AS total
FROM user_activity WHERE user_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3`

// List returns a page of the trail of a user, newest first, and its total size.
func (r *ActivityRepository) List(ctx context.Context, userID string, p domain.Page) ([]domain.Activity, int64, error) {
	rows, err := r.db.Query(ctx, listActivityQuery, userID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	out := []domain.Activity{}
	var total int64
	for rows.Next() {
		var (
			a    domain.Activity
			meta []byte
		)
		if err = rows.Scan(&a.ID, &a.UserID, &a.ActorID, &a.Action, &meta, &a.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan activity: %w", err)
		}
		if len(meta) > 0 {
			if err = json.Unmarshal(meta, &a.Meta); err != nil {
				return nil, 0, fmt.Errorf("unmarshal meta: %w", err)
			}
		}
		out = append(out, a)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read activity: %w", err)
	}
	return out, total, nil
}
