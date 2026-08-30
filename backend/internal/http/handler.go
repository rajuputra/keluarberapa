package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rajuputra/keluarberapa/backend/internal/auth"
	"github.com/rajuputra/keluarberapa/backend/internal/user"
)

// AuthConfig holds the dependencies for the auth middleware.
type AuthConfig struct {
	// Issuer verifies access tokens. Required.
	Issuer auth.AccessTokenIssuer
	// Logger for structured logging. Defaults to discarding.
	Logger *slog.Logger
}

// authContextKey is the type used for context keys in this package.
type authContextKey string

const (
	// UserIDKey is the context key for the authenticated user's ID.
	UserIDKey authContextKey = "user_id"
)

// Auth returns middleware that validates the Authorization header.
//
// On success, the user ID is stored in the request context under UserIDKey.
// On failure, a standard error response is written and the chain stops.
func Auth(cfg AuthConfig) func(http.Handler) http.Handler {
	if cfg.Issuer == nil {
		panic("httpapi: Auth requires an AccessTokenIssuer")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				WriteError(w, cfg.Logger, http.StatusUnauthorized, "unauthorized", "Authorization header is required.")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				WriteError(w, cfg.Logger, http.StatusUnauthorized, "unauthorized", "Authorization header must use Bearer scheme.")
				return
			}

			claims, err := cfg.Issuer.ParseAccessToken(parts[1])
			if err != nil {
				cfg.Logger.Debug("access token validation failed", slog.Any("error", err))
				WriteError(w, cfg.Logger, http.StatusUnauthorized, "unauthorized", "Access token is invalid or expired.")
				return
			}

			userID, err := auth.UserIDFromClaims(claims)
			if err != nil {
				cfg.Logger.Error("access token subject is not a valid UUID", slog.Any("error", err))
				WriteError(w, cfg.Logger, http.StatusUnauthorized, "unauthorized", "Access token is invalid.")
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, userID.String())
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext extracts the user ID from the request context.
//
// Returns empty string and false if the context has no user ID (i.e., the Auth
// middleware was not applied or failed).
func UserIDFromContext(ctx context.Context) (string, bool) {
	val := ctx.Value(UserIDKey)
	if val == nil {
		return "", false
	}
	id, ok := val.(string)
	return id, ok
}

// RequireUserID is a helper that extracts the user ID from context or panics.
//
// It is intended for use in handlers that are already protected by the Auth
// middleware, so the ID is guaranteed to be present. The panic is caught by
// the Recovery middleware and logged.
func RequireUserID(ctx context.Context) string {
	id, ok := UserIDFromContext(ctx)
	if !ok {
		panic("httpapi: user ID not in context; Auth middleware missing")
	}
	return id
}

// AuthHandlerConfig holds the dependencies for auth handlers.
type AuthHandlerConfig struct {
	Service *auth.Service
	Logger  *slog.Logger
}

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	service *auth.Service
	logger  *slog.Logger
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(cfg AuthHandlerConfig) *AuthHandler {
	if cfg.Service == nil {
		panic("auth: Service is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &AuthHandler{service: cfg.Service, logger: cfg.Logger}
}

// RegisterRoutes registers the auth endpoints on the given mux.
func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.Register)
	mux.HandleFunc("POST /api/v1/auth/login", h.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.Logout)
}

// RegisterRequest is the request body for POST /api/v1/auth/register.
type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Timezone string `json:"timezone,omitempty"`
}

// RegisterResponse is the response body for successful registration.
type RegisterResponse struct {
	User user.Profile `json:"user"`
}

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	profile, err := h.service.Register(r.Context(), auth.RegisterInput{
		Name:     req.Name,
		Email:    req.Email,
		Password: req.Password,
		Timezone: req.Timezone,
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	WriteJSON(w, h.logger, http.StatusCreated, RegisterResponse{User: *profile})
}

// LoginRequest is the request body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is the response body for successful login.
type LoginResponse struct {
	User                  user.Profile `json:"user"`
	AccessToken           string       `json:"access_token"`
	AccessTokenExpiresAt  string       `json:"access_token_expires_at"`
	RefreshToken          string       `json:"refresh_token"`
	RefreshTokenExpiresAt string       `json:"refresh_token_expires_at"`
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	session, err := h.service.Login(r.Context(), auth.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, LoginResponse{
		User:                  session.User,
		AccessToken:           session.AccessToken,
		AccessTokenExpiresAt:  session.AccessTokenExpiresAt.Format(time.RFC3339),
		RefreshToken:          session.RefreshToken,
		RefreshTokenExpiresAt: session.RefreshTokenExpiresAt.Format(time.RFC3339),
	})
}

// RefreshRequest is the request body for POST /api/v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// RefreshResponse is the response body for successful refresh.
type RefreshResponse struct {
	User                  user.Profile `json:"user"`
	AccessToken           string       `json:"access_token"`
	AccessTokenExpiresAt  string       `json:"access_token_expires_at"`
	RefreshToken          string       `json:"refresh_token"`
	RefreshTokenExpiresAt string       `json:"refresh_token_expires_at"`
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	session, err := h.service.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, RefreshResponse{
		User:                  session.User,
		AccessToken:           session.AccessToken,
		AccessTokenExpiresAt:  session.AccessTokenExpiresAt.Format(time.RFC3339),
		RefreshToken:          session.RefreshToken,
		RefreshTokenExpiresAt: session.RefreshTokenExpiresAt.Format(time.RFC3339),
	})
}

