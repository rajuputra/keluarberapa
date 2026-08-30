package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/rajuputra/keluarberapa/backend/internal/auth"
	"github.com/rajuputra/keluarberapa/backend/internal/user"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// fakeIssuer is a minimal AccessTokenIssuer for testing.
type fakeIssuer struct {
	userID string
	err    error
}

func (f *fakeIssuer) ParseAccessToken(tokenString string) (*auth.AccessTokenClaims, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.userID == "" {
		return nil, auth.ErrInvalidAccessToken
	}
	return &auth.AccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: f.userID,
		},
	}, nil
}

func (f *fakeIssuer) IssueAccessToken(u user.User, now time.Time) (auth.AccessToken, error) {
	return auth.AccessToken{}, nil
}

// ---------------------------------------------------------------------------
// Auth middleware tests
// ---------------------------------------------------------------------------

func TestAuthRejectsMissingHeader(t *testing.T) {
	issuer := &fakeIssuer{}
	handler := Auth(AuthConfig{Issuer: issuer, Logger: discardLogger()})(okHandler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Errorf("error = %q, want unauthorized", body["error"])
	}
}

func TestAuthRejectsInvalidScheme(t *testing.T) {
	issuer := &fakeIssuer{}
	handler := Auth(AuthConfig{Issuer: issuer, Logger: discardLogger()})(okHandler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthRejectsInvalidToken(t *testing.T) {
	issuer := &fakeIssuer{err: errors.New("invalid")}
	handler := Auth(AuthConfig{Issuer: issuer, Logger: discardLogger()})(okHandler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthAcceptsValidToken(t *testing.T) {
	userID := "550e8400-e29b-41d4-a716-446655440000"
	issuer := &fakeIssuer{userID: userID}
	var seen string

	handler := Auth(AuthConfig{Issuer: issuer, Logger: discardLogger()})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := UserIDFromContext(r.Context())
			if !ok {
				t.Error("user ID not in context")
			}
			seen = id
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if seen != userID {
		t.Errorf("user ID in context = %q, want %q", seen, userID)
	}
}

func TestUserIDFromContextReturnsFalseWhenMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	id, ok := UserIDFromContext(req.Context())
	if ok {
		t.Errorf("UserIDFromContext on bare context = %q, want empty", id)
	}
	if id != "" {
		t.Errorf("id = %q, want empty", id)
	}
}

func TestRequireUserIDPanicsWhenMissing(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("RequireUserID did not panic on empty context")
		}
	}()
	_ = RequireUserID(httptest.NewRequest(http.MethodGet, "/", nil).Context())
}
