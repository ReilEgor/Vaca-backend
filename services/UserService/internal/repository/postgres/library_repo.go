package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// LibraryRepository stores the saved list and the view history.
type LibraryRepository struct {
	db *pgxpool.Pool
}

const saveVacancyQuery = `
INSERT INTO user_saved_vacancies (user_id, vacancy_id, title, company, location, salary, link)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, vacancy_id) DO NOTHING`

// Save adds a vacancy to the saved list; a vacancy that is already saved is left as it is.
func (r *LibraryRepository) Save(ctx context.Context, userID string, v domain.VacancyRef) error {
	_, err := r.db.Exec(ctx, saveVacancyQuery,
		userID, v.VacancyID, v.Title, v.Company, v.Location, v.Salary, v.Link)
	if err != nil {
		return fmt.Errorf("save vacancy: %w", err)
	}
	return nil
}

// Unsave removes a vacancy from the saved list.
func (r *LibraryRepository) Unsave(ctx context.Context, userID, vacancyID string) error {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM user_saved_vacancies WHERE user_id = $1 AND vacancy_id = $2`, userID, vacancyID)
	if err != nil {
		return fmt.Errorf("unsave vacancy: %w", err)
	}
	return ensureAffected(tag, domain.ErrNotFound)
}

const listSavedQuery = `
SELECT vacancy_id, title, company, location, salary, link, saved_at, COUNT(*) OVER () AS total
FROM user_saved_vacancies WHERE user_id = $1
ORDER BY saved_at DESC, vacancy_id
LIMIT $2 OFFSET $3`

// ListSaved returns a page of the saved list, newest first, and the total number of saved vacancies.
func (r *LibraryRepository) ListSaved(
	ctx context.Context, userID string, p domain.Page,
) ([]domain.SavedVacancy, int64, error) {
	rows, err := r.db.Query(ctx, listSavedQuery, userID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list saved: %w", err)
	}
	defer rows.Close()

	out := []domain.SavedVacancy{}
	var total int64
	for rows.Next() {
		var s domain.SavedVacancy
		if err = rows.Scan(&s.VacancyID, &s.Title, &s.Company, &s.Location, &s.Salary, &s.Link,
			&s.SavedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan saved: %w", err)
		}
		out = append(out, s)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read saved: %w", err)
	}
	return out, total, nil
}

const addViewQuery = `
INSERT INTO user_vacancy_views (user_id, vacancy_id, title, company, location, salary, link)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, vacancy_id) DO UPDATE
SET viewed_at = NOW(), title = EXCLUDED.title, company = EXCLUDED.company,
    location = EXCLUDED.location, salary = EXCLUDED.salary, link = EXCLUDED.link`

// AddView puts a vacancy at the top of the view history; a repeated view only refreshes its time.
func (r *LibraryRepository) AddView(ctx context.Context, userID string, v domain.VacancyRef) error {
	_, err := r.db.Exec(ctx, addViewQuery,
		userID, v.VacancyID, v.Title, v.Company, v.Location, v.Salary, v.Link)
	if err != nil {
		return fmt.Errorf("add view: %w", err)
	}
	return nil
}

const listViewsQuery = `
SELECT vacancy_id, title, company, location, salary, link, viewed_at, COUNT(*) OVER () AS total
FROM user_vacancy_views WHERE user_id = $1
ORDER BY viewed_at DESC, vacancy_id
LIMIT $2 OFFSET $3`

// ListViews returns a page of the view history, newest first, and its total size.
func (r *LibraryRepository) ListViews(
	ctx context.Context, userID string, p domain.Page,
) ([]domain.ViewedVacancy, int64, error) {
	rows, err := r.db.Query(ctx, listViewsQuery, userID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list views: %w", err)
	}
	defer rows.Close()

	out := []domain.ViewedVacancy{}
	var total int64
	for rows.Next() {
		var v domain.ViewedVacancy
		if err = rows.Scan(&v.VacancyID, &v.Title, &v.Company, &v.Location, &v.Salary, &v.Link,
			&v.ViewedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan view: %w", err)
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read views: %w", err)
	}
	return out, total, nil
}
