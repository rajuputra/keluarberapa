package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
)

// RequestIDHeader is the header read from the client and echoed on the response.
const RequestIDHeader = "X-Request-Id"

// requestIDKey is unexported so no other package can plant a value under it.
type requestIDKey struct{}

// safeRequestID limits what an inbound id may contain. A client-supplied value
// ends up in log lines and response headers, so it must not be able to carry
// control characters or unbounded length.
var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

// RequestID attaches a request id to the context and to the response, reusing a
// well-formed inbound id so a trace survives across services.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !safeRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
		})
	}
}

// WithRequestID stores id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFrom returns the request id stored in ctx, or "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func newRequestID() string {
	var buf [16]byte
	// crypto/rand.Read never returns an error as of Go 1.24.
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
