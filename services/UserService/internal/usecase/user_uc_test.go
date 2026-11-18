package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
	mocks "github.com/ReilEgor/Vaca/services/UserService/internal/mocks/domain"
)

var fixedNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	uc          *UserInteractor
	profiles    *mocks.ProfileRepository
	preferences *mocks.PreferenceRepository
	library     *mocks.LibraryRepository
	telegram    *mocks.TelegramRepository
	activity    *mocks.ActivityRepository
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		profiles:    mocks.NewProfileRepository(t),
		preferences: mocks.NewPreferenceRepository(t),
		library:     mocks.NewLibraryRepository(t),
		telegram:    mocks.NewTelegramRepository(t),
		activity:    mocks.NewActivityRepository(t),
	}
	f.uc = NewUserInteractor(Deps{
		Profiles:    f.profiles,
		Preferences: f.preferences,
		Library:     f.library,
		Telegram:    f.telegram,
		Activity:    f.activity,
	}, Limits{DefaultPlanSubscriptions: 2, ProPlanSubscriptions: 5, TelegramCodeTTL: 10 * time.Minute})
	f.uc.now = func() time.Time { return fixedNow }
	return f
}

// allowAudit accepts any number of activity records.
func (f *fixture) allowAudit() {
	f.activity.On("Add", mock.Anything, mock.Anything).Return(nil).Maybe()
}

func TestAuthenticate(t *testing.T) {
	ctx := context.Background()
	claims := domain.Claims{Subject: "u1", Email: "a@b.c", Role: domain.RoleUser}

	t.Run("first request creates the profile and audits it", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Upsert", ctx, claims).
			Return(&domain.Profile{ID: "u1", Status: domain.StatusActive, Plan: domain.PlanDefault}, true, nil)
		f.activity.On("Add", ctx, mock.MatchedBy(func(a domain.Activity) bool {
			return a.Action == domain.ActionProfileCreated && a.UserID == "u1"
		})).Return(nil)

		p, err := f.uc.Authenticate(ctx, claims)

		require.NoError(t, err)
		assert.Equal(t, "u1", p.ID)
	})

	t.Run("known profile is not audited again", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Upsert", ctx, claims).
			Return(&domain.Profile{ID: "u1", Status: domain.StatusActive}, false, nil)

		_, err := f.uc.Authenticate(ctx, claims)

		require.NoError(t, err)
	})

	t.Run("blocked account is refused", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Upsert", ctx, claims).
			Return(&domain.Profile{ID: "u1", Status: domain.StatusBlocked}, false, nil)

		_, err := f.uc.Authenticate(ctx, claims)

		require.ErrorIs(t, err, domain.ErrAccountBlocked)
	})

	t.Run("token without subject is invalid", func(t *testing.T) {
		f := newFixture(t)

		_, err := f.uc.Authenticate(ctx, domain.Claims{})

		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("expired pro plan is shown as default", func(t *testing.T) {
		f := newFixture(t)
		past := fixedNow.Add(-time.Hour)
		f.profiles.On("Upsert", ctx, claims).Return(&domain.Profile{
			ID: "u1", Status: domain.StatusActive, Plan: domain.PlanPro, PlanExpiresAt: &past,
		}, false, nil)

		p, err := f.uc.Authenticate(ctx, claims)

		require.NoError(t, err)
		assert.Equal(t, domain.PlanDefault, p.Plan)
		assert.Nil(t, p.PlanExpiresAt)
	})

	t.Run("repository failure is returned", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Upsert", ctx, claims).Return(nil, false, errors.New("db down"))

		_, err := f.uc.Authenticate(ctx, claims)

		require.Error(t, err)
	})
}

func TestUpdateProfile_Validation(t *testing.T) {
	longName := strings.Repeat("x", maxDisplayName+1)
	negative := -1
	tooMany := make([]string, maxStackItems+1)
	for i := range tooMany {
		tooMany[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}

	tests := []struct {
		name string
		upd  domain.ProfileUpdate
	}{
		{"display name too long", domain.ProfileUpdate{DisplayName: &longName}},
		{"negative salary", domain.ProfileUpdate{ExpectedSalary: &negative}},
		{"too many technologies", domain.ProfileUpdate{TechStack: &tooMany}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			_, err := f.uc.UpdateProfile(context.Background(), "u1", tt.upd)

			require.ErrorIs(t, err, domain.ErrInvalidInput)
		})
	}
}

