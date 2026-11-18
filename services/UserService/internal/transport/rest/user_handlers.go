package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

// respondError maps a domain error to an HTTP status. Unknown errors are logged and
// reported without technical details (ТЗ 4.2).
func (s *Server) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput), errors.Is(err, domain.ErrInvalidTelegramCode):
		abort(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrUserNotFound), errors.Is(err, domain.ErrSubscriptionNotFound),
		errors.Is(err, domain.ErrNotFound):
		abort(c, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrForbidden):
		abort(c, http.StatusForbidden, "insufficient permissions")
	case errors.Is(err, domain.ErrAccountBlocked):
		abort(c, http.StatusForbidden, "account is blocked")
	case errors.Is(err, domain.ErrSubscriptionLimit):
		abort(c, http.StatusConflict, err.Error())
	default:
		s.logger.Error("request failed", slog.String("path", c.FullPath()), slog.Any("error", err))
		abort(c, http.StatusInternalServerError, "internal error")
	}
}

func badRequest(c *gin.Context, msg string) { abort(c, http.StatusBadRequest, msg) }

func pageOf(c *gin.Context) domain.Page {
	return domain.Page{Limit: intQuery(c, "limit"), Offset: intQuery(c, "offset")}
}

// intQuery reads a numeric query parameter; a missing or malformed value counts as zero,
// which the use case replaces with the defaults.
func intQuery(c *gin.Context, key string) int {
	n, err := strconv.Atoi(c.Query(key))
	if err != nil {
		return 0
	}
	return n
}

