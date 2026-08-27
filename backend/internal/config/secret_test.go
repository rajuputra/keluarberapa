package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
)

const sensitive = "super-secret-value-nobody-should-see"

// A secret must survive no rendering path. Each subtest is one way a value
// normally reaches a log file, an error string or an API response.
func TestSecretIsRedactedEverywhere(t *testing.T) {
	secret := Secret(sensitive)

	renderings := map[string]func() string{
		"String":       func() string { return secret.String() },
		"fmt %s":       func() string { return fmt.Sprintf("%s", secret) },
		"fmt %v":       func() string { return fmt.Sprintf("%v", secret) },
		"fmt %#v":      func() string { return fmt.Sprintf("%#v", secret) },
		"fmt %q":       func() string { return fmt.Sprintf("%q", secret) },
		"errors":       func() string { return fmt.Errorf("connect: %w", fmt.Errorf("dsn %v", secret)).Error() },
		"struct field": func() string { return fmt.Sprintf("%v", JWT{AccessSecret: secret}) },
	}

	for name, render := range renderings {
		t.Run(name, func(t *testing.T) {
			got := render()
			if bytes.Contains([]byte(got), []byte(sensitive)) {
				t.Fatalf("%s leaked the secret: %s", name, got)
			}
			if !bytes.Contains([]byte(got), []byte(redactedText)) {
				t.Errorf("%s = %s, want it to contain %s", name, got, redactedText)
			}
		})
	}
}

func TestSecretJSONIsRedacted(t *testing.T) {
	payload, err := json.Marshal(struct {
		Token Secret `json:"token"`
	}{Token: Secret(sensitive)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(payload, []byte(sensitive)) {
		t.Fatalf("JSON leaked the secret: %s", payload)
	}
	if want := `{"token":"` + redactedText + `"}`; string(payload) != want {
		t.Errorf("JSON = %s, want %s", payload, want)
	}
}

func TestSecretSlogAttrIsRedacted(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logger.Info("connecting", slog.Any("dsn", Secret(sensitive)))

	if bytes.Contains(buf.Bytes(), []byte(sensitive)) {
		t.Fatalf("log line leaked the secret: %s", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte(redactedText)) {
		t.Errorf("log line = %s, want it to contain %s", buf.String(), redactedText)
	}
}

func TestSecretReveal(t *testing.T) {
	if got := Secret(sensitive).Reveal(); got != sensitive {
		t.Errorf("Reveal() = %q, want %q", got, sensitive)
	}
}

func TestSecretIsZero(t *testing.T) {
	if !Secret("").IsZero() {
		t.Error("empty Secret should report IsZero")
	}
	if Secret("x").IsZero() {
		t.Error("non-empty Secret should not report IsZero")
	}
}
