package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// unsetForTest removes a variable from the process env after the caller has
// already registered it for restore via t.Setenv. t.Setenv records the
// pre-test value and restores it on cleanup regardless of a later Unsetenv, so
// this is safe for exercising the "unset" branches of the env parsers.
func unsetForTest(key string) error {
	return os.Unsetenv(key)
}

// setEnv clears then sets the given keys for the duration of the test. Using
// t.Setenv both registers each key for automatic restore and guarantees a
// deterministic starting value (so a key the test does not list falls through
// to its default in Load). Load reads ".env" relative to the package working
// directory; no internal/config/.env exists, so the dotenv step is a no-op here
// and the process env (controlled below) is authoritative.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	// Explicitly clear the optional seed/secret vars so a developer's exported
	// shell environment cannot leak into the assertions below.
	setEnv(t, map[string]string{
		"LLM_API_BASE_URL":    "",
		"LLM_API_KEY":         "",
		"LLM_MODEL":           "",
		"TRUSTED_PROXIES":     "",
		"SENTRY_DSN":          "",
		"SENTRY_FRONTEND_DSN": "",
		"SENTRY_ENVIRONMENT":  "",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with defaults: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"HTTPPort", cfg.HTTPPort, 8080},
		{"DatabasePath", cfg.DatabasePath, "/data/deep-reader.db"},
		{"TrustProxy", cfg.TrustProxy, false},
		{"LoginMaxAttempts", cfg.LoginMaxAttempts, 5},
		{"LoginAttemptWindow", cfg.LoginAttemptWindow, 15 * time.Minute},
		{"LoginLockoutDuration", cfg.LoginLockoutDuration, 15 * time.Minute},
		{"LLMMaxConcurrent", cfg.LLMMaxConcurrent, 2},
		{"LLMRequestTimeout", cfg.LLMRequestTimeout, 60 * time.Second},
		{"LLMMaxRetries", cfg.LLMMaxRetries, 3},
		{"LLMChunkTokens", cfg.LLMChunkTokens, 500},
		{"ReadabilityTimeout", cfg.ReadabilityTimeout, 20 * time.Second},
		{"MarkdownEnabled", cfg.MarkdownEnabled, true},
		{"MarkdownBaseURL", cfg.MarkdownBaseURL, "https://markdown.new"},
		{"MarkdownTimeout", cfg.MarkdownTimeout, 45 * time.Second},
		{"MarkdownDailyLimit", cfg.MarkdownDailyLimit, 500},
		{"MarkdownCostPerArticle", cfg.MarkdownCostPerArticle, 50},
		{"EnrichmentVersion", cfg.EnrichmentVersion, 2},
		{"LogLevel", cfg.LogLevel, "info"},
		{"LogFormat", cfg.LogFormat, "json"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if cfg.TrustedProxies != nil {
		t.Errorf("TrustedProxies = %v, want nil", cfg.TrustedProxies)
	}
}

func TestLoadValidEnvOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"HTTP_PORT":                 "9090",
		"DATABASE_PATH":             "/tmp/db.sqlite",
		"TRUST_PROXY":               "yes",
		"TRUSTED_PROXIES":           "10.0.0.1, 192.168.0.0/16 ,",
		"LOGIN_MAX_ATTEMPTS":        "10",
		"LOGIN_ATTEMPT_WINDOW":      "30m",
		"LOGIN_LOCKOUT_DURATION":    "1h",
		"LLM_API_BASE_URL":          "https://llm.example/v1",
		"LLM_API_KEY":               "sk-seed",
		"LLM_MODEL":                 "gpt-test",
		"LLM_MAX_CONCURRENT":        "4",
		"LLM_REQUEST_TIMEOUT":       "90s",
		"LLM_MAX_RETRIES":           "0",
		"LLM_CHUNK_TOKENS":          "1000",
		"READABILITY_TIMEOUT":       "30",
		"MARKDOWN_ENABLED":          "off",
		"MARKDOWN_BASE_URL":         "https://md.example",
		"MARKDOWN_TIMEOUT":          "60s",
		"MARKDOWN_DAILY_LIMIT":      "0",
		"MARKDOWN_COST_PER_ARTICLE": "25",
		"ENRICHMENT_VERSION":        "3",
		"LOG_LEVEL":                 "debug",
		"LOG_FORMAT":                "text",
		"SENTRY_DSN":                "https://dsn@sentry/1",
		"SENTRY_FRONTEND_DSN":       "https://fe@sentry/2",
		"SENTRY_ENVIRONMENT":        "staging",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with overrides: %v", err)
	}

	if cfg.HTTPPort != 9090 {
		t.Errorf("HTTPPort = %d, want 9090", cfg.HTTPPort)
	}
	if cfg.DatabasePath != "/tmp/db.sqlite" {
		t.Errorf("DatabasePath = %q, want /tmp/db.sqlite", cfg.DatabasePath)
	}
	if !cfg.TrustProxy {
		t.Error("TrustProxy = false, want true (from \"yes\")")
	}
	wantProxies := []string{"10.0.0.1", "192.168.0.0/16"}
	if len(cfg.TrustedProxies) != len(wantProxies) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, wantProxies)
	}
	for i, p := range wantProxies {
		if cfg.TrustedProxies[i] != p {
			t.Errorf("TrustedProxies[%d] = %q, want %q", i, cfg.TrustedProxies[i], p)
		}
	}
	if cfg.LoginMaxAttempts != 10 {
		t.Errorf("LoginMaxAttempts = %d, want 10", cfg.LoginMaxAttempts)
	}
	if cfg.LoginAttemptWindow != 30*time.Minute {
		t.Errorf("LoginAttemptWindow = %s, want 30m", cfg.LoginAttemptWindow)
	}
	if cfg.LoginLockoutDuration != time.Hour {
		t.Errorf("LoginLockoutDuration = %s, want 1h", cfg.LoginLockoutDuration)
	}
	if cfg.LLMAPIBaseURL != "https://llm.example/v1" || cfg.LLMAPIKey != "sk-seed" || cfg.LLMModel != "gpt-test" {
		t.Errorf("LLM seed = (%q,%q,%q)", cfg.LLMAPIBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	}
	if cfg.LLMMaxConcurrent != 4 {
		t.Errorf("LLMMaxConcurrent = %d, want 4", cfg.LLMMaxConcurrent)
	}
	if cfg.LLMRequestTimeout != 90*time.Second {
		t.Errorf("LLMRequestTimeout = %s, want 90s", cfg.LLMRequestTimeout)
	}
	if cfg.LLMMaxRetries != 0 {
		t.Errorf("LLMMaxRetries = %d, want 0", cfg.LLMMaxRetries)
	}
	if cfg.LLMChunkTokens != 1000 {
		t.Errorf("LLMChunkTokens = %d, want 1000", cfg.LLMChunkTokens)
	}
	// Bare integer interpreted as seconds.
	if cfg.ReadabilityTimeout != 30*time.Second {
		t.Errorf("ReadabilityTimeout = %s, want 30s", cfg.ReadabilityTimeout)
	}
	if cfg.MarkdownEnabled {
		t.Error("MarkdownEnabled = true, want false (from \"off\")")
	}
	if cfg.MarkdownBaseURL != "https://md.example" {
		t.Errorf("MarkdownBaseURL = %q", cfg.MarkdownBaseURL)
	}
	if cfg.MarkdownTimeout != 60*time.Second {
		t.Errorf("MarkdownTimeout = %s, want 60s", cfg.MarkdownTimeout)
	}
	if cfg.MarkdownDailyLimit != 0 {
		t.Errorf("MarkdownDailyLimit = %d, want 0", cfg.MarkdownDailyLimit)
	}
	if cfg.MarkdownCostPerArticle != 25 {
		t.Errorf("MarkdownCostPerArticle = %d, want 25", cfg.MarkdownCostPerArticle)
	}
	if cfg.EnrichmentVersion != 3 {
		t.Errorf("EnrichmentVersion = %d, want 3", cfg.EnrichmentVersion)
	}
	if cfg.LogLevel != "debug" || cfg.LogFormat != "text" {
		t.Errorf("Log = (%q,%q)", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.SentryDSN != "https://dsn@sentry/1" || cfg.SentryFrontendDSN != "https://fe@sentry/2" || cfg.SentryEnvironment != "staging" {
		t.Errorf("Sentry = (%q,%q,%q)", cfg.SentryDSN, cfg.SentryFrontendDSN, cfg.SentryEnvironment)
	}
}

// TestLoadParseErrorPropagates verifies a malformed scalar env var aborts Load
// before validation, surfacing the parser's wrapped error.
func TestLoadParseErrorPropagates(t *testing.T) {
	setEnv(t, map[string]string{"HTTP_PORT": "not-a-number"})

	_, err := Load()
	if err == nil {
		t.Fatal("Load: want error for non-integer HTTP_PORT, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP_PORT") {
		t.Errorf("error = %v, want it to mention HTTP_PORT", err)
	}
}

func TestValidate(t *testing.T) {
	// base is a known-valid config; each case mutates one field and asserts the
	// matching rule fires (or, for valid cases, that it does not).
	base := func() *Config {
		return &Config{
			HTTPPort:               8080,
			DatabasePath:           "/data/deep-reader.db",
			LoginMaxAttempts:       5,
			LoginAttemptWindow:     15 * time.Minute,
			LoginLockoutDuration:   15 * time.Minute,
			LLMMaxConcurrent:       2,
			LLMMaxRetries:          3,
			LLMChunkTokens:         500,
			EnrichmentVersion:      2,
			MarkdownEnabled:        true,
			MarkdownBaseURL:        "https://markdown.new",
			MarkdownCostPerArticle: 50,
			LogLevel:               "info",
			LogFormat:              "json",
		}
	}

	if err := base().validate(); err != nil {
		t.Fatalf("base config should be valid, got %v", err)
	}

	cases := []struct {
		name      string
		mutate    func(*Config)
		wantInErr string // empty => expect no error
	}{
		{"empty database path", func(c *Config) { c.DatabasePath = "" }, "DATABASE_PATH"},
		{"port too low", func(c *Config) { c.HTTPPort = 0 }, "HTTP_PORT"},
		{"port too high", func(c *Config) { c.HTTPPort = 70000 }, "HTTP_PORT"},
		{"port min valid", func(c *Config) { c.HTTPPort = 1 }, ""},
		{"port max valid", func(c *Config) { c.HTTPPort = 65535 }, ""},
		{"concurrency zero", func(c *Config) { c.LLMMaxConcurrent = 0 }, "LLM_MAX_CONCURRENT"},
		{"concurrency one ok", func(c *Config) { c.LLMMaxConcurrent = 1 }, ""},
		{"retries negative", func(c *Config) { c.LLMMaxRetries = -1 }, "LLM_MAX_RETRIES"},
		{"retries zero ok", func(c *Config) { c.LLMMaxRetries = 0 }, ""},
		{"chunk tokens zero", func(c *Config) { c.LLMChunkTokens = 0 }, "LLM_CHUNK_TOKENS"},
		{"enrichment version zero", func(c *Config) { c.EnrichmentVersion = 0 }, "ENRICHMENT_VERSION"},
		{"login max attempts negative", func(c *Config) { c.LoginMaxAttempts = -1 }, "LOGIN_MAX_ATTEMPTS"},
		{"login window non-positive when guard on", func(c *Config) { c.LoginAttemptWindow = 0 }, "LOGIN_ATTEMPT_WINDOW"},
		{"login lockout non-positive when guard on", func(c *Config) { c.LoginLockoutDuration = 0 }, "LOGIN_LOCKOUT_DURATION"},
		{
			"login windows ignored when guard disabled",
			func(c *Config) { c.LoginMaxAttempts = 0; c.LoginAttemptWindow = 0; c.LoginLockoutDuration = 0 },
			"",
		},
		{"markdown base url empty when enabled", func(c *Config) { c.MarkdownBaseURL = "" }, "MARKDOWN_BASE_URL"},
		{"markdown cost zero when enabled", func(c *Config) { c.MarkdownCostPerArticle = 0 }, "MARKDOWN_COST_PER_ARTICLE"},
		{
			"markdown coupling skipped when disabled",
			func(c *Config) { c.MarkdownEnabled = false; c.MarkdownBaseURL = ""; c.MarkdownCostPerArticle = 0 },
			"",
		},
		{"invalid log level", func(c *Config) { c.LogLevel = "trace" }, "LOG_LEVEL"},
		{"invalid log format", func(c *Config) { c.LogFormat = "yaml" }, "LOG_FORMAT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.validate()
			if tc.wantInErr == "" {
				if err != nil {
					t.Fatalf("validate: want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate: want error mentioning %q, got nil", tc.wantInErr)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("validate error = %v, want it to mention %q", err, tc.wantInErr)
			}
		})
	}
}

// TestValidateAggregatesErrors confirms validate uses errors.Join: multiple
// independent violations are all reported, not just the first.
func TestValidateAggregatesErrors(t *testing.T) {
	c := &Config{
		HTTPPort:          0,  // out of range
		DatabasePath:      "", // empty
		LLMMaxConcurrent:  0,  // too low
		LLMMaxRetries:     0,  // ok
		LLMChunkTokens:    1,  // ok
		EnrichmentVersion: 1,  // ok
		LoginMaxAttempts:  0,  // guard off -> windows ignored
		MarkdownEnabled:   false,
		LogLevel:          "nope", // invalid
		LogFormat:         "json",
	}

	err := c.validate()
	if err == nil {
		t.Fatal("validate: want aggregated error, got nil")
	}

	// errors.Join wraps each entry; verify all four independent failures are
	// present and individually unwrappable via the joined error string.
	want := []string{"HTTP_PORT", "DATABASE_PATH", "LLM_MAX_CONCURRENT", "LOG_LEVEL"}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("aggregated error missing %q; full error:\n%v", w, err)
		}
	}

	// A joined error reports newline-separated lines, one per violation.
	if lines := strings.Count(err.Error(), "\n") + 1; lines != len(want) {
		t.Errorf("aggregated error has %d lines, want %d:\n%v", lines, len(want), err)
	}
}

func TestEnvBool(t *testing.T) {
	const key = "DR_TEST_BOOL"
	cases := []struct {
		raw     string
		set     bool
		def     bool
		want    bool
		wantErr bool
	}{
		{set: false, def: true, want: true},
		{set: false, def: false, want: false},
		{raw: "", set: true, def: true, want: true},   // empty -> default
		{raw: "  ", set: true, def: true, want: true}, // whitespace-only -> default
		{raw: "yes", set: true, want: true},
		{raw: "YES", set: true, want: true},
		{raw: "on", set: true, want: true},
		{raw: "ON", set: true, want: true},
		{raw: "no", set: true, def: true, want: false},
		{raw: "off", set: true, def: true, want: false},
		{raw: "OFF", set: true, def: true, want: false},
		{raw: "true", set: true, want: true},
		{raw: "1", set: true, want: true},
		{raw: "t", set: true, want: true},
		{raw: "false", set: true, def: true, want: false},
		{raw: "0", set: true, def: true, want: false},
		{raw: "f", set: true, def: true, want: false},
		{raw: "maybe", set: true, wantErr: true},
	}

	for _, tc := range cases {
		name := "unset"
		if tc.set {
			name = "raw=" + tc.raw
		}
		t.Run(name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.raw)
			} else {
				t.Setenv(key, "")
				_ = unsetForTest(key)
			}
			got, err := envBool(key, tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("envBool(%q): want error, got nil", tc.raw)
				}
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error = %v, want it to mention %q", err, key)
				}
				return
			}
			if err != nil {
				t.Fatalf("envBool(%q): unexpected error %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("envBool(%q, def=%v) = %v, want %v", tc.raw, tc.def, got, tc.want)
			}
		})
	}
}

