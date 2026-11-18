package domain

import (
	"context"
	"time"
)

// UserUsecase is the application logic seen by the transport layer.
//
//go:generate mockery --name UserUsecase --output ../mocks/domain --outpkg domain --case=underscore
type UserUsecase interface {
	// Authenticate creates the profile on the first call, refreshes role and e-mail from the token
	// and rejects blocked accounts.
	Authenticate(ctx context.Context, claims Claims) (*Profile, error)

	GetProfile(ctx context.Context, userID string) (*Profile, error)
	UpdateProfile(ctx context.Context, userID string, upd ProfileUpdate) (*Profile, error)
	DeleteAccount(ctx context.Context, userID string) error

	GetNotificationSettings(ctx context.Context, userID string) (*NotificationSettings, error)
	UpdateNotificationSettings(ctx context.Context, s NotificationSettings) (*NotificationSettings, error)

	ListSubscriptions(ctx context.Context, userID string) ([]Subscription, error)
	CreateSubscription(ctx context.Context, userID string, s Subscription) (*Subscription, error)
	UpdateSubscription(ctx context.Context, userID string, id int64, upd SubscriptionUpdate) (*Subscription, error)
	DeleteSubscription(ctx context.Context, userID string, id int64) error

	SaveVacancy(ctx context.Context, userID string, v VacancyRef) error
	UnsaveVacancy(ctx context.Context, userID, vacancyID string) error
	ListSaved(ctx context.Context, userID string, p Page) ([]SavedVacancy, int64, error)

	RecordView(ctx context.Context, userID string, v VacancyRef) error
	ListHistory(ctx context.Context, userID string, p Page) ([]ViewedVacancy, int64, error)

	IssueTelegramCode(ctx context.Context, userID string) (*TelegramCode, error)
	ConfirmTelegramLink(ctx context.Context, code string, chatID int64) error
	UnlinkTelegram(ctx context.Context, userID string) error

	ListUsers(ctx context.Context, actor Role, f UserFilter) ([]Profile, int64, error)
	GetUser(ctx context.Context, actor Role, userID string) (*Profile, error)
	ListActivity(ctx context.Context, actor Role, userID string, p Page) ([]Activity, int64, error)
	SetPlan(ctx context.Context, actor Claims, userID string, plan Plan, expiresAt *time.Time) error
	BlockUser(ctx context.Context, actor Claims, userID string) error
	UnblockUser(ctx context.Context, actor Claims, userID string) error
	DeleteUser(ctx context.Context, actor Claims, userID string) error
}

// ProfileRepository stores accounts.
//
//go:generate mockery --name ProfileRepository --output ../mocks/domain --outpkg domain --case=underscore
type ProfileRepository interface {
	// Upsert inserts the profile or refreshes e-mail, role and last-seen time; it reports whether the row is new.
	Upsert(ctx context.Context, c Claims) (p *Profile, created bool, err error)
	Get(ctx context.Context, id string) (*Profile, error)
	Update(ctx context.Context, id string, upd ProfileUpdate) (*Profile, error)
	List(ctx context.Context, f UserFilter) ([]Profile, int64, error)
	SetPlan(ctx context.Context, id string, plan Plan, expiresAt *time.Time) error
	SetStatus(ctx context.Context, id string, status Status) error
	Delete(ctx context.Context, id string) error
}

// PreferenceRepository stores notification settings and subscriptions.
//
//go:generate mockery --name PreferenceRepository --output ../mocks/domain --outpkg domain --case=underscore
type PreferenceRepository interface {
	GetSettings(ctx context.Context, userID string) (*NotificationSettings, error)
	SaveSettings(ctx context.Context, s NotificationSettings) error

	ListSubscriptions(ctx context.Context, userID string) ([]Subscription, error)
	CountActiveSubscriptions(ctx context.Context, userID string) (int, error)
	CreateSubscription(ctx context.Context, s Subscription) (*Subscription, error)
	UpdateSubscription(ctx context.Context, userID string, id int64, upd SubscriptionUpdate) (*Subscription, error)
	DeleteSubscription(ctx context.Context, userID string, id int64) error
}

// LibraryRepository stores the saved list and the view history.
//
//go:generate mockery --name LibraryRepository --output ../mocks/domain --outpkg domain --case=underscore
type LibraryRepository interface {
	Save(ctx context.Context, userID string, v VacancyRef) error
	Unsave(ctx context.Context, userID, vacancyID string) error
	ListSaved(ctx context.Context, userID string, p Page) ([]SavedVacancy, int64, error)

	AddView(ctx context.Context, userID string, v VacancyRef) error
	ListViews(ctx context.Context, userID string, p Page) ([]ViewedVacancy, int64, error)
}

// TelegramRepository stores verification codes and the chat bound to an account.
//
//go:generate mockery --name TelegramRepository --output ../mocks/domain --outpkg domain --case=underscore
type TelegramRepository interface {
	SaveCode(ctx context.Context, c TelegramCode) error
	// ConsumeCode atomically removes a non-expired code and returns the user it belongs to.
	ConsumeCode(ctx context.Context, code string, now time.Time) (userID string, err error)
	Link(ctx context.Context, userID string, chatID int64) error
	Unlink(ctx context.Context, userID string) error
}

// ActivityRepository stores the audit trail.
//
//go:generate mockery --name ActivityRepository --output ../mocks/domain --outpkg domain --case=underscore
type ActivityRepository interface {
	Add(ctx context.Context, a Activity) error
	List(ctx context.Context, userID string, p Page) ([]Activity, int64, error)
}
