package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
	"github.com/rajuputra/keluarberapa/backend/internal/middleware"
)

// Deps are the collaborators the router needs. Later stages add the auth,
// transaction and dashboard services here.
type Deps struct {
	Config *config.Config
	Logger *slog.Logger
	// DB backs the /ready probe. It may be nil in tests.
	DB Pinger
}

// route is one registered endpoint. Keeping the table as data lets the router
// answer 405 for a known path used with the wrong method, without a second,
// drifting list of paths.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
}

// NewRouter builds the HTTP handler for the whole API.
//
// Stage 1 registers only the operational endpoints. Business routes live under
// /api/v1 and arrive with the stages that implement them.
func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	cfg := deps.Config

	health := NewHealthHandler(cfg.App, deps.DB, logger, cfg.Database.HealthTimeout)

	routes := []route{
		{http.MethodGet, "/health", health.Health},
		{http.MethodGet, "/ready", health.Ready},
	}

	mux := http.NewServeMux()
	methodsByPath := make(map[string][]string, len(routes))
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.path, rt.handler)
		methodsByPath[rt.path] = append(methodsByPath[rt.path], rt.method)
	}

	// The catch-all makes every unmatched request answer in the standard error
	// envelope instead of net/http's plain-text 404, so clients only ever parse
	// one error shape. It also shadows the mux's built-in 405, which is why the
	// method check is repeated here.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if methods, known := methodsByPath[r.URL.Path]; known {
			w.Header().Set("Allow", strings.Join(append(methods, http.MethodOptions), ", "))
			WriteError(w, logger, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
				"Method "+r.Method+" is not allowed for this endpoint.")
			return
		}
		notFound(w, logger)
	})

	// Outermost first: every later middleware and the handlers themselves can
	// rely on a request id being present, and the logger records the status
	// that recovery writes for a panic.
	return middleware.Chain(mux,
		middleware.RequestID(),
		middleware.RequestLogger(logger),
		middleware.Recovery(logger, panicResponse(logger)),
		middleware.CORS(cfg.HTTP.AllowedOrigins),
	)
}

// notFound answers unmatched routes.
//
// The message never distinguishes "no such route" from "not your resource":
// per ai_instructions.md section 3, a resource belonging to another user must be
// indistinguishable from one that does not exist.
func notFound(w http.ResponseWriter, logger *slog.Logger) {
	WriteError(w, logger, http.StatusNotFound, CodeNotFound, "The requested resource was not found.")
}

// panicResponse is handed to the recovery middleware so the 500 it writes uses
// the standard error envelope. The panic detail stays in the log.
func panicResponse(logger *slog.Logger) middleware.PanicHandler {
	return func(w http.ResponseWriter, _ *http.Request, _ any) {
		WriteError(w, logger, http.StatusInternalServerError, CodeInternalError,
			"An unexpected error occurred. Please try again.")
	}
}