// LogoutRequest is the request body for POST /api/v1/auth/logout.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req LogoutRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	err := h.service.Logout(r.Context(), req.RefreshToken)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleServiceError maps service errors to HTTP responses.
func (h *AuthHandler) handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, user.ErrEmailTaken):
		WriteError(w, h.logger, http.StatusConflict, "conflict", "Email is already registered.")
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(w, h.logger, http.StatusUnauthorized, "unauthorized", "Email or password is incorrect.")
	case errors.Is(err, auth.ErrAccountNotActive):
		WriteError(w, h.logger, http.StatusForbidden, "forbidden", "Account is not active.")
	case errors.Is(err, auth.ErrInvalidRefreshToken):
		WriteError(w, h.logger, http.StatusUnauthorized, "unauthorized", "Refresh token is invalid or expired.")
	case errors.Is(err, auth.ErrMissingRefreshToken):
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", "Refresh token is required.")
	case errors.Is(err, user.ErrNotFound):
		WriteError(w, h.logger, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, user.ErrNoChanges):
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", "No fields to update.")
	case errors.Is(err, user.ErrNameRequired), errors.Is(err, user.ErrNameTooLong),
		errors.Is(err, user.ErrEmailRequired), errors.Is(err, user.ErrEmailInvalid),
		errors.Is(err, user.ErrEmailTooLong), errors.Is(err, user.ErrTimezoneInvalid),
		errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordBlank):
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
	default:
		h.logger.Error("auth handler error", slog.Any("error", err))
		WriteError(w, h.logger, http.StatusInternalServerError, "internal_error", "An unexpected error occurred. Please try again.")
	}
}

// UserHandlerConfig holds the dependencies for user handlers.
type UserHandlerConfig struct {
	Service      *user.Service
	AccessTokens auth.AccessTokenIssuer
	Logger       *slog.Logger
}

// UserHandler handles user profile endpoints.
type UserHandler struct {
	service      *user.Service
	accessTokens auth.AccessTokenIssuer
	logger       *slog.Logger
}

// NewUserHandler creates a new user handler.
func NewUserHandler(cfg UserHandlerConfig) *UserHandler {
	if cfg.Service == nil {
		panic("user: Service is required")
	}
	if cfg.AccessTokens == nil {
		panic("user: AccessTokens issuer is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &UserHandler{service: cfg.Service, accessTokens: cfg.AccessTokens, logger: cfg.Logger}
}

// RegisterRoutes registers the user endpoints on the given mux.
func (h *UserHandler) RegisterRoutes(mux *http.ServeMux) {
	authMiddleware := Auth(AuthConfig{
		Issuer: h.accessTokens,
		Logger: h.logger,
	})
	mux.Handle("GET /api/v1/me", authMiddleware(http.HandlerFunc(h.Profile)))
	mux.Handle("PATCH /api/v1/me", authMiddleware(http.HandlerFunc(h.UpdateProfile)))
}

// ProfileResponse is the response body for GET /api/v1/me.
type ProfileResponse struct {
	User user.Profile `json:"user"`
}

// Profile handles GET /api/v1/me.
func (h *UserHandler) Profile(w http.ResponseWriter, r *http.Request) {
	userIDStr := RequireUserID(r.Context())
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Error("invalid user ID in context", slog.String("user_id", userIDStr), slog.Any("error", err))
		WriteError(w, h.logger, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	profile, err := h.service.Profile(r.Context(), userID)
	if err != nil {
		h.handleUserServiceError(w, err)
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, ProfileResponse{User: *profile})
}

// UpdateProfileRequest is the request body for PATCH /api/v1/me.
type UpdateProfileRequest struct {
	Name     *string `json:"name,omitempty"`
	Timezone *string `json:"timezone,omitempty"`
}

// UpdateProfile handles PATCH /api/v1/me.
func (h *UserHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req UpdateProfileRequest
	if err := DecodeJSON(r, &req); err != nil {
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	userIDStr := RequireUserID(r.Context())
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		h.logger.Error("invalid user ID in context", slog.String("user_id", userIDStr), slog.Any("error", err))
		WriteError(w, h.logger, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	profile, err := h.service.UpdateProfile(r.Context(), userID, user.UpdateProfileInput{
		Name:     req.Name,
		Timezone: req.Timezone,
	})
	if err != nil {
		h.handleUserServiceError(w, err)
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, ProfileResponse{User: *profile})
}

// handleUserServiceError maps user service errors to HTTP responses.
func (h *UserHandler) handleUserServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, user.ErrNotFound):
		WriteError(w, h.logger, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, user.ErrNoChanges):
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", "No fields to update.")
	case errors.Is(err, user.ErrNameRequired), errors.Is(err, user.ErrNameTooLong),
		errors.Is(err, user.ErrTimezoneInvalid):
		WriteError(w, h.logger, http.StatusBadRequest, "bad_request", err.Error())
	default:
		h.logger.Error("user handler error", slog.Any("error", err))
		WriteError(w, h.logger, http.StatusInternalServerError, "internal_error", "An unexpected error occurred. Please try again.")
	}
}
