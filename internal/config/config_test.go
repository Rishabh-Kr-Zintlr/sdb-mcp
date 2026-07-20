package config

import (
	"testing"
	"time"
)

func TestLoadMissingCredentials(t *testing.T) {
	t.Setenv(envUsername, "")
	t.Setenv(envPassword, "")
	if _, err := Load("test"); err == nil {
		t.Fatal("expected error when credentials are missing, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv(envUsername, "alice")
	t.Setenv(envPassword, "secret")
	// Ensure optional vars are unset for this test.
	t.Setenv(envBaseURL, "")
	t.Setenv(envHTTPTimeout, "")
	t.Setenv(envQueryMax, "")
	t.Setenv(envUserAgent, "")

	cfg, err := Load("1.2.3")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, DefaultBaseURL)
	}
	if cfg.HTTPTimeout != DefaultHTTPTimeout {
		t.Errorf("HTTPTimeout = %v, want %v", cfg.HTTPTimeout, DefaultHTTPTimeout)
	}
	if cfg.QueryMaxLimit != DefaultQueryMaxLimit {
		t.Errorf("QueryMaxLimit = %d, want %d", cfg.QueryMaxLimit, DefaultQueryMaxLimit)
	}
	if want := "sdb-mcp/1.2.3 (+github.com/Rishabh-Kr-Zintlr/sdb-mcp)"; cfg.UserAgent != want {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv(envUsername, "alice")
	t.Setenv(envPassword, "secret")
	t.Setenv(envBaseURL, "https://example.test/")
	t.Setenv(envHTTPTimeout, "5s")
	t.Setenv(envQueryMax, "42")
	t.Setenv(envUserAgent, "custom-agent/9")

	cfg, err := Load("test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := "https://example.test"; cfg.BaseURL != want { // trailing slash trimmed
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, want)
	}
	if cfg.HTTPTimeout != 5*time.Second {
		t.Errorf("HTTPTimeout = %v, want 5s", cfg.HTTPTimeout)
	}
	if cfg.QueryMaxLimit != 42 {
		t.Errorf("QueryMaxLimit = %d, want 42", cfg.QueryMaxLimit)
	}
	if cfg.UserAgent != "custom-agent/9" {
		t.Errorf("UserAgent = %q, want custom-agent/9", cfg.UserAgent)
	}
}

func TestLoadInvalidTimeout(t *testing.T) {
	t.Setenv(envUsername, "alice")
	t.Setenv(envPassword, "secret")
	t.Setenv(envHTTPTimeout, "not-a-duration")
	if _, err := Load("test"); err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}
