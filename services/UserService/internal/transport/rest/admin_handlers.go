package rest

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

func (s *Server) listUsers(c *gin.Context) {
	f := domain.UserFilter{
		Page:   pageOf(c),
		Search: c.Query("search"),
		Role:   domain.Role(c.Query("role")),
		Status: domain.Status(c.Query("status")),
	}
	users, total, err := s.uc.ListUsers(c.Request.Context(), claimsOf(c).Role, f)
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, listResponse[domain.Profile]{Items: users, Total: total})
}

func (s *Server) getUser(c *gin.Context) {
	p, err := s.uc.GetUser(c.Request.Context(), claimsOf(c).Role, c.Param("id"))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

func (s *Server) listUserActivity(c *gin.Context) {
	items, total, err := s.uc.ListActivity(c.Request.Context(), claimsOf(c).Role, c.Param("id"), pageOf(c))
	if err != nil {
		s.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, listResponse[domain.Activity]{Items: items, Total: total})
}

type setPlanRequest struct {
	Plan      domain.Plan `json:"plan"`
	ExpiresAt *time.Time  `json:"expires_at"`
}

func (s *Server) setPlan(c *gin.Context) {
	var req setPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	if err := s.uc.SetPlan(c.Request.Context(), claimsOf(c), c.Param("id"), req.Plan, req.ExpiresAt); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) blockUser(c *gin.Context) {
	if err := s.uc.BlockUser(c.Request.Context(), claimsOf(c), c.Param("id")); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) unblockUser(c *gin.Context) {
	if err := s.uc.UnblockUser(c.Request.Context(), claimsOf(c), c.Param("id")); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) deleteUser(c *gin.Context) {
	if err := s.uc.DeleteUser(c.Request.Context(), claimsOf(c), c.Param("id")); err != nil {
		s.respondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
