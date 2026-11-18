// Package usecase implements the application logic of the UserService.
package usecase

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

const (
	maxDisplayName = 100
	maxKeyword     = 255
	maxStackItems  = 30
	maxText        = 100
	maxPageSize    = 100
	defaultPage    = 20

	// telegramCodeAlphabet has no look-alike characters (0/O, 1/I) to make codes easy to type.
	telegramCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	telegramCodeLength   = 8
)

// Limits are the numbers the use case enforces; they come from the configuration.
type Limits struct {
	DefaultPlanSubscriptions int
	ProPlanSubscriptions     int
	TelegramCodeTTL          time.Duration
}

// Deps groups the ports of the use case.
type Deps struct {
	Profiles    domain.ProfileRepository
	Preferences domain.PreferenceRepository
	Library     domain.LibraryRepository
	Telegram    domain.TelegramRepository
	Activity    domain.ActivityRepository
}

// UserInteractor implements domain.UserUsecase.
type UserInteractor struct {
	Deps

	limits Limits
	now    func() time.Time
	logger *slog.Logger
}

// NewUserInteractor builds the use case.
func NewUserInteractor(deps Deps, limits Limits) *UserInteractor {
	return &UserInteractor{
		Deps:   deps,
		limits: limits,
		now:    time.Now,
		logger: slog.With(slog.String("component", "userInteractor")),
	}
}

// Authenticate registers the profile on the first request and refuses blocked accounts.
func (u *UserInteractor) Authenticate(ctx context.Context, claims domain.Claims) (*domain.Profile, error) {
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: token has no subject", domain.ErrInvalidInput)
	}
	p, created, err := u.Profiles.Upsert(ctx, claims)
	if err != nil {
		return nil, fmt.Errorf("upsert profile: %w", err)
	}
	if created {
		u.audit(ctx, p.ID, p.ID, domain.ActionProfileCreated, nil)
	}
	if p.Status == domain.StatusBlocked {
		return nil, domain.ErrAccountBlocked
	}
	u.applyPlanExpiry(p)
	return p, nil
}

// GetProfile returns the account of the user.
func (u *UserInteractor) GetProfile(ctx context.Context, userID string) (*domain.Profile, error) {
	p, err := u.Profiles.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	u.applyPlanExpiry(p)
	return p, nil
}

// UpdateProfile validates and applies a partial update of the profile.
func (u *UserInteractor) UpdateProfile(
	ctx context.Context, userID string, upd domain.ProfileUpdate,
) (*domain.Profile, error) {
	if err := validateProfileUpdate(&upd); err != nil {
		return nil, err
	}
	p, err := u.Profiles.Update(ctx, userID, upd)
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionProfileUpdated, nil)
	u.applyPlanExpiry(p)
	return p, nil
}

func validateProfileUpdate(upd *domain.ProfileUpdate) error {
	if upd.DisplayName != nil {
		name := strings.TrimSpace(*upd.DisplayName)
		if utf8.RuneCountInString(name) > maxDisplayName {
			return fmt.Errorf("%w: display name is longer than %d characters", domain.ErrInvalidInput, maxDisplayName)
		}
		upd.DisplayName = &name
	}
	if upd.TechStack != nil {
		stack := normalizeList(*upd.TechStack)
		if len(stack) > maxStackItems {
			return fmt.Errorf("%w: more than %d technologies", domain.ErrInvalidInput, maxStackItems)
		}
		upd.TechStack = &stack
	}
	for _, s := range []*string{upd.Experience, upd.Location} {
		if s != nil && utf8.RuneCountInString(*s) > maxText {
			return fmt.Errorf("%w: text is longer than %d characters", domain.ErrInvalidInput, maxText)
		}
	}
	if upd.ExpectedSalary != nil && *upd.ExpectedSalary < 0 {
		return fmt.Errorf("%w: expected salary cannot be negative", domain.ErrInvalidInput)
	}
	return nil
}

// DeleteAccount removes the account of the user together with everything attached to it.
func (u *UserInteractor) DeleteAccount(ctx context.Context, userID string) error {
	if err := u.Profiles.Delete(ctx, userID); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}

