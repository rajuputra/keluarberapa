package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"time"
)

// noiseFreePaths are polled constantly by orchestrators. They are logged at
// debug level so an uptime probe cannot drown out real traffic.
var noiseFreePaths = map[string]bool{
	"/health": true,
	"/ready":  true,
}

// RequestLogger logs one structured line per request once the handler returns.
//
// Only metadata is logged. Request bodies are never logged: they can contain
// passwords and WhatsApp message content.
func RequestLogger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := newResponseRecorder(w)

			next.ServeHTTP(rec, r)

			attrs := []slog.Attr{
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("remote_ip", clientIP(r)),
				slog.String("user_agent", r.UserAgent()),
			}

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			case noiseFreePaths[r.URL.Path]:
				level = slog.LevelDebug
			}

			logger.LogAttrs(r.Context(), level, "http request", attrs...)
		})
	}
}

// clientIP returns the peer address. Proxy headers such as X-Forwarded-For are
// deliberately ignored: they are client-controlled and this service does not
// yet know which proxies it may trust.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
