package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// PanicHandler writes the response for a recovered panic. The router injects
// one so this package does not have to import the handler package.
type PanicHandler func(w http.ResponseWriter, r *http.Request, recovered any)

// Recovery turns a panic into a logged 500 instead of a dropped connection.
//
// The panic value and stack trace go to the log only. They can contain internal
// detail, so onPanic is expected to write a generic message to the client.
func Recovery(logger *slog.Logger, onPanic PanicHandler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := newResponseRecorder(w)

			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				// net/http uses this sentinel to abort a connection on purpose.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				logger.ErrorContext(r.Context(), "panic recovered",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)

				// If the handler already started writing, the status line is
				// gone and anything more would corrupt the response body.
				if rec.wroteHeader {
					return
				}
				if onPanic != nil {
					onPanic(rec, r, recovered)
					return
				}
				http.Error(rec, "internal server error", http.StatusInternalServerError)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}