func TestUpdateProfile_NormalizesStack(t *testing.T) {
	f := newFixture(t)
	f.allowAudit()
	stack := []string{" Go ", "go", "", "Docker"}
	f.profiles.On("Update", mock.Anything, "u1", mock.MatchedBy(func(u domain.ProfileUpdate) bool {
		return u.TechStack != nil && assert.ObjectsAreEqual([]string{"Go", "Docker"}, *u.TechStack)
	})).Return(&domain.Profile{ID: "u1"}, nil)

	_, err := f.uc.UpdateProfile(context.Background(), "u1", domain.ProfileUpdate{TechStack: &stack})

	require.NoError(t, err)
}

func TestUpdateNotificationSettings(t *testing.T) {
	ctx := context.Background()

	t.Run("unknown frequency is rejected", func(t *testing.T) {
		f := newFixture(t)

		_, err := f.uc.UpdateNotificationSettings(ctx, domain.NotificationSettings{UserID: "u1", Frequency: "weekly"})

		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("unknown type is rejected", func(t *testing.T) {
		f := newFixture(t)

		_, err := f.uc.UpdateNotificationSettings(ctx, domain.NotificationSettings{
			UserID: "u1", Frequency: domain.FrequencyDaily, Types: []domain.NotificationType{"spam"},
		})

		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("duplicates are dropped and a past mute time is cleared", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		past := fixedNow.Add(-time.Minute)
		f.preferences.On("SaveSettings", ctx, mock.MatchedBy(func(s domain.NotificationSettings) bool {
			return len(s.Types) == 1 && s.MutedUntil == nil
		})).Return(nil)

		got, err := f.uc.UpdateNotificationSettings(ctx, domain.NotificationSettings{
			UserID:     "u1",
			Frequency:  domain.FrequencyHourly,
			Types:      []domain.NotificationType{domain.NotifyBilling, domain.NotifyBilling},
			MutedUntil: &past,
		})

		require.NoError(t, err)
		assert.Equal(t, []domain.NotificationType{domain.NotifyBilling}, got.Types)
	})
}

func TestGetNotificationSettings_DefaultsWhenMissing(t *testing.T) {
	f := newFixture(t)
	f.preferences.On("GetSettings", mock.Anything, "u1").Return(nil, domain.ErrNotFound)

	got, err := f.uc.GetNotificationSettings(context.Background(), "u1")

	require.NoError(t, err)
	assert.Equal(t, domain.FrequencyInstant, got.Frequency)
	assert.Len(t, got.Types, 3)
}

func TestCreateSubscription(t *testing.T) {
	ctx := context.Background()

	t.Run("keyword is required", func(t *testing.T) {
		f := newFixture(t)

		_, err := f.uc.CreateSubscription(ctx, "u1", domain.Subscription{Keyword: "  ", IsActive: true})

		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	tests := []struct {
		name    string
		plan    domain.Plan
		active  int
		wantErr error
	}{
		{"default plan below limit", domain.PlanDefault, 1, nil},
		{"default plan at limit", domain.PlanDefault, 2, domain.ErrSubscriptionLimit},
		{"pro plan has a higher limit", domain.PlanPro, 4, nil},
		{"pro plan at limit", domain.PlanPro, 5, domain.ErrSubscriptionLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.allowAudit()
			f.profiles.On("Get", ctx, "u1").Return(&domain.Profile{ID: "u1", Plan: tt.plan}, nil)
			f.preferences.On("CountActiveSubscriptions", ctx, "u1").Return(tt.active, nil)
			if tt.wantErr == nil {
				f.preferences.On("CreateSubscription", ctx, mock.MatchedBy(func(s domain.Subscription) bool {
					return s.UserID == "u1" && s.Keyword == "golang"
				})).Return(&domain.Subscription{ID: 7, Keyword: "golang"}, nil)
			}

			got, err := f.uc.CreateSubscription(ctx, "u1", domain.Subscription{Keyword: " golang ", IsActive: true})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.EqualValues(t, 7, got.ID)
		})
	}

	t.Run("inactive subscription ignores the limit", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		f.profiles.On("Get", ctx, "u1").Return(&domain.Profile{ID: "u1", Plan: domain.PlanDefault}, nil)
		f.preferences.On("CreateSubscription", ctx, mock.Anything).Return(&domain.Subscription{ID: 1}, nil)

		_, err := f.uc.CreateSubscription(ctx, "u1", domain.Subscription{Keyword: "go", IsActive: false})

		require.NoError(t, err)
	})
}

func TestUpdateSubscription_ReactivationRespectsLimit(t *testing.T) {
	ctx := context.Background()
	active := true
	subs := []domain.Subscription{
		{ID: 1, IsActive: true}, {ID: 2, IsActive: true}, {ID: 3, IsActive: false},
	}

	t.Run("activating beyond the limit fails", func(t *testing.T) {
		f := newFixture(t)
		f.preferences.On("ListSubscriptions", ctx, "u1").Return(subs, nil)
		f.profiles.On("Get", ctx, "u1").Return(&domain.Profile{ID: "u1", Plan: domain.PlanDefault}, nil)

		_, err := f.uc.UpdateSubscription(ctx, "u1", 3, domain.SubscriptionUpdate{IsActive: &active})

		require.ErrorIs(t, err, domain.ErrSubscriptionLimit)
	})

	t.Run("an already active subscription can be edited", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		f.preferences.On("ListSubscriptions", ctx, "u1").Return(subs, nil)
		f.preferences.On("UpdateSubscription", ctx, "u1", int64(1), mock.Anything).
			Return(&domain.Subscription{ID: 1}, nil)

		_, err := f.uc.UpdateSubscription(ctx, "u1", 1, domain.SubscriptionUpdate{IsActive: &active})

		require.NoError(t, err)
	})
}

func TestSaveAndHistory(t *testing.T) {
	ctx := context.Background()

	t.Run("vacancy id is required", func(t *testing.T) {
		f := newFixture(t)

		require.ErrorIs(t, f.uc.SaveVacancy(ctx, "u1", domain.VacancyRef{}), domain.ErrInvalidInput)
		require.ErrorIs(t, f.uc.RecordView(ctx, "u1", domain.VacancyRef{VacancyID: " "}), domain.ErrInvalidInput)
	})

	t.Run("save delegates to the library", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		f.library.On("Save", ctx, "u1", domain.VacancyRef{VacancyID: "v1"}).Return(nil)

		require.NoError(t, f.uc.SaveVacancy(ctx, "u1", domain.VacancyRef{VacancyID: " v1 "}))
	})

	t.Run("page size is clamped", func(t *testing.T) {
		f := newFixture(t)
		f.library.On("ListSaved", ctx, "u1", domain.Page{Limit: maxPageSize, Offset: 0}).
			Return([]domain.SavedVacancy{}, int64(0), nil)

		_, _, err := f.uc.ListSaved(ctx, "u1", domain.Page{Limit: 10_000, Offset: -5})

		require.NoError(t, err)
	})

	t.Run("default page size is applied", func(t *testing.T) {
		f := newFixture(t)
		f.library.On("ListViews", ctx, "u1", domain.Page{Limit: defaultPage}).
			Return([]domain.ViewedVacancy{}, int64(0), nil)

		_, _, err := f.uc.ListHistory(ctx, "u1", domain.Page{})

		require.NoError(t, err)
	})
}

func TestTelegram(t *testing.T) {
	ctx := context.Background()

	t.Run("issued code expires after the ttl and has the expected shape", func(t *testing.T) {
		f := newFixture(t)
		f.telegram.On("SaveCode", ctx, mock.Anything).Return(nil)

		code, err := f.uc.IssueTelegramCode(ctx, "u1")

		require.NoError(t, err)
		assert.Len(t, code.Code, telegramCodeLength)
		assert.Regexp(t, "^[A-HJ-NP-Z2-9]+$", code.Code)
		assert.Equal(t, fixedNow.Add(10*time.Minute), code.ExpiresAt)
	})

	t.Run("confirm normalises the code and links the chat", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		f.telegram.On("ConsumeCode", ctx, "ABCD2345", fixedNow).Return("u1", nil)
		f.telegram.On("Link", ctx, "u1", int64(42)).Return(nil)

		require.NoError(t, f.uc.ConfirmTelegramLink(ctx, " abcd2345 ", 42))
	})

	t.Run("unknown or expired code is reported as invalid", func(t *testing.T) {
		f := newFixture(t)
		f.telegram.On("ConsumeCode", ctx, "BAD", fixedNow).Return("", domain.ErrNotFound)

		require.ErrorIs(t, f.uc.ConfirmTelegramLink(ctx, "bad", 42), domain.ErrInvalidTelegramCode)
	})
}

