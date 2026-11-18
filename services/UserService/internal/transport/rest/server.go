// Package rest is the HTTP transport of the UserService.
package rest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ReilEgor/Vaca/services/UserService/internal/auth"
	"github.com/ReilEgor/Vaca/services/UserService/internal/domain"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// Server serves the REST API of the service.
type Server struct {
	engine *gin.Engine
	uc     domain.UserUsecase
	logger *slog.Logger
}

// NewServer wires the routes. internalToken guards the service-to-service routes.
func NewServer(uc domain.UserUsecase, verifier auth.Verifier, internalToken string) *Server {
	logger := slog.With(slog.String("component", "rest"))
	engine := gin.New()
	engine.Use(gin.Recovery(), requestLogger(logger))

	s := &Server{engine: engine, uc: uc, logger: logger}
	s.routes(verifier, internalToken)
	return s
}

// Handler exposes the router, mainly for tests.
func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes(verifier auth.Verifier, internalToken string) {
	s.engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	s.engine.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	api := s.engine.Group("/api/v1")
	authed := api.Group("", authenticate(verifier, s.uc))

	me := authed.Group("/users/me")
	{
		me.GET("", s.getMe)
		me.PATCH("", s.updateMe)
		me.DELETE("", s.deleteMe)

		me.GET("/notification-settings", s.getSettings)
		me.PUT("/notification-settings", s.putSettings)

		me.GET("/subscriptions", s.listSubscriptions)
		me.POST("/subscriptions", s.createSubscription)
		me.PATCH("/subscriptions/:id", s.updateSubscription)
		me.DELETE("/subscriptions/:id", s.deleteSubscription)

		me.GET("/saved", s.listSaved)
		me.PUT("/saved/:vacancy_id", s.saveVacancy)
		me.DELETE("/saved/:vacancy_id", s.unsaveVacancy)

		me.GET("/history", s.listHistory)
		me.POST("/history", s.recordView)

		me.POST("/telegram/code", s.issueTelegramCode)
		me.DELETE("/telegram", s.unlinkTelegram)
	}

	admin := authed.Group("/admin", requireRole(domain.RoleManager))
	{
		admin.GET("/users", s.listUsers)
		admin.GET("/users/:id", s.getUser)
		admin.GET("/users/:id/activity", s.listUserActivity)
		admin.PUT("/users/:id/plan", s.setPlan)
		admin.POST("/users/:id/block", s.blockUser)
		admin.POST("/users/:id/unblock", requireRole(domain.RoleAdmin), s.unblockUser)
		admin.DELETE("/users/:id", requireRole(domain.RoleAdmin), s.deleteUser)
	}

	internal := s.engine.Group("/internal", requireInternalToken(internalToken))
	internal.POST("/telegram/link", s.confirmTelegramLink)
}

// Run serves HTTP until the context is canceled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, port string) error {
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           s.engine,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