// GetNotificationSettings returns the settings, or the defaults when the user never saved any.
func (u *UserInteractor) GetNotificationSettings(
	ctx context.Context, userID string,
) (*domain.NotificationSettings, error) {
	s, err := u.Preferences.GetSettings(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return defaultSettings(userID), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	return s, nil
}

func defaultSettings(userID string) *domain.NotificationSettings {
	return &domain.NotificationSettings{
		UserID:    userID,
		Frequency: domain.FrequencyInstant,
		Types:     []domain.NotificationType{domain.NotifyNewVacancy, domain.NotifyBilling, domain.NotifySystem},
	}
}

// UpdateNotificationSettings validates and stores the settings.
func (u *UserInteractor) UpdateNotificationSettings(
	ctx context.Context, s domain.NotificationSettings,
) (*domain.NotificationSettings, error) {
	if !s.Frequency.Valid() {
		return nil, fmt.Errorf("%w: unknown frequency %q", domain.ErrInvalidInput, s.Frequency)
	}
	seen := make(map[domain.NotificationType]bool, len(s.Types))
	types := make([]domain.NotificationType, 0, len(s.Types))
	for _, t := range s.Types {
		if !t.Valid() {
			return nil, fmt.Errorf("%w: unknown notification type %q", domain.ErrInvalidInput, t)
		}
		if !seen[t] {
			seen[t] = true
			types = append(types, t)
		}
	}
	s.Types = types
	if s.MutedUntil != nil && !s.MutedUntil.After(u.now()) {
		s.MutedUntil = nil
	}
	if err := u.Preferences.SaveSettings(ctx, s); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}
	u.audit(ctx, s.UserID, s.UserID, domain.ActionSettingsUpdated, nil)
	return &s, nil
}

// ListSubscriptions returns the filter-triggers of the user.
func (u *UserInteractor) ListSubscriptions(ctx context.Context, userID string) ([]domain.Subscription, error) {
	subs, err := u.Preferences.ListSubscriptions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return subs, nil
}

// CreateSubscription adds a filter-trigger if the plan still allows one more active subscription.
func (u *UserInteractor) CreateSubscription(
	ctx context.Context, userID string, s domain.Subscription,
) (*domain.Subscription, error) {
	s.Keyword = strings.TrimSpace(s.Keyword)
	s.Location = strings.TrimSpace(s.Location)
	if err := validateSubscription(s.Keyword, s.Location, s.MinSalary); err != nil {
		return nil, err
	}
	p, err := u.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.IsActive {
		n, cErr := u.Preferences.CountActiveSubscriptions(ctx, userID)
		if cErr != nil {
			return nil, fmt.Errorf("count subscriptions: %w", cErr)
		}
		if n >= u.subscriptionLimit(p.Plan) {
			return nil, domain.ErrSubscriptionLimit
		}
	}
	s.UserID = userID
	created, err := u.Preferences.CreateSubscription(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionSubscriptionAdd, map[string]any{"id": created.ID})
	return created, nil
}

