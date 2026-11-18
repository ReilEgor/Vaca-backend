package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ReilEgor/Vaca/services/UserService/internal/auth"
	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
	authmocks "github.com/ReilEgor/Vaca/services/UserService/internal/mocks/auth"
	mocks "github.com/ReilEgor/Vaca/services/UserService/internal/mocks/domain"
)

const internalSecret = "s3cret"

type harness struct {
	server   *Server
	uc       *mocks.UserUsecase
	verifier *authmocks.Verifier
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &harness{uc: mocks.NewUserUsecase(t), verifier: authmocks.NewVerifier(t)}
	h.server = NewServer(h.uc, h.verifier, internalSecret)
	return h
}

// login makes the verifier accept the token and the use case authenticate the claims.
func (h *harness) login(claims domain.Claims) {
	h.verifier.On("Verify", mock.Anything, "tok").Return(claims, nil)
	h.uc.On("Authenticate", mock.Anything, claims).
		Return(&domain.Profile{ID: claims.Subject, Role: claims.Role, Status: domain.StatusActive}, nil)
}

func (h *harness) do(
	t *testing.T,
	method, path string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

var bearer = map[string]string{"Authorization": "Bearer tok"}

func TestAuthMiddleware(t *testing.T) {
	t.Run("missing token is 401", func(t *testing.T) {
		h := newHarness(t)

		rec := h.do(t, http.MethodGet, "/api/v1/users/me", nil, nil)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid token is 401", func(t *testing.T) {
		h := newHarness(t)
		h.verifier.On("Verify", mock.Anything, "tok").Return(domain.Claims{}, auth.ErrInvalidToken)

		rec := h.do(t, http.MethodGet, "/api/v1/users/me", nil, bearer)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.NotContains(t, rec.Body.String(), "invalid token:", "no technical details for the client")
	})

	t.Run("blocked account is 403", func(t *testing.T) {
		h := newHarness(t)
		claims := domain.Claims{Subject: "u1", Role: domain.RoleUser}
		h.verifier.On("Verify", mock.Anything, "tok").Return(claims, nil)
		h.uc.On("Authenticate", mock.Anything, claims).Return(nil, domain.ErrAccountBlocked)

		rec := h.do(t, http.MethodGet, "/api/v1/users/me", nil, bearer)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("unexpected failure is 500 without details", func(t *testing.T) {
		h := newHarness(t)
		claims := domain.Claims{Subject: "u1", Role: domain.RoleUser}
		h.verifier.On("Verify", mock.Anything, "tok").Return(claims, nil)
		h.uc.On("Authenticate", mock.Anything, claims).Return(nil, errors.New("pq: password authentication failed"))

		rec := h.do(t, http.MethodGet, "/api/v1/users/me", nil, bearer)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "pq:")
	})
}

func TestProfileEndpoints(t *testing.T) {
	user := domain.Claims{Subject: "u1", Role: domain.RoleUser}

	t.Run("get me", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("GetProfile", mock.Anything, "u1").Return(&domain.Profile{ID: "u1", Email: "a@b.c"}, nil)

		rec := h.do(t, http.MethodGet, "/api/v1/users/me", nil, bearer)

		require.Equal(t, http.StatusOK, rec.Code)
		var got domain.Profile
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, "a@b.c", got.Email)
	})

	t.Run("patch me passes only the given fields", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("UpdateProfile", mock.Anything, "u1", mock.MatchedBy(func(u domain.ProfileUpdate) bool {
			return u.Location != nil && *u.Location == "Kyiv" && u.DisplayName == nil && u.ExpectedSalary == nil
		})).Return(&domain.Profile{ID: "u1", Location: "Kyiv"}, nil)

		rec := h.do(t, http.MethodPatch, "/api/v1/users/me", map[string]any{"location": "Kyiv"}, bearer)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("validation error is 400", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("UpdateProfile", mock.Anything, "u1", mock.Anything).
			Return(nil, errors.Join(domain.ErrInvalidInput, errors.New("bad")))

		rec := h.do(t, http.MethodPatch, "/api/v1/users/me", map[string]any{"location": "x"}, bearer)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed body is 400", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)

		rec := h.do(t, http.MethodPatch, "/api/v1/users/me", "not an object", bearer)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("delete me is 204", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("DeleteAccount", mock.Anything, "u1").Return(nil)

		rec := h.do(t, http.MethodDelete, "/api/v1/users/me", nil, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestSubscriptionEndpoints(t *testing.T) {
	user := domain.Claims{Subject: "u1", Role: domain.RoleUser}

	t.Run("create defaults to active and returns 201", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("CreateSubscription", mock.Anything, "u1", mock.MatchedBy(func(s domain.Subscription) bool {
			return s.Keyword == "go" && s.IsActive
		})).Return(&domain.Subscription{ID: 1, Keyword: "go", IsActive: true}, nil)

		rec := h.do(t, http.MethodPost, "/api/v1/users/me/subscriptions", map[string]any{"keyword": "go"}, bearer)

		assert.Equal(t, http.StatusCreated, rec.Code)
	})

	t.Run("plan limit is 409", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("CreateSubscription", mock.Anything, "u1", mock.Anything).Return(nil, domain.ErrSubscriptionLimit)

		rec := h.do(t, http.MethodPost, "/api/v1/users/me/subscriptions", map[string]any{"keyword": "go"}, bearer)

		assert.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("bad id is 400", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)

		rec := h.do(t, http.MethodDelete, "/api/v1/users/me/subscriptions/abc", nil, bearer)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("deleting a missing subscription is 404", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("DeleteSubscription", mock.Anything, "u1", int64(9)).Return(domain.ErrSubscriptionNotFound)

		rec := h.do(t, http.MethodDelete, "/api/v1/users/me/subscriptions/9", nil, bearer)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestSavedAndHistoryEndpoints(t *testing.T) {
	user := domain.Claims{Subject: "u1", Role: domain.RoleUser}

	t.Run("put saved takes the id from the path", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("SaveVacancy", mock.Anything, "u1", mock.MatchedBy(func(v domain.VacancyRef) bool {
			return v.VacancyID == "v1" && v.Title == "Go dev"
		})).Return(nil)

		rec := h.do(t, http.MethodPut, "/api/v1/users/me/saved/v1",
			map[string]any{"vacancy_id": "ignored", "title": "Go dev"}, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("list saved passes pagination and returns the total", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("ListSaved", mock.Anything, "u1", domain.Page{Limit: 5, Offset: 10}).
			Return([]domain.SavedVacancy{{VacancyRef: domain.VacancyRef{VacancyID: "v1"}}}, int64(11), nil)

		rec := h.do(t, http.MethodGet, "/api/v1/users/me/saved?limit=5&offset=10", nil, bearer)

		require.Equal(t, http.StatusOK, rec.Code)
		var got struct {
			Items []domain.SavedVacancy `json:"items"`
			Total int64                 `json:"total"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Len(t, got.Items, 1)
		assert.EqualValues(t, 11, got.Total)
	})

	t.Run("unsave of something not saved is 404", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("UnsaveVacancy", mock.Anything, "u1", "v1").Return(domain.ErrNotFound)

		rec := h.do(t, http.MethodDelete, "/api/v1/users/me/saved/v1", nil, bearer)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("record view", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)
		h.uc.On("RecordView", mock.Anything, "u1", mock.Anything).Return(nil)

		rec := h.do(t, http.MethodPost, "/api/v1/users/me/history", map[string]any{"vacancy_id": "v1"}, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestAdminEndpoints(t *testing.T) {
	user := domain.Claims{Subject: "u1", Role: domain.RoleUser}
	manager := domain.Claims{Subject: "m1", Role: domain.RoleManager}
	admin := domain.Claims{Subject: "a1", Role: domain.RoleAdmin}

	t.Run("a plain user is forbidden from the admin area", func(t *testing.T) {
		h := newHarness(t)
		h.login(user)

		rec := h.do(t, http.MethodGet, "/api/v1/admin/users", nil, bearer)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("a manager lists users with filters", func(t *testing.T) {
		h := newHarness(t)
		h.login(manager)
		h.uc.On("ListUsers", mock.Anything, domain.RoleManager, mock.MatchedBy(func(f domain.UserFilter) bool {
			return f.Search == "ann" && f.Status == domain.StatusBlocked && f.Limit == 3
		})).Return([]domain.Profile{{ID: "u1"}}, int64(1), nil)

		rec := h.do(t, http.MethodGet, "/api/v1/admin/users?search=ann&status=blocked&limit=3", nil, bearer)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("a manager may block", func(t *testing.T) {
		h := newHarness(t)
		h.login(manager)
		h.uc.On("BlockUser", mock.Anything, manager, "u1").Return(nil)

		rec := h.do(t, http.MethodPost, "/api/v1/admin/users/u1/block", nil, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("a manager cannot unblock or delete", func(t *testing.T) {
		h := newHarness(t)
		h.login(manager)

		assert.Equal(
			t,
			http.StatusForbidden,
			h.do(t, http.MethodPost, "/api/v1/admin/users/u1/unblock", nil, bearer).Code,
		)
		assert.Equal(t, http.StatusForbidden, h.do(t, http.MethodDelete, "/api/v1/admin/users/u1", nil, bearer).Code)
	})

	t.Run("an admin deletes", func(t *testing.T) {
		h := newHarness(t)
		h.login(admin)
		h.uc.On("DeleteUser", mock.Anything, admin, "u1").Return(nil)

		rec := h.do(t, http.MethodDelete, "/api/v1/admin/users/u1", nil, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("hierarchy violation from the use case is 403", func(t *testing.T) {
		h := newHarness(t)
		h.login(manager)
		h.uc.On("BlockUser", mock.Anything, manager, "m2").Return(domain.ErrForbidden)

		rec := h.do(t, http.MethodPost, "/api/v1/admin/users/m2/block", nil, bearer)

		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("set plan", func(t *testing.T) {
		h := newHarness(t)
		h.login(manager)
		h.uc.On("SetPlan", mock.Anything, manager, "u1", domain.PlanPro, (*time.Time)(nil)).Return(nil)

		rec := h.do(t, http.MethodPut, "/api/v1/admin/users/u1/plan", map[string]any{"plan": "pro"}, bearer)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestInternalTelegramLink(t *testing.T) {
	body := map[string]any{"code": "ABCD2345", "chat_id": 42}

	t.Run("wrong token is 401", func(t *testing.T) {
		h := newHarness(t)

		rec := h.do(t, http.MethodPost, "/internal/telegram/link", body, map[string]string{"X-Internal-Token": "nope"})

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("an empty secret keeps the route closed", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		uc := mocks.NewUserUsecase(t)
		srv := NewServer(uc, authmocks.NewVerifier(t), "")
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/internal/telegram/link",
			bytes.NewBufferString(`{}`))
		rec := httptest.NewRecorder()

		srv.Handler().ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("valid code links the chat", func(t *testing.T) {
		h := newHarness(t)
		h.uc.On("ConfirmTelegramLink", mock.Anything, "ABCD2345", int64(42)).Return(nil)

		rec := h.do(
			t,
			http.MethodPost,
			"/internal/telegram/link",
			body,
			map[string]string{"X-Internal-Token": internalSecret},
		)

		assert.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("invalid code is 400", func(t *testing.T) {
		h := newHarness(t)
		h.uc.On("ConfirmTelegramLink", mock.Anything, "ABCD2345", int64(42)).Return(domain.ErrInvalidTelegramCode)

		rec := h.do(
			t,
			http.MethodPost,
			"/internal/telegram/link",
			body,
			map[string]string{"X-Internal-Token": internalSecret},
		)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing fields are 400", func(t *testing.T) {
		h := newHarness(t)

		rec := h.do(t, http.MethodPost, "/internal/telegram/link", map[string]any{"code": "X"},
			map[string]string{"X-Internal-Token": internalSecret})

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHealth(t *testing.T) {
	h := newHarness(t)

	rec := h.do(t, http.MethodGet, "/health", nil, nil)

	assert.Equal(t, http.StatusOK, rec.Code)
}
