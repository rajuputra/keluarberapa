package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// preflightMaxAge is how long a browser may cache a preflight result.
const preflightMaxAge = 12 * time.Hour

// allowedMethods and allowedHeaders cover what the Astro dashboard needs.
var (
	allowedMethods = []string{
		http.MethodGet, http.MethodPost, http.MethodPatch,
		http.MethodDelete, http.MethodOptions,
	}
	allowedHeaders = []string{"Authorization", "Content-Type", RequestIDHeader}
)

// CORS answers browser preflights and adds the response headers the dashboard
// needs.
//
// Origins are matched exactly against CORS_ALLOWED_ORIGINS. "*" is never echoed
// back together with credentials, because that combination would let any site
// replay a logged-in user's cookies.
func CORS(allowedOrigins []string) Middleware {
	allowed := make(map[string]bool, len(allowedOrigins))
	wildcard := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			wildcard = true
			continue
		}
		allowed[strings.TrimRight(origin, "/")] = true
	}

	methods := strings.Join(allowedMethods, ", ")
	headers := strings.Join(allowedHeaders, ", ")
	maxAge := strconv.Itoa(int(preflightMaxAge.Seconds()))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimRight(r.Header.Get("Origin"), "/")
			permitted := origin != "" && (wildcard || allowed[origin])

			if permitted {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Expose-Headers", RequestIDHeader)
				// Caches must not serve one origin's response to another.
				w.Header().Add("Vary", "Origin")
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Add("Vary", "Access-Control-Request-Method")
				w.Header().Add("Vary", "Access-Control-Request-Headers")
				if permitted {
					w.Header().Set("Access-Control-Allow-Methods", methods)
					w.Header().Set("Access-Control-Allow-Headers", headers)
					w.Header().Set("Access-Control-Max-Age", maxAge)
				}
				// A disallowed origin gets 204 with no CORS headers; the
				// browser then blocks the real request.
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
