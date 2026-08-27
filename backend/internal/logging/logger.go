// Package logging builds the process-wide structured logger.
//
// Every log line carries the service, version and environment so that lines
// from several deployments remain distinguishable once shipped to a log store.
package logging

import (
	"io"
	"log/slog"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
)

// New returns a structured logger configured from cfg, writing to w.
//
// LOG_FORMAT=json (the default) is meant for deployed environments;
// LOG_FORMAT=text is easier to read while developing.
func New(cfg config.App, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler).With(
		slog.String("service", cfg.Name),
		slog.String("version", cfg.Version),
		slog.String("env", cfg.Env),
	)
}

// Discard returns a logger that drops everything. Useful in tests that need a
// non-nil logger but no output.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
