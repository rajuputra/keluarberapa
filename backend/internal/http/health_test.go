package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

// stubPinger stands in for the database in health-endpoint tests.
type stubPinger struct {
	err   error
	delay time.Duration
	calls int
}

func (s *stubPinger) Ping(ctx context.Context) error {
	s.calls++
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

func testApp() config.App {
	return config.App{
		Env:      config.EnvTest,
		Name:     "keluarberapa-api",
		Version:  "0.1.0-test",
		Timezone: config.DefaultTimezone,
	}
}

func newTestHealthHandler(db Pinger) *HealthHandler {
	return NewHealthHandler(testApp(), db, logging.Discard(), 200*time.Millisecond)
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	return out
}

func TestHealthReportsService(t *testing.T) {
	h := newTestHealthHandler(&stubPinger{})

	rec := httptest.NewRecorder()
	h.Health(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	body := decode[map[string]any](t, rec.Body.Bytes())
	if body["status"] != statusOK {
		t.Errorf("status = %v, want %q", body["status"], statusOK)
	}
	if body["service"] != "keluarberapa-api" {
		t.Errorf("service = %v, want keluarberapa-api", body["service"])
	}
	if body["version"] != "0.1.0-test" {
		t.Errorf("version = %v, want 0.1.0-test", body["version"])
	}
	if _, err := time.Parse(time.RFC3339, body["time"].(string)); err != nil {
		t.Errorf("time = %v, want RFC3339: %v", body["time"], err)
	}
}

// /health is a liveness probe: a sick database must not make the process look
// dead, or an orchestrator would restart a container that is working fine.
func TestHealthDoesNotTouchTheDatabase(t *testing.T) {
	db := &stubPinger{err: errors.New("connection refused")}
	h := newTestHealthHandler(db)

	rec := httptest.NewRecorder()
	h.Health(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d even with a failing database", rec.Code, http.StatusOK)
	}
	if db.calls != 0 {
		t.Errorf("database was pinged %d times, want 0", db.calls)
	}
}

func TestReadyWithHealthyDatabase(t *testing.T) {
	db := &stubPinger{}
	h := newTestHealthHandler(db)

	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body)
	}
	if db.calls != 1 {
		t.Errorf("database was pinged %d times, want 1", db.calls)
	}

	body := decode[readyResponse](t, rec.Body.Bytes())
	if body.Status != statusReady {
		t.Errorf("status = %q, want %q", body.Status, statusReady)
	}
	if got := body.Checks["database"].Status; got != statusOK {
		t.Errorf("checks.database.status = %q, want %q", got, statusOK)
	}
}

func TestReadyWithUnreachableDatabase(t *testing.T) {
	// A driver error can carry internal host names and, in the worst case, DSN
	// fragments. None of it may reach the client.
	db := &stubPinger{err: errors.New("dial tcp 10.0.3.14:5432: connect: connection refused")}
	h := newTestHealthHandler(db)

	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	raw := rec.Body.String()
	if strings.Contains(raw, "10.0.3.14") || strings.Contains(raw, "connection refused") {
		t.Errorf("response leaked driver detail: %s", raw)
	}

	body := decode[readyErrorResponse](t, rec.Body.Bytes())
	if body.Error != CodeUnavailable {
		t.Errorf("error = %q, want %q", body.Error, CodeUnavailable)
	}
	if body.Message == "" {
		t.Error("message should explain which dependency failed")
	}
	if got := body.Checks["database"].Status; got != statusError {
		t.Errorf("checks.database.status = %q, want %q", got, statusError)
	}
}

// A hung database must not hang the probe: the check is bounded by
// DATABASE_HEALTH_TIMEOUT and reports 503 instead.
func TestReadyBoundsASlowDatabase(t *testing.T) {
	h := NewHealthHandler(testApp(), &stubPinger{delay: time.Minute}, logging.Discard(), 30*time.Millisecond)

	start := time.Now()
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if elapsed > 5*time.Second {
		t.Errorf("probe took %v, want it bounded by the health timeout", elapsed)
	}
}

func TestReadyWithoutDatabaseReportsSkipped(t *testing.T) {
	h := newTestHealthHandler(nil)

	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := decode[readyResponse](t, rec.Body.Bytes())
	if got := body.Checks["database"].Status; got != statusSkipped {
		t.Errorf("checks.database.status = %q, want %q", got, statusSkipped)
	}
}