func TestEnvDuration(t *testing.T) {
	const key = "DR_TEST_DURATION"
	cases := []struct {
		raw     string
		set     bool
		def     time.Duration
		want    time.Duration
		wantErr bool
	}{
		{set: false, def: 7 * time.Second, want: 7 * time.Second},
		{raw: "", set: true, def: 9 * time.Second, want: 9 * time.Second},
		{raw: "60s", set: true, want: 60 * time.Second},
		{raw: "2m", set: true, want: 2 * time.Minute},
		{raw: "1h30m", set: true, want: 90 * time.Minute},
		{raw: "60", set: true, want: 60 * time.Second},   // bare int -> seconds
		{raw: "0", set: true, want: 0},                   // bare zero -> 0s
		{raw: " 90 ", set: true, want: 90 * time.Second}, // trimmed then bare int
		{raw: "abc", set: true, wantErr: true},
		{raw: "10x", set: true, wantErr: true},
	}

	for _, tc := range cases {
		name := "unset"
		if tc.set {
			name = "raw=" + tc.raw
		}
		t.Run(name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.raw)
			} else {
				t.Setenv(key, "")
				_ = unsetForTest(key)
			}
			got, err := envDuration(key, tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("envDuration(%q): want error, got nil", tc.raw)
				}
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error = %v, want it to mention %q", err, key)
				}
				return
			}
			if err != nil {
				t.Fatalf("envDuration(%q): unexpected error %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("envDuration(%q, def=%s) = %s, want %s", tc.raw, tc.def, got, tc.want)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	const key = "DR_TEST_INT"

	t.Run("unset returns default", func(t *testing.T) {
		t.Setenv(key, "")
		_ = unsetForTest(key)
		got, err := envInt(key, 42)
		if err != nil || got != 42 {
			t.Fatalf("envInt unset = (%d,%v), want (42,nil)", got, err)
		}
	})

	t.Run("parses value", func(t *testing.T) {
		t.Setenv(key, " 123 ")
		got, err := envInt(key, 0)
		if err != nil || got != 123 {
			t.Fatalf("envInt = (%d,%v), want (123,nil)", got, err)
		}
	})

	t.Run("negative value parses", func(t *testing.T) {
		t.Setenv(key, "-5")
		got, err := envInt(key, 0)
		if err != nil || got != -5 {
			t.Fatalf("envInt = (%d,%v), want (-5,nil)", got, err)
		}
	})

	t.Run("invalid returns error", func(t *testing.T) {
		t.Setenv(key, "12.5")
		_, err := envInt(key, 0)
		if err == nil {
			t.Fatal("envInt: want error for non-integer, got nil")
		}
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error = %v, want it to mention %q", err, key)
		}
	})
}

