// Package config restricts the exploratory API to a synthetic, loopback-only setup.
// This is not a production configuration or a substitute for G1/G2/G3 approval.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrMissing = errors.New("required configuration missing")
	ErrInvalid = errors.New("invalid configuration")
	ErrUnknown = errors.New("unknown configuration field")
)

const redacted = "[REDACTED]"

// Secret has no plaintext formatting or JSON representation. Do not expose the
// private value from this package before a reviewed dependency adapter exists.
type Secret struct{ value string }

func NewSecret(value string) Secret         { return Secret{value: value} }
func (s Secret) IsSet() bool                { return s.value != "" }
func (s Secret) String() string             { return redacted }
func (s Secret) GoString() string           { return redacted }
func (s Secret) Format(w fmt.State, _ rune) { _, _ = io.WriteString(w, redacted) }
func (s Secret) LogValue() slog.Value       { return slog.StringValue(redacted) }
func (s Secret) MarshalJSON() ([]byte, error) {
	return nil, errors.New("secret serialization forbidden")
}

var _ json.Marshaler = Secret{}

// Config contains only local synthetic inputs. The DSN is syntactically checked
// but never dialed by the exploratory server; readiness remains unavailable.
type Config struct {
	Mode        string
	HTTPAddr    string
	PostgresDSN Secret
	TestSecret  Secret
}

// Parse rejects unknown MEMX_* fields, duplicates, missing inputs, non-loopback
// addresses, and non-test databases. It never embeds an input value in an error.
func Parse(environ []string) (Config, error) {
	values := make(map[string]string, 4)
	for _, entry := range environ {
		if !strings.HasPrefix(entry, "MEMX_") {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return Config{}, ErrInvalid
		}
		switch key {
		case "MEMX_MODE", "MEMX_HTTP_ADDR", "MEMX_PG_DSN", "MEMX_TEST_SECRET":
		default:
			return Config{}, ErrUnknown
		}
		if _, exists := values[key]; exists {
			return Config{}, ErrInvalid
		}
		values[key] = value
	}
	for _, key := range []string{"MEMX_MODE", "MEMX_HTTP_ADDR", "MEMX_PG_DSN", "MEMX_TEST_SECRET"} {
		if values[key] == "" {
			return Config{}, ErrMissing
		}
	}
	if values["MEMX_MODE"] != "synthetic" || !loopbackAddress(values["MEMX_HTTP_ADDR"], true) || !localTestDSN(values["MEMX_PG_DSN"]) {
		return Config{}, ErrInvalid
	}
	return Config{
		Mode: "synthetic", HTTPAddr: values["MEMX_HTTP_ADDR"],
		PostgresDSN: NewSecret(values["MEMX_PG_DSN"]), TestSecret: NewSecret(values["MEMX_TEST_SECRET"]),
	}, nil
}

func loopbackAddress(address string, allowZero bool) bool {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host != "127.0.0.1" && host != "::1" {
		return false
	}
	port, err := strconv.Atoi(portText)
	return err == nil && port >= 0 && port <= 65535 && (allowZero || port != 0)
}

func localTestDSN(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "postgres" || u.Path != "/memx_test" || u.Fragment != "" || u.User == nil || u.User.Username() == "" {
		return false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "::1" {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	// For the spike, even the DSN may only identify a local test database. No
	// driver is linked, and this value is never used to open a connection.
	return true
}
