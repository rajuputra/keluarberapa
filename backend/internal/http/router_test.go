package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
	"github.com/rajuputra/keluarberapa/backend/internal/middleware"
)

func testConfig() *config.Config {
	return &config.Config{
		App: testApp(),
		HTTP: config.HTTP{
			Host:           "127.0.0.1",
			Port:           8080,
			AllowedOrigins: []string{"http://localhost:4321"},
		},
		Database: config.Database{HealthTimeout: 200 * time.Millisecond},
	}
}

func newTestRouter(db Pinger) http.Handler {
	return NewRouter(Deps{Config: testConfig(), Logger: logging.Discard(), DB: db})
}

func TestRouterServesHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&stubPinger{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("GET /health = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body)
	}
}

func TestRouterServesReady(t *testing.T) {
	tests := []struct {
		name string
		db   Pinger
		want int
	}{
		{"healthy database", &stubPinger{}, http.StatusOK},
		{"failing database", &stubPinger{err: errors.New("down")}, http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestRouter(tt.db).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

			if rec.Code != tt.want {
				t.Errorf("GET /ready = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

func TestRouterAttachesRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&stubPinger{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Header().Get(middleware.RequestIDHeader) == "" {
		t.Errorf("%s header should be set on every response", middleware.RequestIDHeader)
	}
}

// Every error, including a 404 on a route that does not exist, uses the single
// envelope from architecture.md section 5 so the dashboard parses one shape.
func TestRouterUnknownRouteUsesTheStandardErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&stubPinger{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	body := decode[map[string]any](t, rec.Body.Bytes())
	if body["error"] != CodeNotFound {
		t.Errorf("error = %v, want %q", body["error"], CodeNotFound)
	}
	if body["message"] == "" {
		t.Error("message should not be empty")
	}
	// The contract is exactly two fields; extra keys would be a silent change.
	if len(body) != 2 {
		t.Errorf("error body has %d fields (%v), want exactly error and message", len(body), body)
	}
}

func TestRouterWrongMethodOnKnownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestRouter(&stubPinger{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /health = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); !strings.Contains(got, http.MethodGet) {
		t.Errorf("Allow = %q, want it to include GET", got)
	}

	body := decode[map[string]any](t, rec.Body.Bytes())
	if body["error"] != CodeMethodNotAllowed {
		t.Errorf("error = %v, want %q", body["error"], CodeMethodNotAllowed)
	}
}

func TestRouterAppliesCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://localhost:4321")
	rec := httptest.NewRecorder()
	newTestRouter(&stubPinger{}).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4321" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the configured origin", got)
	}
}

// A panic anywhere behind the router must come back as the standard 500
// envelope, never as a dropped connection or a Go stack trace.
func TestRouterRecoversFromPanic(t *testing.T) {
	cfg := testConfig()
	logger := logging.Discard()

	handler := middleware.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom: internal detail")
		}),
		middleware.RequestID(),
		middleware.RequestLogger(logger),
		middleware.Recovery(logger, panicResponse(logger)),
		middleware.CORS(cfg.HTTP.AllowedOrigins),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if strings.Contains(rec.Body.String(), "internal detail") {
		t.Errorf("response leaked the panic detail: %s", rec.Body)
	}

	body := decode[map[string]any](t, rec.Body.Bytes())
	if body["error"] != CodeInternalError {
		t.Errorf("error = %v, want %q", body["error"], CodeInternalError)
	}
	if len(body) != 2 {
		t.Errorf("error body has %d fields (%v), want exactly error and message", len(body), body)
	}
}
