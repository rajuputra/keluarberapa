package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/middleware"
)

// Check statuses reported by /ready.
const (
	statusOK          = "ok"
	statusError       = "error"
	statusSkipped     = "skipped"
	statusReady       = "ready"
	statusUnavailable = "unavailable"
)

// Pinger is anything that can confirm a dependency answers. *database.DB
// satisfies it; tests supply a stub.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthHandler serves the two operational endpoints.
type HealthHandler struct {
	app      config.App
	db       Pinger
	logger   *slog.Logger
	now      func() time.Time // injectable so tests get a fixed timestamp
	started  time.Time
	dbBudget time.Duration
}

// NewHealthHandler builds the handler. db may be nil, in which case /ready
// reports the database check as skipped rather than pretending it passed.
func NewHealthHandler(app config.App, db Pinger, logger *slog.Logger, dbBudget time.Duration) *HealthHandler {
	now := time.Now
	return &HealthHandler{
		app:      app,
		db:       db,
		logger:   logger,
		now:      now,
		started:  now(),
		dbBudget: dbBudget,
	}
}

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
	Env     string `json:"env"`
	Time    string `json:"time"`
	UptimeS int64  `json:"uptime_seconds"`
}

// Health serves GET /health: a liveness probe.
//
// It deliberately touches no dependency. A slow database must not cause an
// orchestrator to restart an otherwise healthy process; that is what /ready is
// for.
func (h *HealthHandler) Health(w http.ResponseWriter, _ *http.Request) {
	now := h.now()
	WriteJSON(w, h.logger, http.StatusOK, healthResponse{
		Status:  statusOK,
		Service: h.app.Name,
		Version: h.app.Version,
		Env:     h.app.Env,
		Time:    now.UTC().Format(time.RFC3339),
		UptimeS: int64(now.Sub(h.started).Seconds()),
	})
}

type checkResult struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type readyResponse struct {
	Status string                 `json:"status"`
	Checks map[string]checkResult `json:"checks"`
}

// readyErrorResponse keeps the standard {"error","message"} envelope and adds
// the per-dependency detail an operator needs to see which check failed.
type readyErrorResponse struct {
	Error   string                 `json:"error"`
	Message string                 `json:"message"`
	Checks  map[string]checkResult `json:"checks"`
}

// Ready serves GET /ready: a readiness probe that verifies PostgreSQL answers.
//
// It returns 200 when every dependency is usable and 503 otherwise, so a load
// balancer can drain traffic from an instance that has lost its database.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	// Ordered so the reported failure is deterministic as dependencies are added.
	ordered := []struct {
		name   string
		result checkResult
	}{
		{"database", h.checkDatabase(r)},
	}

	checks := make(map[string]checkResult, len(ordered))
	for _, c := range ordered {
		checks[c.name] = c.result
	}

	for _, c := range ordered {
		if c.result.Status != statusError {
			continue
		}
		// The body carries no driver detail: a connection error can name
		// internal hosts. The full error is in the log, keyed by request id.
		WriteJSON(w, h.logger, http.StatusServiceUnavailable, readyErrorResponse{
			Error:   CodeUnavailable,
			Message: "Dependency \"" + c.name + "\" is not available.",
			Checks:  checks,
		})
		return
	}

	WriteJSON(w, h.logger, http.StatusOK, readyResponse{Status: statusReady, Checks: checks})
}

func (h *HealthHandler) checkDatabase(r *http.Request) checkResult {
	if h.db == nil {
		// Only reachable in a build wired without a database, such as a test.
		return checkResult{Status: statusSkipped}
	}

	budget := h.dbBudget
	if budget <= 0 {
		budget = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()

	start := h.now()
	err := h.db.Ping(ctx)
	latency := h.now().Sub(start).Milliseconds()

	if err != nil {
		h.logger.ErrorContext(r.Context(), "readiness check failed",
			slog.String("request_id", middleware.RequestIDFrom(r.Context())),
			slog.String("dependency", "database"),
			slog.Int64("latency_ms", latency),
			slog.Any("error", err),
		)
		return checkResult{Status: statusError, LatencyMS: latency, Error: "unreachable"}
	}
	return checkResult{Status: statusOK, LatencyMS: latency}
}
