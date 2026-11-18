package domain

import "errors"

// Errors returned by the repositories and the use case; the transport maps them to HTTP statuses.
var (
	ErrUserNotFound         = errors.New("user not found")
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrNotFound             = errors.New("not found")

	ErrAccountBlocked = errors.New("account is blocked")
	ErrForbidden      = errors.New("forbidden")

	ErrInvalidInput        = errors.New("invalid input")
	ErrSubscriptionLimit   = errors.New("subscription limit of the plan is reached")
	ErrInvalidTelegramCode = errors.New("telegram code is invalid or expired")
)
