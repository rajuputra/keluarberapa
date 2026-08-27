package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

// validEnv is the minimum environment that must produce a usable Config.
func validEnv(overrides map[string]string) lookup {
	env := map[string]string{
		"DATABASE_URL": "postgres://app:secret@localhost:5432/keluarberapa",
	}
	for k, v := range overrides {
		env[k] = v
	}
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(validEnv(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.App.Env != EnvDevelopment {
		t.Errorf("App.Env = %q, want %q", cfg.App.Env, EnvDevelopment)
	}
	if cfg.App.Timezone != DefaultTimezone {
		t.Errorf("App.Timezone = %q, want %q", cfg.App.Timezone, DefaultTimezone)
	}
	if cfg.App.LogLevel != slog.LevelInfo {
		t.Errorf("App.LogLevel = %v, want info", cfg.App.LogLevel)
	}
	if got, want := cfg.HTTP.Addr(), "0.0.0.0:8080"; got != want {
		t.Errorf("HTTP.Addr() = %q, want %q", got, want)
	}
	// architecture.md section 2: access 15m, refresh 30d.
	if got, want := cfg.JWT.AccessTTL, 15*time.Minute; got != want {
		t.Errorf("JWT.AccessTTL = %v, want %v", got, want)
	}
	if got, want := cfg.JWT.RefreshTTL, 30*24*time.Hour; got != want {
		t.Errorf("JWT.RefreshTTL = %v, want %v", got, want)
	}
	if cfg.Database.AutoMigrate {
		t.Error("Database.AutoMigrate should default to false")
	}
	if cfg.App.IsProduction() {
		t.Error("IsProduction() should be false in development")
	}
}

// Asia/Jakarta must resolve on every platform, including Windows hosts with no
// system zoneinfo. The blank import of time/tzdata in config.go guarantees it.
func TestDefaultTimezoneResolves(t *testing.T) {
	cfg, err := load(validEnv(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	loc := cfg.App.Location()
	if loc == time.UTC {
		t.Fatalf("Location() fell back to UTC; %s did not resolve", DefaultTimezone)
	}
	if loc.String() != DefaultTimezone {
		t.Errorf("Location() = %q, want %q", loc, DefaultTimezone)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := load(func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("expected an error when DATABASE_URL is unset")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Errorf("error = %v, want it to mention DATABASE_URL", err)
	}
}

func TestLoadRejectsNonPostgresDatabaseURL(t *testing.T) {
	const dsn = "mysql://app:topsecret@localhost:3306/db"

	_, err := load(validEnv(map[string]string{"DATABASE_URL": dsn}))
	if err == nil {
		t.Fatal("expected an error for a non-postgres DSN")
	}
	// The DSN holds a password, so it must not be echoed into the error.
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("error leaked the DSN password: %v", err)
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	_, err := load(validEnv(map[string]string{
		"PORT":       "70000",
		"LOG_LEVEL":  "verbose",
		"APP_ENV":    "prod",
		"LOG_FORMAT": "xml",
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"PORT", "LOG_LEVEL", "APP_ENV", "LOG_FORMAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"port out of range", map[string]string{"PORT": "0"}, "PORT"},
		{"port not a number", map[string]string{"PORT": "http"}, "PORT"},
		{"unknown environment", map[string]string{"APP_ENV": "prod"}, "APP_ENV"},
		{"bad duration", map[string]string{"JWT_ACCESS_TTL": "15 minutes"}, "JWT_ACCESS_TTL"},
		{"negative duration", map[string]string{"HTTP_READ_TIMEOUT": "-5s"}, "HTTP_READ_TIMEOUT"},
		{"bad boolean", map[string]string{"DATABASE_AUTO_MIGRATE": "maybe"}, "DATABASE_AUTO_MIGRATE"},
		{"unknown timezone", map[string]string{"APP_TIMEZONE": "Mars/Olympus"}, "APP_TIMEZONE"},
		{"min above max conns", map[string]string{"DATABASE_MAX_CONNS": "2", "DATABASE_MIN_CONNS": "5"}, "DATABASE_MIN_CONNS"},
		{"access ttl not shorter than refresh ttl", map[string]string{"JWT_ACCESS_TTL": "800h"}, "JWT_ACCESS_TTL"},
		{"short jwt secret", map[string]string{"JWT_ACCESS_SECRET": "too-short"}, "JWT_ACCESS_SECRET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(validEnv(tt.env))
			if err == nil {
				t.Fatalf("expected an error for %v", tt.env)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error should mention %s: %v", tt.want, err)
			}
		})
	}
}

func TestLoadRejectsIdenticalJWTSecrets(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"

	_, err := load(validEnv(map[string]string{
		"JWT_ACCESS_SECRET":  secret,
		"JWT_REFRESH_SECRET": secret,
	}))
	if err == nil {
		t.Fatal("expected an error when both JWT secrets are equal")
	}
	if !strings.Contains(err.Error(), "must be different") {
		t.Errorf("error = %v, want it to say the secrets must differ", err)
	}
}

// Stage 1 only serves /health and /ready, so auth and WhatsApp credentials are
// optional locally but mandatory in production.
func TestProductionRequiresSecrets(t *testing.T) {
	_, err := load(validEnv(map[string]string{"APP_ENV": EnvProduction}))
	if err == nil {
		t.Fatal("expected an error when production secrets are missing")
	}

	required := []string{
		"JWT_ACCESS_SECRET", "JWT_REFRESH_SECRET",
		"WHATSAPP_VERIFY_TOKEN", "WHATSAPP_ACCESS_TOKEN",
		"WHATSAPP_APP_SECRET", "WHATSAPP_PHONE_NUMBER_ID",
	}
	for _, key := range required {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("%s should be required in production: %v", key, err)
		}
	}
}

func TestProductionRejectsWildcardCORS(t *testing.T) {
	_, err := load(validEnv(map[string]string{
		"APP_ENV":              EnvProduction,
		"CORS_ALLOWED_ORIGINS": "https://app.example.com,*",
	}))
	if err == nil {
		t.Fatal("expected an error for a wildcard CORS origin in production")
	}
	if !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
		t.Errorf("error = %v, want it to mention CORS_ALLOWED_ORIGINS", err)
	}
}

func TestProductionAcceptsCompleteConfig(t *testing.T) {
	cfg, err := load(validEnv(map[string]string{
		"APP_ENV":                  EnvProduction,
		"JWT_ACCESS_SECRET":        strings.Repeat("a", minSecretLength),
		"JWT_REFRESH_SECRET":       strings.Repeat("b", minSecretLength),
		"WHATSAPP_VERIFY_TOKEN":    "verify-token",
		"WHATSAPP_ACCESS_TOKEN":    "access-token",
		"WHATSAPP_APP_SECRET":      "app-secret",
		"WHATSAPP_PHONE_NUMBER_ID": "123456789",
		"CORS_ALLOWED_ORIGINS":     "https://app.example.com",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.App.IsProduction() {
		t.Error("IsProduction() = false, want true")
	}
	if got, want := cfg.HTTP.AllowedOrigins, []string{"https://app.example.com"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("AllowedOrigins = %v, want %v", got, want)
	}
}

func TestListParsing(t *testing.T) {
	cfg, err := load(validEnv(map[string]string{
		"CORS_ALLOWED_ORIGINS": " http://localhost:4321 , ,https://app.example.com ",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"http://localhost:4321", "https://app.example.com"}
	if len(cfg.HTTP.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %v, want %v", cfg.HTTP.AllowedOrigins, want)
	}
	for i := range want {
		if cfg.HTTP.AllowedOrigins[i] != want[i] {
			t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.HTTP.AllowedOrigins[i], want[i])
		}
	}
}
