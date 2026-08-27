// Package config loads and validates the application configuration from
// environment variables. Nothing in the codebase reads os.Getenv directly; the
// validated Config value is the single source of truth.
//
// Every variable is documented in backend/.env.example.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	// Asia/Jakarta must resolve even on hosts without a system zoneinfo
	// database (Windows, scratch/distroless containers).
	_ "time/tzdata"
)

// Environment names accepted in APP_ENV.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// DefaultTimezone is the project-wide default (architecture.md section 1).
const DefaultTimezone = "Asia/Jakarta"

// Config is the fully validated application configuration.
type Config struct {
	App      App
	HTTP     HTTP
	Database Database
	JWT      JWT
	WhatsApp WhatsApp
}

// App holds process-level settings.
type App struct {
	Env       string
	Name      string
	Version   string
	LogLevel  slog.Level
	LogFormat string // "json" or "text"
	Timezone  string // IANA name, already verified to load
}

// IsProduction reports whether the process runs with production strictness.
func (a App) IsProduction() bool { return a.Env == EnvProduction }

// Location returns the default display timezone. Load() guarantees it parses.
func (a App) Location() *time.Location {
	loc, err := time.LoadLocation(a.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// HTTP holds the API server settings.
type HTTP struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	AllowedOrigins  []string
}

// Addr is the listen address for the API server.
func (h HTTP) Addr() string { return fmt.Sprintf("%s:%d", h.Host, h.Port) }

// Database holds the PostgreSQL connection settings.
type Database struct {
	URL             Secret
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
	HealthTimeout   time.Duration // budget for the /ready probe
	AutoMigrate     bool          // run pending migrations on API startup
}

// JWT holds token settings. Tokens themselves are issued in a later stage;
// the secrets are validated here so a misconfiguration fails at boot.
type JWT struct {
	AccessSecret  Secret
	RefreshSecret Secret
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	Issuer        string
}

// WhatsApp holds Meta WhatsApp Cloud API settings.
type WhatsApp struct {
	VerifyToken   Secret
	AccessToken   Secret
	AppSecret     Secret
	PhoneNumberID string
	APIBaseURL    string
	APIVersion    string
}

// minSecretLength is the shortest secret we accept for HMAC signing keys.
const minSecretLength = 32

// Load reads the configuration from the process environment.
//
// Validation errors are collected rather than returned one at a time, so a
// misconfigured deployment reports everything that is wrong in a single pass.
func Load() (*Config, error) { return load(os.LookupEnv) }

// lookup mirrors os.LookupEnv so tests can supply a fake environment.
type lookup func(key string) (string, bool)

func load(env lookup) (*Config, error) {
	p := &parser{env: env}

	appEnv := p.enum("APP_ENV", EnvDevelopment,
		EnvDevelopment, EnvTest, EnvStaging, EnvProduction)

	cfg := &Config{
		App: App{
			Env:       appEnv,
			Name:      p.str("APP_NAME", "keluarberapa-api"),
			Version:   p.str("APP_VERSION", "0.1.0"),
			LogLevel:  p.logLevel("LOG_LEVEL", slog.LevelInfo),
			LogFormat: p.enum("LOG_FORMAT", "json", "json", "text"),
			Timezone:  p.timezone("APP_TIMEZONE", DefaultTimezone),
		},
		HTTP: HTTP{
			Host:            p.str("HTTP_HOST", "0.0.0.0"),
			Port:            p.port("PORT", 8080),
			ReadTimeout:     p.duration("HTTP_READ_TIMEOUT", 10*time.Second),
			WriteTimeout:    p.duration("HTTP_WRITE_TIMEOUT", 15*time.Second),
			IdleTimeout:     p.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: p.duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			AllowedOrigins:  p.list("CORS_ALLOWED_ORIGINS", []string{"http://localhost:4321"}),
		},
		Database: Database{
			URL:             p.databaseURL("DATABASE_URL"),
			MaxConns:        int32(p.intRange("DATABASE_MAX_CONNS", 10, 1, 1000)),
			MinConns:        int32(p.intRange("DATABASE_MIN_CONNS", 2, 0, 1000)),
			MaxConnLifetime: p.duration("DATABASE_MAX_CONN_LIFETIME", time.Hour),
			MaxConnIdleTime: p.duration("DATABASE_MAX_CONN_IDLE_TIME", 30*time.Minute),
			ConnectTimeout:  p.duration("DATABASE_CONNECT_TIMEOUT", 5*time.Second),
			HealthTimeout:   p.duration("DATABASE_HEALTH_TIMEOUT", 2*time.Second),
			AutoMigrate:     p.boolean("DATABASE_AUTO_MIGRATE", false),
		},
		JWT: JWT{
			AccessSecret:  p.secret("JWT_ACCESS_SECRET"),
			RefreshSecret: p.secret("JWT_REFRESH_SECRET"),
			// architecture.md section 2: access 15m, refresh 30d.
			AccessTTL:  p.duration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: p.duration("JWT_REFRESH_TTL", 30*24*time.Hour),
			Issuer:     p.str("JWT_ISSUER", "keluarberapa"),
		},
		WhatsApp: WhatsApp{
			VerifyToken:   p.secret("WHATSAPP_VERIFY_TOKEN"),
			AccessToken:   p.secret("WHATSAPP_ACCESS_TOKEN"),
			AppSecret:     p.secret("WHATSAPP_APP_SECRET"),
			PhoneNumberID: p.str("WHATSAPP_PHONE_NUMBER_ID", ""),
			APIBaseURL:    p.str("WHATSAPP_API_BASE_URL", "https://graph.facebook.com"),
			APIVersion:    p.str("WHATSAPP_API_VERSION", "v21.0"),
		},
	}

	cfg.validate(p)

	if err := p.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate applies the cross-field rules that a single variable cannot express.
func (c *Config) validate(p *parser) {
	if c.Database.MinConns > c.Database.MaxConns {
		p.addf("DATABASE_MIN_CONNS (%d) must not exceed DATABASE_MAX_CONNS (%d)",
			c.Database.MinConns, c.Database.MaxConns)
	}
	if c.JWT.AccessTTL >= c.JWT.RefreshTTL {
		p.addf("JWT_ACCESS_TTL (%s) must be shorter than JWT_REFRESH_TTL (%s)",
			c.JWT.AccessTTL, c.JWT.RefreshTTL)
	}

	// Secrets that are set must be strong enough, in every environment.
	for _, s := range []struct {
		key   string
		value Secret
	}{
		{"JWT_ACCESS_SECRET", c.JWT.AccessSecret},
		{"JWT_REFRESH_SECRET", c.JWT.RefreshSecret},
	} {
		if !s.value.IsZero() && len(s.value) < minSecretLength {
			p.addf("%s must be at least %d characters", s.key, minSecretLength)
		}
	}
	if !c.JWT.AccessSecret.IsZero() && c.JWT.AccessSecret == c.JWT.RefreshSecret {
		p.add("JWT_ACCESS_SECRET and JWT_REFRESH_SECRET must be different")
	}

	// Stage 1 exposes only /health and /ready, so auth and WhatsApp credentials
	// are optional for local development. A production deployment must have
	// them: booting without them would only fail later, on a live request.
	if c.App.IsProduction() {
		required := []struct {
			key   string
			empty bool
		}{
			{"JWT_ACCESS_SECRET", c.JWT.AccessSecret.IsZero()},
			{"JWT_REFRESH_SECRET", c.JWT.RefreshSecret.IsZero()},
			{"WHATSAPP_VERIFY_TOKEN", c.WhatsApp.VerifyToken.IsZero()},
			{"WHATSAPP_ACCESS_TOKEN", c.WhatsApp.AccessToken.IsZero()},
			{"WHATSAPP_APP_SECRET", c.WhatsApp.AppSecret.IsZero()},
			{"WHATSAPP_PHONE_NUMBER_ID", c.WhatsApp.PhoneNumberID == ""},
		}
		for _, r := range required {
			if r.empty {
				p.addf("%s is required when APP_ENV=%s", r.key, EnvProduction)
			}
		}
		for _, origin := range c.HTTP.AllowedOrigins {
			if origin == "*" {
				p.add("CORS_ALLOWED_ORIGINS must not contain \"*\" when APP_ENV=" + EnvProduction)
			}
		}
	}
}

// parser reads typed values from an environment and accumulates errors.
type parser struct {
	env  lookup
	errs []error
}

func (p *parser) add(msg string)               { p.errs = append(p.errs, errors.New(msg)) }
func (p *parser) addf(format string, a ...any) { p.errs = append(p.errs, fmt.Errorf(format, a...)) }

// raw returns the trimmed value and whether the variable was set at all.
func (p *parser) raw(key string) (string, bool) {
	v, ok := p.env(key)
	return strings.TrimSpace(v), ok
}

func (p *parser) str(key, def string) string {
	if v, ok := p.raw(key); ok && v != "" {
		return v
	}
	return def
}

func (p *parser) err() error {
	if len(p.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration: %w", errors.Join(p.errs...))
}

func (p *parser) secret(key string) Secret { return Secret(p.str(key, "")) }

func (p *parser) boolean(key string, def bool) bool {
	v, ok := p.raw(key)
	if !ok || v == "" {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		p.addf("%s must be a boolean (got %q)", key, v)
		return def
	}
	return parsed
}

func (p *parser) intRange(key string, def, low, high int) int {
	v, ok := p.raw(key)
	if !ok || v == "" {
		return def
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		p.addf("%s must be an integer (got %q)", key, v)
		return def
	}
	if parsed < low || parsed > high {
		p.addf("%s must be between %d and %d (got %d)", key, low, high, parsed)
		return def
	}
	return parsed
}

func (p *parser) port(key string, def int) int { return p.intRange(key, def, 1, 65535) }

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.raw(key)
	if !ok || v == "" {
		return def
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		p.addf("%s must be a duration such as 15m or 30s (got %q)", key, v)
		return def
	}
	if parsed <= 0 {
		p.addf("%s must be positive (got %s)", key, parsed)
		return def
	}
	return parsed
}

func (p *parser) enum(key, def string, allowed ...string) string {
	v := p.str(key, def)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	p.addf("%s must be one of %s (got %q)", key, strings.Join(allowed, ", "), v)
	return def
}

func (p *parser) logLevel(key string, def slog.Level) slog.Level {
	v, ok := p.raw(key)
	if !ok || v == "" {
		return def
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		p.addf("%s must be one of debug, info, warn, error (got %q)", key, v)
		return def
	}
	return level
}

func (p *parser) timezone(key, def string) string {
	v := p.str(key, def)
	if _, err := time.LoadLocation(v); err != nil {
		p.addf("%s must be a valid IANA timezone (got %q)", key, v)
		return def
	}
	return v
}

// list splits a comma-separated value, dropping empty entries.
func (p *parser) list(key string, def []string) []string {
	v, ok := p.raw(key)
	if !ok || v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// databaseURL validates the one variable without which the service cannot run.
func (p *parser) databaseURL(key string) Secret {
	v := p.str(key, "")
	switch {
	case v == "":
		p.addf("%s is required", key)
	case !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://"):
		// Reported without the value: a DSN normally contains a password.
		p.addf("%s must be a postgres:// or postgresql:// connection string", key)
	}
	return Secret(v)
}
