package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Serve runs srv on ln until ctx is cancelled, then drains in-flight requests.
//
// Cancelling ctx (which main wires to SIGINT and SIGTERM) stops the listener and
// gives running handlers up to shutdownTimeout to finish, so a deploy does not
// cut a request in half. Serve returns only once the server has stopped, which
// is what lets the caller close the database pool safely afterwards.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	// Buffered so the goroutine can always finish, even if nobody reads.
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", slog.String("addr", ln.Addr().String()))
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		// The server stopped on its own; there is nothing to drain.
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received, draining connections",
		slog.Duration("timeout", shutdownTimeout))

	// Detached from ctx, which is already cancelled, so the grace period is
	// actually honoured.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		// Requests outlasted the grace period. Close the rest rather than
		// hanging the process forever.
		logger.Error("graceful shutdown timed out, closing remaining connections",
			slog.Any("error", shutdownErr))
		if err := srv.Close(); err != nil {
			logger.Error("close server", slog.Any("error", err))
		}
	}

	// Wait for Serve to return before reporting, so callers may release
	// resources that handlers were still using.
	if err := <-serveErr; err != nil {
		return fmt.Errorf("http server: %w", err)
	}
	if shutdownErr != nil {
		return fmt.Errorf("graceful shutdown: %w", shutdownErr)
	}

	logger.Info("shutdown complete")
	return nil
}