type listResponse[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

// --- profile ---

type updateProfileRequest struct {
	DisplayName    *string   `json:"display_name"`
	TechStack      *[]string `json:"tech_stack"`
	Experience     *string   `json:"experience"`
	Location       *string   `json:"location"`
	ExpectedSalary *int      `json:"expected_salary"`
}

func (s *Server) getMe(c *gin.Context) {
	p, err := s.uc.GetProfile(c.Request.Context(), profileOf(c).ID)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (s *Server) updateMe(c *gin.Context) {
	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	p, err := s.uc.UpdateProfile(c.Request.Context(), profileOf(c).ID, domain.ProfileUpdate{
		DisplayName:    req.DisplayName,
		TechStack:      req.TechStack,
		Experience:     req.Experience,
		Location:       req.Location,
		ExpectedSalary: req.ExpectedSalary,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (s *Server) deleteMe(c *gin.Context) {
	if err := s.uc.DeleteAccount(c.Request.Context(), profileOf(c).ID); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- notification settings ---

type settingsRequest struct {
	Frequency  domain.Frequency          `json:"frequency"`
	Types      []domain.NotificationType `json:"types"`
	Muted      bool                      `json:"muted"`
	MutedUntil *time.Time                `json:"muted_until"`
}

func (s *Server) getSettings(c *gin.Context) {
	st, err := s.uc.GetNotificationSettings(c.Request.Context(), profileOf(c).ID)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) putSettings(c *gin.Context) {
	var req settingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	st, err := s.uc.UpdateNotificationSettings(c.Request.Context(), domain.NotificationSettings{
		UserID:     profileOf(c).ID,
		Frequency:  req.Frequency,
		Types:      req.Types,
		Muted:      req.Muted,
		MutedUntil: req.MutedUntil,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, st)
}

// --- subscriptions ---

type createSubscriptionRequest struct {
	Keyword   string `json:"keyword"`
	Location  string `json:"location"`
	MinSalary int    `json:"min_salary"`
	IsActive  *bool  `json:"is_active"`
}

type updateSubscriptionRequest struct {
	Keyword   *string `json:"keyword"`
	Location  *string `json:"location"`
	MinSalary *int    `json:"min_salary"`
	IsActive  *bool   `json:"is_active"`
}

func (s *Server) listSubscriptions(c *gin.Context) {
	subs, err := s.uc.ListSubscriptions(c.Request.Context(), profileOf(c).ID)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, listResponse[domain.Subscription]{Items: subs, Total: int64(len(subs))})
}

func (s *Server) createSubscription(c *gin.Context) {
	var req createSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	active := req.IsActive == nil || *req.IsActive
	sub, err := s.uc.CreateSubscription(c.Request.Context(), profileOf(c).ID, domain.Subscription{
		Keyword:   req.Keyword,
		Location:  req.Location,
		MinSalary: req.MinSalary,
		IsActive:  active,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, sub)
}

func (s *Server) updateSubscription(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		badRequest(c, "invalid subscription id")
		return
	}
	var req updateSubscriptionRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	sub, err := s.uc.UpdateSubscription(c.Request.Context(), profileOf(c).ID, id, domain.SubscriptionUpdate{
		Keyword:   req.Keyword,
		Location:  req.Location,
		MinSalary: req.MinSalary,
		IsActive:  req.IsActive,
	})
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, sub)
}

func (s *Server) deleteSubscription(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		badRequest(c, "invalid subscription id")
		return
	}
	if err = s.uc.DeleteSubscription(c.Request.Context(), profileOf(c).ID, id); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- saved vacancies and view history ---

type vacancyRequest struct {
	VacancyID string `json:"vacancy_id"`
	Title     string `json:"title"`
	Company   string `json:"company"`
	Location  string `json:"location"`
	Salary    string `json:"salary"`
	Link      string `json:"link"`
}

func (r vacancyRequest) ref() domain.VacancyRef {
	return domain.VacancyRef{
		VacancyID: r.VacancyID, Title: r.Title, Company: r.Company,
		Location: r.Location, Salary: r.Salary, Link: r.Link,
	}
}

func (s *Server) listSaved(c *gin.Context) {
	items, total, err := s.uc.ListSaved(c.Request.Context(), profileOf(c).ID, pageOf(c))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, listResponse[domain.SavedVacancy]{Items: items, Total: total})
}

// saveVacancy is PUT /users/me/saved/:vacancy_id; the vacancy snapshot comes in the body.
func (s *Server) saveVacancy(c *gin.Context) {
	var req vacancyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	req.VacancyID = c.Param("vacancy_id")
	if err := s.uc.SaveVacancy(c.Request.Context(), profileOf(c).ID, req.ref()); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) unsaveVacancy(c *gin.Context) {
	if err := s.uc.UnsaveVacancy(c.Request.Context(), profileOf(c).ID, c.Param("vacancy_id")); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) listHistory(c *gin.Context) {
	items, total, err := s.uc.ListHistory(c.Request.Context(), profileOf(c).ID, pageOf(c))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, listResponse[domain.ViewedVacancy]{Items: items, Total: total})
}

func (s *Server) recordView(c *gin.Context) {
	var req vacancyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	if err := s.uc.RecordView(c.Request.Context(), profileOf(c).ID, req.ref()); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- telegram ---

type telegramCodeResponse struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) issueTelegramCode(c *gin.Context) {
	code, err := s.uc.IssueTelegramCode(c.Request.Context(), profileOf(c).ID)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, telegramCodeResponse{Code: code.Code, ExpiresAt: code.ExpiresAt})
}

func (s *Server) unlinkTelegram(c *gin.Context) {
	if err := s.uc.UnlinkTelegram(c.Request.Context(), profileOf(c).ID); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type confirmLinkRequest struct {
	Code   string `json:"code"`
	ChatID int64  `json:"chat_id"`
}

// confirmTelegramLink is called by the bot with the internal token after the user sent the code.
func (s *Server) confirmTelegramLink(c *gin.Context) {
	var req confirmLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" || req.ChatID == 0 {
		badRequest(c, "code and chat_id are required")
		return
	}
	if err := s.uc.ConfirmTelegramLink(c.Request.Context(), req.Code, req.ChatID); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
