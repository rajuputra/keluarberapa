package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDotEnv(t *testing.T) {
	const content = `
# a comment
APP_ENV=development

export PORT=9090
QUOTED="hello world"
SINGLE='raw $value'
ESCAPED="line1\nline2"
EMPTY=
WITH_EQUALS=postgres://u:p@host:5432/db?sslmode=disable
  SPACED  =  trimmed
`

	vars, err := parseDotEnv(strings.NewReader(content))
	if err != nil {
		t.Fatalf("parseDotEnv: %v", err)
	}

	want := map[string]string{
		"APP_ENV":     "development",
		"PORT":        "9090",
		"QUOTED":      "hello world",
		"SINGLE":      "raw $value",
		"ESCAPED":     "line1\nline2",
		"EMPTY":       "",
		"WITH_EQUALS": "postgres://u:p@host:5432/db?sslmode=disable",
		"SPACED":      "trimmed",
	}
	for key, wantValue := range want {
		if got := vars[key]; got != wantValue {
			t.Errorf("%s = %q, want %q", key, got, wantValue)
		}
	}
	if len(vars) != len(want) {
		t.Errorf("parsed %d vars, want %d: %v", len(vars), len(want), vars)
	}
}

func TestParseDotEnvRejectsMalformedLine(t *testing.T) {
	if _, err := parseDotEnv(strings.NewReader("VALID=1\nNOT_A_PAIR\n")); err == nil {
		t.Fatal("expected an error for a line without '='")
	}
}

func TestLoadDotEnvMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.env")
	if err := LoadDotEnv(path); err != nil {
		t.Errorf("LoadDotEnv on a missing file = %v, want nil", err)
	}
}

// A real environment variable must always win: a stray .env file left in a
// container image must not be able to override the deployed configuration.
func TestLoadDotEnvDoesNotOverrideRealEnvironment(t *testing.T) {
	const key = "KELUARBERAPA_DOTENV_TEST"
	t.Setenv(key, "from-environment")

	path := filepath.Join(t.TempDir(), ".env")
	body := key + "=from-file\nKELUARBERAPA_DOTENV_NEW=from-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv(key); got != "from-environment" {
		t.Errorf("%s = %q, want the pre-existing environment value", key, got)
	}

	// A variable that was not already set does get filled in.
	t.Cleanup(func() { _ = os.Unsetenv("KELUARBERAPA_DOTENV_NEW") })
	if got := os.Getenv("KELUARBERAPA_DOTENV_NEW"); got != "from-file" {
		t.Errorf("KELUARBERAPA_DOTENV_NEW = %q, want %q", got, "from-file")
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `plain`},
		{`"double"`, `double`},
		{`'single'`, `single`},
		{`"unbalanced`, `"unbalanced`},
		{`"a\tb"`, "a\tb"},
		{`'a\tb'`, `a\tb`}, // single quotes keep escapes literal, like a shell
		{`"`, `"`},
		{``, ``},
	}
	for _, tt := range tests {
		if got := unquote(tt.in); got != tt.want {
			t.Errorf("unquote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
