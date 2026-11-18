//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// newTestPool starts a throwaway PostgreSQL, applies the migrations and returns a pool on it.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("users"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.WithoutCancel(ctx))) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, cleanup, err := NewPostgresDB(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return pool
}

func TestRepositories(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	profiles := NewProfileRepository(pool)
	prefs := NewPreferenceRepository(pool)
	library := NewLibraryRepository(pool)
	tg := NewTelegramRepository(pool)
	activity := NewActivityRepository(pool)

	ann := domain.Claims{Subject: "ann", Email: "ann@example.com", Username: "ann", Role: domain.RoleUser}
	bob := domain.Claims{Subject: "bob", Email: "bob@example.com", Username: "bob", Role: domain.RoleManager}

	t.Run("migrations are idempotent", func(t *testing.T) {
		require.NoError(t, Migrate(ctx, pool))
	})

	t.Run("upsert registers once and refreshes role and email", func(t *testing.T) {
		p, created, err := profiles.Upsert(ctx, ann)
		require.NoError(t, err)
		assert.True(t, created)
		assert.Equal(t, "ann", p.DisplayName)
		assert.Equal(t, domain.PlanDefault, p.Plan)
		assert.Equal(t, domain.StatusActive, p.Status)

		name := "Ann Custom"
		_, err = profiles.Update(ctx, "ann", domain.ProfileUpdate{DisplayName: &name})
		require.NoError(t, err)

		promoted := ann
		promoted.Role = domain.RoleAdmin
		promoted.Email = "new@example.com"
		p, created, err = profiles.Upsert(ctx, promoted)
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, domain.RoleAdmin, p.Role)
		assert.Equal(t, "new@example.com", p.Email)
		assert.Equal(t, "Ann Custom", p.DisplayName, "an edited display name survives a login")

		_, _, err = profiles.Upsert(ctx, ann) // back to a plain user
		require.NoError(t, err)
	})

	_, _, err := profiles.Upsert(ctx, bob)
	require.NoError(t, err)

	t.Run("partial profile update keeps untouched fields", func(t *testing.T) {
		stack := []string{"Go", "Docker"}
		salary := 3000
		p, err := profiles.Update(ctx, "bob", domain.ProfileUpdate{TechStack: &stack, ExpectedSalary: &salary})
		require.NoError(t, err)
		assert.Equal(t, stack, p.TechStack)
		assert.Equal(t, 3000, p.ExpectedSalary)

		loc := "Kyiv"
		p, err = profiles.Update(ctx, "bob", domain.ProfileUpdate{Location: &loc})
		require.NoError(t, err)
		assert.Equal(t, stack, p.TechStack)
		assert.Equal(t, "Kyiv", p.Location)

		empty := []string{}
		p, err = profiles.Update(ctx, "bob", domain.ProfileUpdate{TechStack: &empty})
		require.NoError(t, err)
		assert.Empty(t, p.TechStack)
	})

	t.Run("missing profile is ErrUserNotFound", func(t *testing.T) {
		_, err := profiles.Get(ctx, "ghost")
		require.ErrorIs(t, err, domain.ErrUserNotFound)
		require.ErrorIs(t, profiles.SetStatus(ctx, "ghost", domain.StatusBlocked), domain.ErrUserNotFound)
		require.ErrorIs(t, profiles.Delete(ctx, "ghost"), domain.ErrUserNotFound)
	})

	t.Run("list filters, searches and counts", func(t *testing.T) {
		all, total, err := profiles.List(ctx, domain.UserFilter{Page: domain.Page{Limit: 10}})
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		assert.Len(t, all, 2)

		managers, total, err := profiles.List(ctx,
			domain.UserFilter{Page: domain.Page{Limit: 10}, Role: domain.RoleManager})
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		assert.Equal(t, "bob", managers[0].ID)

		found, _, err := profiles.List(ctx, domain.UserFilter{Page: domain.Page{Limit: 10}, Search: "ANN@"})
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, "ann", found[0].ID)

		none, total, err := profiles.List(ctx, domain.UserFilter{Page: domain.Page{Limit: 10}, Search: "%"})
		require.NoError(t, err)
		assert.Empty(t, none, "LIKE wildcards in the search are escaped")
		assert.Zero(t, total)

		page, total, err := profiles.List(ctx, domain.UserFilter{Page: domain.Page{Limit: 1, Offset: 1}})
		require.NoError(t, err)
		assert.Len(t, page, 1)
		assert.EqualValues(t, 2, total, "total counts all matches, not the page")
	})

	t.Run("plan and status", func(t *testing.T) {
		until := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Microsecond)
		require.NoError(t, profiles.SetPlan(ctx, "ann", domain.PlanPro, &until))
		require.NoError(t, profiles.SetStatus(ctx, "ann", domain.StatusBlocked))

		p, err := profiles.Get(ctx, "ann")
		require.NoError(t, err)
		assert.Equal(t, domain.PlanPro, p.Plan)
		assert.True(t, until.Equal(*p.PlanExpiresAt))
		assert.Equal(t, domain.StatusBlocked, p.Status)

		blocked, total, err := profiles.List(ctx,
			domain.UserFilter{Page: domain.Page{Limit: 10}, Status: domain.StatusBlocked})
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		assert.Equal(t, "ann", blocked[0].ID)
	})

	t.Run("notification settings round trip", func(t *testing.T) {
		_, err := prefs.GetSettings(ctx, "bob")
		require.ErrorIs(t, err, domain.ErrNotFound)

		until := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
		want := domain.NotificationSettings{
			UserID: "bob", Frequency: domain.FrequencyDaily, Muted: true, MutedUntil: &until,
			Types: []domain.NotificationType{domain.NotifyBilling},
		}
		require.NoError(t, prefs.SaveSettings(ctx, want))
		want.Frequency = domain.FrequencyHourly
		require.NoError(t, prefs.SaveSettings(ctx, want), "saving again replaces the row")

		got, err := prefs.GetSettings(ctx, "bob")
		require.NoError(t, err)
		assert.Equal(t, domain.FrequencyHourly, got.Frequency)
		assert.Equal(t, want.Types, got.Types)
		assert.True(t, got.Muted)
		assert.True(t, until.Equal(*got.MutedUntil))
	})

	t.Run("subscriptions: create, count, update, delete and ownership", func(t *testing.T) {
		s1, err := prefs.CreateSubscription(ctx,
			domain.Subscription{UserID: "bob", Keyword: "golang", IsActive: true})
		require.NoError(t, err)
		_, err = prefs.CreateSubscription(ctx,
			domain.Subscription{UserID: "bob", Keyword: "rust", IsActive: false})
		require.NoError(t, err)

		n, err := prefs.CountActiveSubscriptions(ctx, "bob")
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		list, err := prefs.ListSubscriptions(ctx, "bob")
		require.NoError(t, err)
		assert.Len(t, list, 2)

		kw := "go"
		upd, err := prefs.UpdateSubscription(ctx, "bob", s1.ID, domain.SubscriptionUpdate{Keyword: &kw})
		require.NoError(t, err)
		assert.Equal(t, "go", upd.Keyword)
		assert.True(t, upd.IsActive, "untouched field is kept")

		_, err = prefs.UpdateSubscription(ctx, "ann", s1.ID, domain.SubscriptionUpdate{Keyword: &kw})
		require.ErrorIs(t, err, domain.ErrSubscriptionNotFound, "another user cannot edit it")
		require.ErrorIs(t, prefs.DeleteSubscription(ctx, "ann", s1.ID), domain.ErrSubscriptionNotFound)

		require.NoError(t, prefs.DeleteSubscription(ctx, "bob", s1.ID))
	})

	t.Run("saved list is idempotent, ordered and paged", func(t *testing.T) {
		require.NoError(t, library.Save(ctx, "bob", domain.VacancyRef{VacancyID: "v1", Title: "One"}))
		require.NoError(t, library.Save(ctx, "bob", domain.VacancyRef{VacancyID: "v1", Title: "One again"}))
		time.Sleep(5 * time.Millisecond)
		require.NoError(t, library.Save(ctx, "bob", domain.VacancyRef{VacancyID: "v2", Title: "Two"}))

		items, total, err := library.ListSaved(ctx, "bob", domain.Page{Limit: 1})
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		require.Len(t, items, 1)
		assert.Equal(t, "v2", items[0].VacancyID, "newest first")

		require.NoError(t, library.Unsave(ctx, "bob", "v1"))
		require.ErrorIs(t, library.Unsave(ctx, "bob", "v1"), domain.ErrNotFound)

		empty, total, err := library.ListSaved(ctx, "nobody", domain.Page{Limit: 10})
		require.NoError(t, err)
		assert.Empty(t, empty)
		assert.Zero(t, total)
	})

	t.Run("view history moves a repeated view to the top", func(t *testing.T) {
		require.NoError(t, library.AddView(ctx, "bob", domain.VacancyRef{VacancyID: "a"}))
		time.Sleep(5 * time.Millisecond)
		require.NoError(t, library.AddView(ctx, "bob", domain.VacancyRef{VacancyID: "b"}))
		time.Sleep(5 * time.Millisecond)
		require.NoError(t, library.AddView(ctx, "bob", domain.VacancyRef{VacancyID: "a", Title: "refreshed"}))

		items, total, err := library.ListViews(ctx, "bob", domain.Page{Limit: 10})
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		require.Len(t, items, 2)
		assert.Equal(t, "a", items[0].VacancyID)
		assert.Equal(t, "refreshed", items[0].Title)
	})

	t.Run("telegram code is single use, expiring and the latest wins", func(t *testing.T) {
		now := time.Now()
		hour := now.Add(time.Hour)
		require.NoError(t, tg.SaveCode(ctx, domain.TelegramCode{Code: "OLD", UserID: "bob", ExpiresAt: hour}))
		require.NoError(t, tg.SaveCode(ctx, domain.TelegramCode{Code: "NEW", UserID: "bob", ExpiresAt: hour}))

		_, err := tg.ConsumeCode(ctx, "OLD", now)
		require.ErrorIs(t, err, domain.ErrNotFound, "issuing a new code invalidates the old one")

		uid, err := tg.ConsumeCode(ctx, "NEW", now)
		require.NoError(t, err)
		assert.Equal(t, "bob", uid)
		_, err = tg.ConsumeCode(ctx, "NEW", now)
		require.ErrorIs(t, err, domain.ErrNotFound, "second use fails")

		past := now.Add(-time.Minute)
		require.NoError(t, tg.SaveCode(ctx, domain.TelegramCode{Code: "LATE", UserID: "bob", ExpiresAt: past}))
		_, err = tg.ConsumeCode(ctx, "LATE", now)
		require.ErrorIs(t, err, domain.ErrNotFound, "expired code fails")
	})

	t.Run("telegram link moves a chat between accounts and unlinks", func(t *testing.T) {
		require.NoError(t, tg.Link(ctx, "bob", 777))
		p, err := profiles.Get(ctx, "bob")
		require.NoError(t, err)
		assert.True(t, p.TelegramLinked)

		require.NoError(t, tg.Link(ctx, "ann", 777), "the same chat can move to another account")
		p, err = profiles.Get(ctx, "bob")
		require.NoError(t, err)
		assert.False(t, p.TelegramLinked)
		p, err = profiles.Get(ctx, "ann")
		require.NoError(t, err)
		assert.True(t, p.TelegramLinked)

		require.NoError(t, tg.Unlink(ctx, "ann"))
		p, err = profiles.Get(ctx, "ann")
		require.NoError(t, err)
		assert.False(t, p.TelegramLinked)
		require.ErrorIs(t, tg.Link(ctx, "ghost", 1), domain.ErrUserNotFound)
	})

	t.Run("activity trail survives account deletion", func(t *testing.T) {
		require.NoError(t, activity.Add(ctx, domain.Activity{
			UserID: "ann", ActorID: "bob", Action: domain.ActionBlocked, Meta: map[string]any{"reason": "spam"},
		}))
		time.Sleep(5 * time.Millisecond)
		require.NoError(t, activity.Add(ctx, domain.Activity{
			UserID: "ann", ActorID: "ann", Action: domain.ActionProfileUpdated,
		}))

		require.NoError(t, profiles.Delete(ctx, "ann"))

		items, total, err := activity.List(ctx, "ann", domain.Page{Limit: 10})
		require.NoError(t, err)
		assert.EqualValues(t, 2, total)
		require.Len(t, items, 2)
		assert.Equal(t, domain.ActionProfileUpdated, items[0].Action, "newest first")
		assert.Equal(t, "spam", items[1].Meta["reason"])
	})

	t.Run("deleting a profile cascades to everything it owns", func(t *testing.T) {
		require.NoError(t, profiles.Delete(ctx, "bob"))

		for _, table := range []string{
			"user_notification_settings", "user_subscriptions", "user_saved_vacancies", "user_vacancy_views",
		} {
			var n int
			require.NoError(t, pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n))
			assert.Zero(t, n, table)
		}
	})
}
