package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func validEnv() []string {
	return []string{
		"MEMX_MODE=synthetic",
		"MEMX_HTTP_ADDR=127.0.0.1:0",
		"MEMX_PG_DSN=postgres://memx:synthetic-password@127.0.0.1:5432/memx_test?sslmode=disable",
		"MEMX_TEST_SECRET=synthetic-canary-123",
		"PATH=/ignored",
	}
}

func TestParseSyntheticConfig(t *testing.T) {
	cfg, err := Parse(validEnv())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "synthetic" || cfg.HTTPAddr != "127.0.0.1:0" || !cfg.PostgresDSN.IsSet() || !cfg.TestSecret.IsSet() {
		t.Fatalf("config was not parsed: mode=%q addr=%q", cfg.Mode, cfg.HTTPAddr)
	}
}

func TestParseFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		env  []string
	}{
		{"unknown MEMX field", append(validEnv(), "MEMX_SURPRISE=secret-canary")},
		{"duplicate field", append(validEnv(), "MEMX_MODE=synthetic")},
		{"missing secret", validEnv()[:3]},
		{"missing dependency", append(validEnv()[:2], validEnv()[3:]...)},
		{"unsupported mode", replace(validEnv(), "MEMX_MODE=production")},
		{"public bind", replace(validEnv(), "MEMX_HTTP_ADDR=0.0.0.0:8080")},
		{"remote database", replace(validEnv(), "MEMX_PG_DSN=postgres://memx:secret-canary@db.example/memx_test")},
		{"wrong database", replace(validEnv(), "MEMX_PG_DSN=postgres://memx:secret-canary@127.0.0.1/memx_prod")},
		{"malformed port", replace(validEnv(), "MEMX_HTTP_ADDR=127.0.0.1:not-a-port")},
		{"empty secret", replace(validEnv(), "MEMX_TEST_SECRET=")},
		{"invalid environment entry", append(validEnv(), "MEMX_BROKEN")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.env)
			if err == nil {
				t.Fatal("expected fail-closed error")
			}
			for _, forbidden := range []string{"synthetic-canary-123", "secret-canary", "synthetic-password", "db.example"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked input: %q", err)
				}
			}
		})
	}
}

func TestSecretNeverRendersPlaintext(t *testing.T) {
	secret := NewSecret("synthetic-canary-123")
	for _, rendered := range []string{
		fmt.Sprint(secret), fmt.Sprintf("%s", secret), fmt.Sprintf("%v", secret),
		fmt.Sprintf("%+v", secret), fmt.Sprintf("%#v", secret),
		slog.Any("secret", secret).Value.String(),
	} {
		if strings.Contains(rendered, "synthetic-canary-123") {
			t.Fatalf("secret leaked: %q", rendered)
		}
	}
	if b, err := json.Marshal(secret); err == nil || strings.Contains(string(b), "synthetic-canary-123") {
		t.Fatal("secret must not be serializable")
	}
	if b, err := json.Marshal(struct{ Secret Secret }{secret}); err == nil || strings.Contains(string(b), "synthetic-canary-123") {
		t.Fatal("config secret must not be serializable")
	}
	cfg, err := Parse(validEnv())
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		if strings.Contains(rendered, "synthetic-password") || strings.Contains(rendered, "synthetic-canary-123") {
			t.Fatal("config struct leaked a secret")
		}
	}
	if b, err := json.Marshal(cfg); err == nil || strings.Contains(string(b), "synthetic-canary-123") {
		t.Fatal("config struct must not be serializable")
	}
}

func replace(env []string, entry string) []string {
	out := append([]string(nil), env...)
	key, _, _ := strings.Cut(entry, "=")
	for i, value := range out {
		if strings.HasPrefix(value, key+"=") {
			out[i] = entry
			return out
		}
	}
	return append(out, entry)
}