func TestEnvStr(t *testing.T) {
	const key = "DR_TEST_STR"

	t.Run("unset returns default", func(t *testing.T) {
		t.Setenv(key, "")
		_ = unsetForTest(key)
		if got := envStr(key, "def"); got != "def" {
			t.Errorf("envStr unset = %q, want def", got)
		}
	})

	t.Run("whitespace-only returns default", func(t *testing.T) {
		t.Setenv(key, "   ")
		if got := envStr(key, "def"); got != "def" {
			t.Errorf("envStr blank = %q, want def", got)
		}
	})

	t.Run("trims value", func(t *testing.T) {
		t.Setenv(key, "  value  ")
		if got := envStr(key, "def"); got != "value" {
			t.Errorf("envStr = %q, want value", got)
		}
	})
}

func TestEnvStrings(t *testing.T) {
	const key = "DR_TEST_STRINGS"

	t.Run("unset returns nil", func(t *testing.T) {
		t.Setenv(key, "")
		_ = unsetForTest(key)
		if got := envStrings(key); got != nil {
			t.Errorf("envStrings unset = %v, want nil", got)
		}
	})

	t.Run("trims and drops empties", func(t *testing.T) {
		t.Setenv(key, " a , ,b,  c ,")
		got := envStrings(key)
		want := []string{"a", "b", "c"}
		if len(got) != len(want) {
			t.Fatalf("envStrings = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("envStrings[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

func TestValidLogLevel(t *testing.T) {
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		if !validLogLevel(lvl) {
			t.Errorf("validLogLevel(%q) = false, want true", lvl)
		}
	}
	for _, lvl := range []string{"", "trace", "INFO", "fatal"} {
		if validLogLevel(lvl) {
			t.Errorf("validLogLevel(%q) = true, want false", lvl)
		}
	}
}
