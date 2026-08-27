package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler writes 200 and a short body.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

func TestChainAppliesOutermostFirst(t *testing.T) {
	var order []string

	record := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), record("first"), record("second"))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first", "second", "handler"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestRequestIDGeneratesWhenAbsent(t *testing.T) {
	var seen string
	handler := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if len(seen) != 32 {
		t.Errorf("generated request id = %q, want 32 hex characters", seen)
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Errorf("%s header = %q, want %q", RequestIDHeader, got, seen)
	}
}

func TestRequestIDReusesWellFormedInboundID(t *testing.T) {
	const inbound = "trace-abc-123"

	var seen string
	handler := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(RequestIDHeader, inbound)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if seen != inbound {
		t.Errorf("request id = %q, want the inbound %q", seen, inbound)
	}
}

// A client-supplied id ends up in logs and response headers, so anything with
// control characters or an implausible length must be replaced.
func TestRequestIDRejectsUntrustworthyInboundID(t *testing.T) {
	tests := map[string]string{
		"too short":         "abc",
		"too long":          strings.Repeat("a", 65),
		"header injection":  "abcdefgh\r\nSet-Cookie: x=1",
		"illegal character": "abcdefgh<script>",
		"empty":             "",
	}

	for name, inbound := range tests {
		t.Run(name, func(t *testing.T) {
			var seen string
			handler := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = RequestIDFrom(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Header[RequestIDHeader] = []string{inbound}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			if seen == inbound {
				t.Errorf("request id %q was accepted, want a generated replacement", inbound)
			}
			if len(seen) != 32 {
				t.Errorf("replacement id = %q, want 32 hex characters", seen)
			}
		})
	}
}

func TestRequestIDFromEmptyContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := RequestIDFrom(req.Context()); got != "" {
		t.Errorf("RequestIDFrom on a bare context = %q, want empty", got)
	}
}

func TestRequestLoggerRecordsOutcome(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}), RequestID(), RequestLogger(logger))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/things", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	if line["msg"] != "http request" {
		t.Errorf("msg = %v, want \"http request\"", line["msg"])
	}
	if line["status"] != float64(http.StatusCreated) {
		t.Errorf("status = %v, want %d", line["status"], http.StatusCreated)
	}
	if line["method"] != http.MethodPost {
		t.Errorf("method = %v, want %s", line["method"], http.MethodPost)
	}
	if line["path"] != "/api/v1/things" {
		t.Errorf("path = %v, want /api/v1/things", line["path"])
	}
	if line["bytes"] != float64(len("created")) {
		t.Errorf("bytes = %v, want %d", line["bytes"], len("created"))
	}
	if id, _ := line["request_id"].(string); id == "" {
		t.Error("request_id should be logged")
	}
}

func TestRequestLoggerSeverityFollowsStatus(t *testing.T) {
	tests := []struct {
		status int
		path   string
		want   string
	}{
		{http.StatusOK, "/api/v1/things", "INFO"},
		{http.StatusOK, "/health", "DEBUG"}, // probes must not drown out traffic
		{http.StatusNotFound, "/api/v1/things", "WARN"},
		{http.StatusInternalServerError, "/api/v1/things", "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			handler := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))

			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line is not JSON: %v", err)
			}
			if line["level"] != tt.want {
				t.Errorf("level = %v, want %s", line["level"], tt.want)
			}
		})
	}
}

// The logger must never record a request body: it can hold a password or the
// text of a WhatsApp message.
func TestRequestLoggerDoesNotLogBody(t *testing.T) {
	const body = `{"password":"correct-horse-battery-staple"}`

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := RequestLogger(logger)(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if bytes.Contains(buf.Bytes(), []byte("correct-horse-battery-staple")) {
		t.Errorf("log line leaked the request body: %s", buf.String())
	}
}

func TestRecoveryTurnsPanicIntoHandledResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	called := false
	handler := Recovery(logger, func(w http.ResponseWriter, _ *http.Request, _ any) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal_error","message":"boom"}`))
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("database exploded: password=hunter2")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/things", nil))

	if !called {
		t.Error("the injected panic handler was not called")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	// The panic detail belongs in the log, not in the response.
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaked the panic detail: %s", rec.Body.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("panic recovered")) {
		t.Errorf("panic was not logged: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("stack")) {
		t.Error("the log line should include a stack trace")
	}
}

func TestRecoveryWithoutPanicHandlerStillResponds(t *testing.T) {
	handler := Recovery(discardLogger(), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// Once a handler has written the status line, the response cannot be rewritten;
// appending an error envelope would corrupt the body the client is reading.
func TestRecoveryDoesNotRewriteAStartedResponse(t *testing.T) {
	handler := Recovery(discardLogger(), func(w http.ResponseWriter, _ *http.Request, _ any) {
		_, _ = w.Write([]byte("SHOULD NOT APPEAR"))
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"partial":`))
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the already-written %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "SHOULD NOT APPEAR") {
		t.Errorf("recovery appended to a started response: %s", rec.Body.String())
	}
}

// net/http uses ErrAbortHandler to drop a connection deliberately; swallowing it
// would defeat that.
func TestRecoveryRepanicsOnErrAbortHandler(t *testing.T) {
	handler := Recovery(discardLogger(), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler to propagate", recovered)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "http://localhost:4321")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4321" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the request origin", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestCORSRejectsUnknownOrigin(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it to be absent", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321"})(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			t.Error("preflight should not reach the handler")
		}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/things", nil)
	req.Header.Set("Origin", "http://localhost:4321")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPost) {
		t.Errorf("Access-Control-Allow-Methods = %q, want it to include POST", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to include Authorization", got)
	}
	if rec.Header().Get("Access-Control-Max-Age") == "" {
		t.Error("Access-Control-Max-Age should be set on a preflight response")
	}
}

func TestCORSPreflightFromUnknownOriginGetsNoPermission(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321"})(okHandler())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it to be absent", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q, want it to be absent", got)
	}
}

func TestCORSIgnoresTrailingSlashDifference(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321/"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "http://localhost:4321")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4321" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the request origin", got)
	}
}

func TestCORSWildcardEchoesTheRequestOrigin(t *testing.T) {
	handler := CORS([]string{"*"})(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/things", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// The concrete origin is echoed rather than "*", so the header stays valid
	// if credentialed requests are enabled later.
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the request origin", got)
	}
}

func TestCORSWithoutOriginHeaderIsUntouched(t *testing.T) {
	handler := CORS([]string{"http://localhost:4321"})(okHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it to be absent", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestResponseRecorderKeepsTheFirstStatus(t *testing.T) {
	rec := newResponseRecorder(httptest.NewRecorder())

	rec.WriteHeader(http.StatusTeapot)
	rec.WriteHeader(http.StatusOK)

	if rec.status != http.StatusTeapot {
		t.Errorf("status = %d, want the first one written (%d)", rec.status, http.StatusTeapot)
	}
}

func TestResponseRecorderDefaultsToOKOnWrite(t *testing.T) {
	rec := newResponseRecorder(httptest.NewRecorder())

	if _, err := rec.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.status, http.StatusOK)
	}
	if rec.bytes != len("hello") {
		t.Errorf("bytes = %d, want %d", rec.bytes, len("hello"))
	}
}
