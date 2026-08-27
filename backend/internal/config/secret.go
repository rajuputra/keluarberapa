package config

import "log/slog"

// redactedText replaces a secret in every human- or machine-readable rendering.
const redactedText = "[REDACTED]"

// Secret is a configuration value that must never reach a log line, an error
// message or an API response (ai_instructions.md section 1.8). It deliberately
// implements the interfaces the standard library and log/slog reach for when
// rendering a value, so the only way to obtain the real content is Reveal.
type Secret string

// String satisfies fmt.Stringer, covering %s, %v and Print-style calls.
func (s Secret) String() string { return redactedText }

// GoString satisfies fmt.GoStringer, covering %#v.
func (s Secret) GoString() string { return redactedText }

// LogValue satisfies slog.LogValuer, covering structured log attributes.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redactedText) }

// MarshalJSON keeps secrets out of any JSON payload, including debug dumps.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redactedText + `"`), nil }

// MarshalText keeps secrets out of text-encoded output.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redactedText), nil }

// Reveal returns the underlying value. Call it only at the point of use, such
// as opening a database connection or signing a token.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is unset.
func (s Secret) IsZero() bool { return s == "" }
