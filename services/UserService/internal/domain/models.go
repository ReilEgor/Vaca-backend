// Package domain holds the entities, errors and ports of the UserService.
package domain

import "time"

// Role is an access level taken from the Keycloak realm roles of the JWT.
type Role string

// Roles known to the system (ТЗ 4.1.2: user, manager, admin).
const (
	RoleUser    Role = "user"
	RoleManager Role = "manager"
	RoleAdmin   Role = "admin"
)

// Rank orders roles so that a higher role includes the rights of a lower one.
func (r Role) Rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleManager:
		return 2
	case RoleUser:
		return 1
	default:
		return 0
	}
}

// Plan is a tariff plan of a user (ТЗ 4.1.6).
type Plan string

// Tariff plans.
const (
	PlanDefault Plan = "default"
	PlanPro     Plan = "pro"
)

// Valid reports whether the plan is a known one.
func (p Plan) Valid() bool { return p == PlanDefault || p == PlanPro }

// Status is an account state; a blocked account cannot use the API.
type Status string

// Account statuses.
const (
	StatusActive  Status = "active"
	StatusBlocked Status = "blocked"
)

// Frequency is how often the user wants to receive notifications.
type Frequency string

// Notification frequencies.
const (
	FrequencyInstant Frequency = "instant"
	FrequencyHourly  Frequency = "hourly"
	FrequencyDaily   Frequency = "daily"
)

// Valid reports whether the frequency is a known one.
func (f Frequency) Valid() bool {
	return f == FrequencyInstant || f == FrequencyHourly || f == FrequencyDaily
}

// NotificationType is a kind of message the user can opt in or out of.
type NotificationType string

// Notification types.
const (
	NotifyNewVacancy NotificationType = "new_vacancy"
	NotifyBilling    NotificationType = "billing"
	NotifySystem     NotificationType = "system"
)

// Valid reports whether the type is a known one.
func (t NotificationType) Valid() bool {
	return t == NotifyNewVacancy || t == NotifyBilling || t == NotifySystem
}

// Claims is the identity extracted from a verified JWT.
type Claims struct {
	Subject  string
	Email    string
	Username string
	Role     Role
}

// Profile is the account of a person; its ID is the Keycloak subject.
type Profile struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	DisplayName    string     `json:"display_name"`
	Role           Role       `json:"role"`
	Status         Status     `json:"status"`
	Plan           Plan       `json:"plan"`
	PlanExpiresAt  *time.Time `json:"plan_expires_at,omitempty"`
	TechStack      []string   `json:"tech_stack"`
	Experience     string     `json:"experience"`
	Location       string     `json:"location"`
	ExpectedSalary int        `json:"expected_salary"`
	TelegramLinked bool       `json:"telegram_linked"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
}

// ProfileUpdate is a partial update of the profile fields the user controls; nil means "keep".
type ProfileUpdate struct {
	DisplayName    *string
	TechStack      *[]string
	Experience     *string
	Location       *string
	ExpectedSalary *int
}

// NotificationSettings are the delivery preferences of a user.
type NotificationSettings struct {
	UserID     string             `json:"-"`
	Frequency  Frequency          `json:"frequency"`
	Types      []NotificationType `json:"types"`
	Muted      bool               `json:"muted"`
	MutedUntil *time.Time         `json:"muted_until,omitempty"`
}

// Subscription is a saved search the user wants to be notified about (filter-trigger).
type Subscription struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"-"`
	Keyword   string    `json:"keyword"`
	Location  string    `json:"location"`
	MinSalary int       `json:"min_salary"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// SubscriptionUpdate is a partial update of a subscription; nil means "keep".
type SubscriptionUpdate struct {
	Keyword   *string
	Location  *string
	MinSalary *int
	IsActive  *bool
}

// VacancyRef is a snapshot of a vacancy kept in the saved list and the view history,
// so the lists can be rendered without a call to another service.
type VacancyRef struct {
	VacancyID string `json:"vacancy_id"`
	Title     string `json:"title"`
	Company   string `json:"company"`
	Location  string `json:"location"`
	Salary    string `json:"salary"`
	Link      string `json:"link"`
}

// SavedVacancy is a vacancy in the "Saved Jobs" list.
type SavedVacancy struct {
	VacancyRef
	SavedAt time.Time `json:"saved_at"`
}

// ViewedVacancy is an entry of the view history.
type ViewedVacancy struct {
	VacancyRef
	ViewedAt time.Time `json:"viewed_at"`
}

// TelegramCode is a one-time verification code used to bind a Telegram chat to an account.
type TelegramCode struct {
	Code      string
	UserID    string
	ExpiresAt time.Time
}

// Activity is an audit record of an action of or on a user.
type Activity struct {
	ID        int64          `json:"id"`
	UserID    string         `json:"user_id"`
	ActorID   string         `json:"actor_id"`
	Action    string         `json:"action"`
	Meta      map[string]any `json:"meta,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// Activity actions.
const (
	ActionProfileCreated   = "profile_created"
	ActionProfileUpdated   = "profile_updated"
	ActionVacancySaved     = "vacancy_saved"
	ActionVacancyUnsaved   = "vacancy_unsaved"
	ActionVacancyViewed    = "vacancy_viewed"
	ActionSubscriptionAdd  = "subscription_added"
	ActionSubscriptionEdit = "subscription_updated"
	ActionSubscriptionDel  = "subscription_deleted"
	ActionSettingsUpdated  = "notification_settings_updated"
	ActionTelegramLinked   = "telegram_linked"
	ActionTelegramUnlinked = "telegram_unlinked"
	ActionPlanChanged      = "plan_changed"
	ActionBlocked          = "blocked"
	ActionUnblocked        = "unblocked"
)

// Page is a window over an ordered list.
type Page struct {
	Limit  int
	Offset int
}

// UserFilter narrows the admin list of users.
type UserFilter struct {
	Page
	Search string
	Role   Role
	Status Status
}
