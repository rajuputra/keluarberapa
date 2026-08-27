// Package httpapi contains the HTTP transport layer: the router, the shared
// response helpers and the operational endpoints.
//
// The package lives in internal/http (architecture.md section 3) but is named
// httpapi so that files here can use net/http without an import alias.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error codes used by the standard error envelope. The set grows with each
// stage; every code is documented in docs/openapi.yaml.
const (
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternalError    = "internal_error"
	CodeUnavailable      = "service_unavailable"
)

// ErrorResponse is the standard API error format from architecture.md section 5:
// {"error": "...", "message": "..."}. The shape is fixed; do not add fields
// without updating docs/openapi.yaml first.
type ErrorResponse struct {
	// Error is a stable, machine-readable code the frontend can branch on.
	Error string `json:"error"`
	// Message is a human-readable explanation, safe to show to a user.
	Message string `json:"message"`
}

// WriteJSON serialises payload as JSON with the given status code.
//
// The body is encoded before the header is written so that an encoding failure
// does not leave a truncated response behind a 200.
func WriteJSON(w http.ResponseWriter, logger *slog.Logger, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Error("encode response body", slog.Any("error", err))
		writeRawError(w, http.StatusInternalServerError, CodeInternalError,
			"The server could not encode a response.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		// The client is gone; there is nothing left to send.
		logger.Debug("write response body", slog.Any("error", err))
	}
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, logger *slog.Logger, status int, code, message string) {
	WriteJSON(w, logger, status, ErrorResponse{Error: code, Message: message})
}

// writeRawError is the last-resort writer used when JSON encoding itself
// failed. The body is a constant, so it cannot fail the same way.
func writeRawError(w http.ResponseWriter, status int, code, message string) {
	body, _ := json.Marshal(ErrorResponse{Error: code, Message: message})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