// UpdateSubscription applies a partial update; reactivating a subscription is checked against the plan limit.
func (u *UserInteractor) UpdateSubscription(
	ctx context.Context, userID string, id int64, upd domain.SubscriptionUpdate,
) (*domain.Subscription, error) {
	if upd.Keyword != nil {
		k := strings.TrimSpace(*upd.Keyword)
		upd.Keyword = &k
	}
	if upd.Location != nil {
		l := strings.TrimSpace(*upd.Location)
		upd.Location = &l
	}
	keyword, location, minSalary := "x", "", 0
	if upd.Keyword != nil {
		keyword = *upd.Keyword
	}
	if upd.Location != nil {
		location = *upd.Location
	}
	if upd.MinSalary != nil {
		minSalary = *upd.MinSalary
	}
	if err := validateSubscription(keyword, location, minSalary); err != nil {
		return nil, err
	}
	if upd.IsActive != nil && *upd.IsActive {
		if err := u.checkCanActivate(ctx, userID, id); err != nil {
			return nil, err
		}
	}
	s, err := u.Preferences.UpdateSubscription(ctx, userID, id, upd)
	if err != nil {
		return nil, fmt.Errorf("update subscription: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionSubscriptionEdit, map[string]any{"id": id})
	return s, nil
}

func (u *UserInteractor) checkCanActivate(ctx context.Context, userID string, id int64) error {
	subs, err := u.Preferences.ListSubscriptions(ctx, userID)
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	active := 0
	for _, s := range subs {
		if s.ID == id && s.IsActive {
			return nil // already active, nothing changes
		}
		if s.IsActive {
			active++
		}
	}
	p, err := u.GetProfile(ctx, userID)
	if err != nil {
		return err
	}
	if active >= u.subscriptionLimit(p.Plan) {
		return domain.ErrSubscriptionLimit
	}
	return nil
}

// DeleteSubscription removes a filter-trigger.
func (u *UserInteractor) DeleteSubscription(ctx context.Context, userID string, id int64) error {
	if err := u.Preferences.DeleteSubscription(ctx, userID, id); err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionSubscriptionDel, map[string]any{"id": id})
	return nil
}

func validateSubscription(keyword, location string, minSalary int) error {
	switch {
	case keyword == "":
		return fmt.Errorf("%w: keyword is required", domain.ErrInvalidInput)
	case utf8.RuneCountInString(keyword) > maxKeyword || utf8.RuneCountInString(location) > maxKeyword:
		return fmt.Errorf("%w: keyword or location is too long", domain.ErrInvalidInput)
	case minSalary < 0:
		return fmt.Errorf("%w: minimal salary cannot be negative", domain.ErrInvalidInput)
	}
	return nil
}

func (u *UserInteractor) subscriptionLimit(p domain.Plan) int {
	if p == domain.PlanPro {
		return u.limits.ProPlanSubscriptions
	}
	return u.limits.DefaultPlanSubscriptions
}

// SaveVacancy adds a vacancy to the saved list; saving it twice is not an error.
func (u *UserInteractor) SaveVacancy(ctx context.Context, userID string, v domain.VacancyRef) error {
	if err := validateRef(&v); err != nil {
		return err
	}
	if err := u.Library.Save(ctx, userID, v); err != nil {
		return fmt.Errorf("save vacancy: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionVacancySaved, map[string]any{"vacancy_id": v.VacancyID})
	return nil
}

// UnsaveVacancy removes a vacancy from the saved list.
func (u *UserInteractor) UnsaveVacancy(ctx context.Context, userID, vacancyID string) error {
	if err := u.Library.Unsave(ctx, userID, vacancyID); err != nil {
		return fmt.Errorf("unsave vacancy: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionVacancyUnsaved, map[string]any{"vacancy_id": vacancyID})
	return nil
}

// ListSaved returns a page of the saved list, newest first.
func (u *UserInteractor) ListSaved(
	ctx context.Context, userID string, p domain.Page,
) ([]domain.SavedVacancy, int64, error) {
	items, total, err := u.Library.ListSaved(ctx, userID, clampPage(p))
	if err != nil {
		return nil, 0, fmt.Errorf("list saved: %w", err)
	}
	return items, total, nil
}

// RecordView puts a vacancy at the top of the view history.
func (u *UserInteractor) RecordView(ctx context.Context, userID string, v domain.VacancyRef) error {
	if err := validateRef(&v); err != nil {
		return err
	}
	if err := u.Library.AddView(ctx, userID, v); err != nil {
		return fmt.Errorf("record view: %w", err)
	}
	return nil
}

// ListHistory returns a page of the view history, newest first.
func (u *UserInteractor) ListHistory(
	ctx context.Context, userID string, p domain.Page,
) ([]domain.ViewedVacancy, int64, error) {
	items, total, err := u.Library.ListViews(ctx, userID, clampPage(p))
	if err != nil {
		return nil, 0, fmt.Errorf("list history: %w", err)
	}
	return items, total, nil
}

func validateRef(v *domain.VacancyRef) error {
	v.VacancyID = strings.TrimSpace(v.VacancyID)
	if v.VacancyID == "" {
		return fmt.Errorf("%w: vacancy id is required", domain.ErrInvalidInput)
	}
	return nil
}

// IssueTelegramCode creates a one-time code the user sends to the bot to bind the chat.
func (u *UserInteractor) IssueTelegramCode(ctx context.Context, userID string) (*domain.TelegramCode, error) {
	code, err := generateCode()
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	c := domain.TelegramCode{Code: code, UserID: userID, ExpiresAt: u.now().Add(u.limits.TelegramCodeTTL)}
	if err := u.Telegram.SaveCode(ctx, c); err != nil {
		return nil, fmt.Errorf("save code: %w", err)
	}
	return &c, nil
}

// ConfirmTelegramLink is called by the bot: it exchanges a code for a binding of the chat to the account.
func (u *UserInteractor) ConfirmTelegramLink(ctx context.Context, code string, chatID int64) error {
	userID, err := u.Telegram.ConsumeCode(ctx, strings.ToUpper(strings.TrimSpace(code)), u.now())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrInvalidTelegramCode
	}
	if err != nil {
		return fmt.Errorf("consume code: %w", err)
	}
	if err := u.Telegram.Link(ctx, userID, chatID); err != nil {
		return fmt.Errorf("link telegram: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionTelegramLinked, nil)
	return nil
}

// UnlinkTelegram detaches the chat from the account.
func (u *UserInteractor) UnlinkTelegram(ctx context.Context, userID string) error {
	if err := u.Telegram.Unlink(ctx, userID); err != nil {
		return fmt.Errorf("unlink telegram: %w", err)
	}
	u.audit(ctx, userID, userID, domain.ActionTelegramUnlinked, nil)
	return nil
}

func generateCode() (string, error) {
	buf := make([]byte, telegramCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	for i, b := range buf {
		buf[i] = telegramCodeAlphabet[int(b)%len(telegramCodeAlphabet)]
	}
	return string(buf), nil
}

// ListUsers is the admin/manager list of accounts; a manager sees only ordinary users.
func (u *UserInteractor) ListUsers(
	ctx context.Context, actor domain.Role, f domain.UserFilter,
) ([]domain.Profile, int64, error) {
	if actor.Rank() < domain.RoleManager.Rank() {
		return nil, 0, domain.ErrForbidden
	}
	if actor == domain.RoleManager {
		f.Role = domain.RoleUser
	}
	f.Page = clampPage(f.Page)
	users, total, err := u.Profiles.List(ctx, f)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	for i := range users {
		u.applyPlanExpiry(&users[i])
	}
	return users, total, nil
}

// GetUser returns one account for the admin/manager views.
func (u *UserInteractor) GetUser(ctx context.Context, actor domain.Role, userID string) (*domain.Profile, error) {
	p, err := u.Profiles.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if !canView(actor, p.Role) {
		return nil, domain.ErrForbidden
	}
	u.applyPlanExpiry(p)
	return p, nil
}

// ListActivity returns the audit trail of an account.
func (u *UserInteractor) ListActivity(
	ctx context.Context, actor domain.Role, userID string, p domain.Page,
) ([]domain.Activity, int64, error) {
	target, err := u.Profiles.Get(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("get user: %w", err)
	}
	if !canView(actor, target.Role) {
		return nil, 0, domain.ErrForbidden
	}
	items, total, err := u.Activity.List(ctx, userID, clampPage(p))
	if err != nil {
		return nil, 0, fmt.Errorf("list activity: %w", err)
	}
	return items, total, nil
}

// SetPlan changes the tariff plan of a user (manager and admin).
func (u *UserInteractor) SetPlan(
	ctx context.Context, actor domain.Claims, userID string, plan domain.Plan, expiresAt *time.Time,
) error {
	if !plan.Valid() {
		return fmt.Errorf("%w: unknown plan %q", domain.ErrInvalidInput, plan)
	}
	if plan == domain.PlanDefault {
		expiresAt = nil
	} else if expiresAt != nil && !expiresAt.After(u.now()) {
		return fmt.Errorf("%w: plan expiry must be in the future", domain.ErrInvalidInput)
	}
	if _, err := u.manageable(ctx, actor, userID, domain.RoleManager); err != nil {
		return err
	}
	if err := u.Profiles.SetPlan(ctx, userID, plan, expiresAt); err != nil {
		return fmt.Errorf("set plan: %w", err)
	}
	u.audit(ctx, userID, actor.Subject, domain.ActionPlanChanged, map[string]any{"plan": plan})
	return nil
}

// BlockUser disables an account; a manager may block users, an admin also managers.
func (u *UserInteractor) BlockUser(ctx context.Context, actor domain.Claims, userID string) error {
	if _, err := u.manageable(ctx, actor, userID, domain.RoleManager); err != nil {
		return err
	}
	if err := u.Profiles.SetStatus(ctx, userID, domain.StatusBlocked); err != nil {
		return fmt.Errorf("block user: %w", err)
	}
	u.audit(ctx, userID, actor.Subject, domain.ActionBlocked, nil)
	return nil
}

// UnblockUser re-enables an account (admin only).
func (u *UserInteractor) UnblockUser(ctx context.Context, actor domain.Claims, userID string) error {
	if _, err := u.manageable(ctx, actor, userID, domain.RoleAdmin); err != nil {
		return err
	}
	if err := u.Profiles.SetStatus(ctx, userID, domain.StatusActive); err != nil {
		return fmt.Errorf("unblock user: %w", err)
	}
	u.audit(ctx, userID, actor.Subject, domain.ActionUnblocked, nil)
	return nil
}

// DeleteUser removes the account of a user or a manager (admin only).
func (u *UserInteractor) DeleteUser(ctx context.Context, actor domain.Claims, userID string) error {
	if _, err := u.manageable(ctx, actor, userID, domain.RoleAdmin); err != nil {
		return err
	}
	if err := u.Profiles.Delete(ctx, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// manageable loads the target and checks that the actor has at least the minimal role
// and outranks the target; nobody manages themselves or an admin.
func (u *UserInteractor) manageable(
	ctx context.Context, actor domain.Claims, userID string, minRole domain.Role,
) (*domain.Profile, error) {
	if actor.Role.Rank() < minRole.Rank() {
		return nil, domain.ErrForbidden
	}
	target, err := u.Profiles.Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get target: %w", err)
	}
	if target.ID == actor.Subject || target.Role.Rank() >= actor.Role.Rank() {
		return nil, domain.ErrForbidden
	}
	return target, nil
}

// canView reports whether the actor may look at an account of the given role.
func canView(actor, target domain.Role) bool {
	return actor.Rank() >= domain.RoleManager.Rank() && actor.Rank() > target.Rank()
}

// applyPlanExpiry downgrades an expired paid plan in the view returned to callers.
func (u *UserInteractor) applyPlanExpiry(p *domain.Profile) {
	if p.Plan == domain.PlanPro && p.PlanExpiresAt != nil && !p.PlanExpiresAt.After(u.now()) {
		p.Plan = domain.PlanDefault
		p.PlanExpiresAt = nil
	}
}

// audit writes an activity record; a failure is logged but never breaks the request.
func (u *UserInteractor) audit(ctx context.Context, userID, actorID, action string, meta map[string]any) {
	err := u.Activity.Add(ctx, domain.Activity{
		UserID:  userID,
		ActorID: actorID,
		Action:  action,
		Meta:    meta,
	})
	if err != nil {
		u.logger.Warn("failed to write activity",
			slog.String("action", action), slog.String("user_id", userID), slog.Any("error", err))
	}
}

func clampPage(p domain.Page) domain.Page {
	if p.Limit <= 0 {
		p.Limit = defaultPage
	}
	p.Limit = min(p.Limit, maxPageSize)
	p.Offset = max(p.Offset, 0)
	return p
}

func normalizeList(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