func TestAdminRules(t *testing.T) {
	ctx := context.Background()
	admin := domain.Claims{Subject: "admin", Role: domain.RoleAdmin}
	manager := domain.Claims{Subject: "mgr", Role: domain.RoleManager}
	user := domain.Claims{Subject: "usr", Role: domain.RoleUser}

	targetUser := &domain.Profile{ID: "t1", Role: domain.RoleUser}
	targetManager := &domain.Profile{ID: "t2", Role: domain.RoleManager}
	targetAdmin := &domain.Profile{ID: "t3", Role: domain.RoleAdmin}

	t.Run("a plain user cannot list users", func(t *testing.T) {
		f := newFixture(t)

		_, _, err := f.uc.ListUsers(ctx, user.Role, domain.UserFilter{})

		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("a manager list is limited to ordinary users", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("List", ctx, mock.MatchedBy(func(fl domain.UserFilter) bool {
			return fl.Role == domain.RoleUser && fl.Limit == defaultPage
		})).Return([]domain.Profile{}, int64(0), nil)

		_, _, err := f.uc.ListUsers(ctx, manager.Role, domain.UserFilter{Role: domain.RoleAdmin})

		require.NoError(t, err)
	})

	t.Run("an admin may filter by any role", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("List", ctx, mock.MatchedBy(func(fl domain.UserFilter) bool {
			return fl.Role == domain.RoleManager
		})).Return([]domain.Profile{}, int64(0), nil)

		_, _, err := f.uc.ListUsers(ctx, admin.Role, domain.UserFilter{Role: domain.RoleManager})

		require.NoError(t, err)
	})

	blockTests := []struct {
		name    string
		actor   domain.Claims
		target  *domain.Profile
		wantErr error
	}{
		{"manager blocks a user", manager, targetUser, nil},
		{"manager cannot block a manager", manager, targetManager, domain.ErrForbidden},
		{"admin blocks a manager", admin, targetManager, nil},
		{"nobody blocks an admin", admin, targetAdmin, domain.ErrForbidden},
		{"nobody blocks themselves", admin, &domain.Profile{ID: "admin", Role: domain.RoleUser}, domain.ErrForbidden},
		{"a plain user cannot block", user, targetUser, domain.ErrForbidden},
	}
	for _, tt := range blockTests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.allowAudit()
			if tt.actor.Role.Rank() >= domain.RoleManager.Rank() {
				f.profiles.On("Get", ctx, tt.target.ID).Return(tt.target, nil)
			}
			if tt.wantErr == nil {
				f.profiles.On("SetStatus", ctx, tt.target.ID, domain.StatusBlocked).Return(nil)
			}

			err := f.uc.BlockUser(ctx, tt.actor, tt.target.ID)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("unblock and delete are admin only", func(t *testing.T) {
		f := newFixture(t)

		require.ErrorIs(t, f.uc.UnblockUser(ctx, manager, "t1"), domain.ErrForbidden)
		require.ErrorIs(t, f.uc.DeleteUser(ctx, manager, "t1"), domain.ErrForbidden)
	})

	t.Run("admin deletes a manager", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Get", ctx, "t2").Return(targetManager, nil)
		f.profiles.On("Delete", ctx, "t2").Return(nil)

		require.NoError(t, f.uc.DeleteUser(ctx, admin, "t2"))
	})

	t.Run("manager sets a pro plan with a future expiry", func(t *testing.T) {
		f := newFixture(t)
		f.allowAudit()
		until := fixedNow.Add(24 * time.Hour)
		f.profiles.On("Get", ctx, "t1").Return(targetUser, nil)
		f.profiles.On("SetPlan", ctx, "t1", domain.PlanPro, &until).Return(nil)

		require.NoError(t, f.uc.SetPlan(ctx, manager, "t1", domain.PlanPro, &until))
	})

	t.Run("plan expiry in the past is rejected", func(t *testing.T) {
		f := newFixture(t)
		past := fixedNow.Add(-time.Hour)

		err := f.uc.SetPlan(ctx, manager, "t1", domain.PlanPro, &past)

		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("unknown plan is rejected", func(t *testing.T) {
		f := newFixture(t)

		require.ErrorIs(t, f.uc.SetPlan(ctx, manager, "t1", "platinum", nil), domain.ErrInvalidInput)
	})

	t.Run("a manager cannot view the activity of an admin", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Get", ctx, "t3").Return(targetAdmin, nil)

		_, _, err := f.uc.ListActivity(ctx, manager.Role, "t3", domain.Page{})

		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("an admin views the activity of a manager", func(t *testing.T) {
		f := newFixture(t)
		f.profiles.On("Get", ctx, "t2").Return(targetManager, nil)
		f.activity.On("List", ctx, "t2", domain.Page{Limit: defaultPage}).Return([]domain.Activity{}, int64(0), nil)

		_, _, err := f.uc.ListActivity(ctx, admin.Role, "t2", domain.Page{})

		require.NoError(t, err)
	})
}

func TestAuditFailureDoesNotBreakTheRequest(t *testing.T) {
	f := newFixture(t)
	f.activity.On("Add", mock.Anything, mock.Anything).Return(errors.New("audit down"))
	f.library.On("Save", mock.Anything, "u1", mock.Anything).Return(nil)

	require.NoError(t, f.uc.SaveVacancy(context.Background(), "u1", domain.VacancyRef{VacancyID: "v1"}))
}
