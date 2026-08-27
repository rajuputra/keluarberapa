package httpapi

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajuputra/keluarberapa/backend/internal/logging"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSON(rec, logging.Discard(), http.StatusCreated, map[string]string{"id": "abc"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"id":"abc"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestWriteErrorUsesTheStandardEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteError(rec, logging.Discard(), http.StatusNotFound, CodeNotFound, "Not found.")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	// architecture.md section 5 fixes the shape as {"error": ..., "message": ...}.
	want := `{"error":"not_found","message":"Not found."}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// An unencodable payload must not leave a 200 with a truncated body behind; the
// status is only written once the body has been produced.
func TestWriteJSONFallsBackWhenEncodingFails(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSON(rec, logging.Discard(), http.StatusOK, map[string]any{"bad": math.Inf(1)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	body := decode[ErrorResponse](t, rec.Body.Bytes())
	if body.Error != CodeInternalError {
		t.Errorf("error = %q, want %q", body.Error, CodeInternalError)
	}
}
