package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/rajuputra/keluarberapa/backend/internal/config"
)

func testApp() config.App {
	return config.App{
		Env:       config.EnvTest,
		Name:      "keluarberapa-api",
		Version:   "0.1.0-test",
		LogLevel:  slog.LevelInfo,
		LogFormat: "json",
	}
}

func TestNewJSONLoggerCarriesServiceContext(t *testing.T) {
	var buf bytes.Buffer

	New(testApp(), &buf).Info("hello", slog.String("extra", "value"))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, buf.String())
	}
	want := map[string]string{
		"service": "keluarberapa-api",
		"version": "0.1.0-test",
		"env":     config.EnvTest,
		"msg":     "hello",
		"extra":   "value",
	}
	for key, wantValue := range want {
		if line[key] != wantValue {
			t.Errorf("%s = %v, want %q", key, line[key], wantValue)
		}
	}
}

func TestNewTextLogger(t *testing.T) {
	var buf bytes.Buffer

	app := testApp()
	app.LogFormat = "text"
	New(app, &buf).Info("hello")

	out := buf.String()
	if json.Valid(buf.Bytes()) {
		t.Errorf("text format produced JSON: %s", out)
	}
	if !strings.Contains(out, "service=keluarberapa-api") {
		t.Errorf("log line = %s, want it to carry the service name", out)
	}
}

func TestLogLevelIsHonoured(t *testing.T) {
	var buf bytes.Buffer

	app := testApp()
	app.LogLevel = slog.LevelWarn
	logger := New(app, &buf)

	logger.Debug("debug")
	logger.Info("info")
	if buf.Len() != 0 {
		t.Errorf("levels below warn should be dropped: %s", buf.String())
	}

	logger.Warn("warn")
	if !strings.Contains(buf.String(), "warn") {
		t.Errorf("warn should be logged: %s", buf.String())
	}
}

// A secret handed to the logger, however it is wrapped, must come out redacted.
func TestSecretsAreRedactedInLogs(t *testing.T) {
	const dsn = "postgres://app:hunter2@db:5432/keluarberapa"

	var buf bytes.Buffer
	logger := New(testApp(), &buf)

	logger.Info("starting", slog.Any("database_url", config.Secret(dsn)))
	logger.Info("config", slog.Any("jwt", config.JWT{AccessSecret: config.Secret("s3cr3t")}))

	if bytes.Contains(buf.Bytes(), []byte("hunter2")) {
		t.Errorf("log leaked the DSN password: %s", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("s3cr3t")) {
		t.Errorf("log leaked the JWT secret: %s", buf.String())
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	logger := Discard()
	if logger == nil {
		t.Fatal("Discard() returned nil")
	}
	// Nothing to assert on the output; the point is that it must not panic.
	logger.Error("this goes nowhere")
}
