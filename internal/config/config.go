// Package config loads and validates runtime configuration for the sdb-mcp
// server, such as the sdb.zintlr.com base URL and API credentials sourced from
// the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Default configuration values. These are used when the corresponding
// environment variable is unset or empty.
const (
	DefaultBaseURL       = "https://sdb.zintlr.com"
	DefaultHTTPTimeout   = 30 * time.Second
	DefaultQueryMaxLimit = 100
)

// Config holds the resolved runtime configuration.
type Config struct {
	// Username and Password are the login credentials. Both are required and
	// come only from the environment; they are never logged.
	Username string
	Password string

	// BaseURL is the upstream API root, without a trailing slash.
	BaseURL string

	// HTTPTimeout bounds each outbound HTTP request.
	HTTPTimeout time.Duration

	// QueryMaxLimit is the hard cap applied to the `limit` of a find query.
	QueryMaxLimit int

	// UserAgent is the outbound User-Agent header. It deliberately identifies
	// this client rather than spoofing a browser.
	UserAgent string
}

// Environment variable names.
const (
	envUsername    = "SDB_USERNAME"
	envPassword    = "SDB_PASSWORD"
	envBaseURL     = "SDB_BASE_URL"
	envHTTPTimeout = "SDB_HTTP_TIMEOUT"
	envQueryMax    = "SDB_QUERY_MAX_LIMIT"
	envUserAgent   = "SDB_USER_AGENT"
)

// DefaultUserAgent returns the default User-Agent for the given build version.
func DefaultUserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("sdb-mcp/%s (+github.com/Rishabh-Kr-Zintlr/sdb-mcp)", version)
}

// Load reads configuration from the environment, applies defaults, and
// validates required values. version is the build version used in the default
// User-Agent.
func Load(version string) (*Config, error) {
	cfg := &Config{
		Username:      strings.TrimSpace(os.Getenv(envUsername)),
		Password:      os.Getenv(envPassword),
		BaseURL:       DefaultBaseURL,
		HTTPTimeout:   DefaultHTTPTimeout,
		QueryMaxLimit: DefaultQueryMaxLimit,
		UserAgent:     DefaultUserAgent(version),
	}

	var missing []string
	if cfg.Username == "" {
		missing = append(missing, envUsername)
	}
	if cfg.Password == "" {
		missing = append(missing, envPassword)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}

	if v := strings.TrimSpace(os.Getenv(envBaseURL)); v != "" {
		cfg.BaseURL = strings.TrimRight(v, "/")
	}

	if v := strings.TrimSpace(os.Getenv(envHTTPTimeout)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", envHTTPTimeout, v, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("invalid %s %q: must be positive", envHTTPTimeout, v)
		}
		cfg.HTTPTimeout = d
	}

	if v := strings.TrimSpace(os.Getenv(envQueryMax)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", envQueryMax, v, err)
		}
		if n <= 0 {
			return nil, fmt.Errorf("invalid %s %q: must be positive", envQueryMax, v)
		}
		cfg.QueryMaxLimit = n
	}

	if v := strings.TrimSpace(os.Getenv(envUserAgent)); v != "" {
		cfg.UserAgent = v
	}

	return cfg, nil
}
