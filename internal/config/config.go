// Package config loads and validates runtime configuration from environment
// variables, as specified in architecture spec §12. Only deployment-level
// settings live here; user-tunable settings (CEFR level, target language,
// model) live in the database, not in env.
//
// NOTE: there is intentionally no STATIC_DIR variable. The frontend is embedded
// into the binary at build time via go:embed (package "deep-reader/web"); there
// is no runtime static directory.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the fully-resolved runtime configuration. All fields are populated
// by Load from environment variables with the defaults documented below.
type Config struct {
	// HTTPPort is the port the HTTP server listens on. Env: HTTP_PORT (8080).
	HTTPPort int
	// DatabasePath is the filesystem path to the SQLite file. Env:
	// DATABASE_PATH (/data/deep-reader.db).
	DatabasePath string

	// TrustProxy enables reading the client IP from the proxy header (default
	// X-Forwarded-For) when the immediate peer is a trusted proxy. Required for
	// the per-IP login brute-force guard to see real client IPs behind a reverse
	// proxy; without it every request appears to originate from the proxy. Env:
	// TRUST_PROXY (false).
	TrustProxy bool
	// TrustedProxies is the allowlist of proxy IPs/CIDRs whose forwarded headers
	// are trusted. Only consulted when TrustProxy is true. When empty (and
	// TrustProxy is true) the loopback, private, and link-local ranges are
	// trusted, which covers the documented reverse-proxy-on-loopback / Docker
	// deployment. Env: TRUSTED_PROXIES (comma-separated, empty).
	TrustedProxies []string

	// LoginMaxAttempts is the number of consecutive failed logins from one client
	// IP that trips a lockout. 0 disables the brute-force guard entirely. Env:
	// LOGIN_MAX_ATTEMPTS (5).
	LoginMaxAttempts int
	// LoginAttemptWindow is the rolling window over which failed logins are
	// counted; failures older than this are forgotten. Env: LOGIN_ATTEMPT_WINDOW
	// (15m).
	LoginAttemptWindow time.Duration
	// LoginLockoutDuration is how long a client IP is locked out after reaching
	// LoginMaxAttempts. Env: LOGIN_LOCKOUT_DURATION (15m).
	LoginLockoutDuration time.Duration

	// LLMAPIBaseURL is the OpenAI-compatible API base URL. Env:
	// LLM_API_BASE_URL.
	LLMAPIBaseURL string
	// LLMAPIKey is the provider API key. Never exposed to the client. Env:
	// LLM_API_KEY (required).
	LLMAPIKey string
	// LLMModel is the default model name. May be overridden per-user via
	// Settings. Env: LLM_MODEL.
	LLMModel string
	// LLMMaxConcurrent is the enrichment worker-pool size. Env:
	// LLM_MAX_CONCURRENT (2).
	LLMMaxConcurrent int
	// LLMRequestTimeout bounds a single LLM call. Env: LLM_REQUEST_TIMEOUT
	// (60s).
	LLMRequestTimeout time.Duration
	// LLMMaxRetries is the number of retries on 429/5xx. Env: LLM_MAX_RETRIES
	// (3).
	LLMMaxRetries int
	// LLMChunkTokens is the target token-window size for the step-wise enrichment:
	// the article's tokens are split into windows of roughly this many tokens
	// (snapped to sentence boundaries) and each window is annotated by its own
	// bounded LLM call, so no single completion is long enough to be truncated.
	// Env: LLM_CHUNK_TOKENS (500).
	LLMChunkTokens int

	// ReadabilityTimeout bounds fetch + extract for ingestion. Env:
	// READABILITY_TIMEOUT (20s).
	ReadabilityTimeout time.Duration

	// MarkdownEnabled turns the markdown.new extractor on as the primary content
	// source (with the local readability extractor as fallback). Env:
	// MARKDOWN_ENABLED (true).
	MarkdownEnabled bool
	// MarkdownBaseURL is the markdown.new (or self-hosted) base URL. Env:
	// MARKDOWN_BASE_URL (https://markdown.new).
	MarkdownBaseURL string
	// MarkdownTimeout bounds a single markdown.new conversion. Browser rendering
	// of JS-heavy pages adds latency, so this is generous. Env: MARKDOWN_TIMEOUT
	// (45s).
	MarkdownTimeout time.Duration
	// MarkdownDailyLimit is the markdown.new request-unit budget per UTC day. The
	// free plan grants 500 units/day. A value <= 0 means unlimited. Env:
	// MARKDOWN_DAILY_LIMIT (500).
	MarkdownDailyLimit int
	// MarkdownCostPerArticle is how many request units one article conversion
	// costs against MarkdownDailyLimit. The free plan bills a crawl at 50 units,
	// so the conservative default is 50 (≈10 conversions/day). Env:
	// MARKDOWN_COST_PER_ARTICLE (50).
	MarkdownCostPerArticle int

	// EnrichmentVersion is the current enrichment schema/prompt version. Bumping
	// it signals re-enrichment. Env: ENRICHMENT_VERSION (2).
	EnrichmentVersion int

	// LogLevel is one of debug|info|warn|error. Env: LOG_LEVEL (info).
	LogLevel string
	// LogFormat is one of json|text. Env: LOG_FORMAT (json).
	LogFormat string

	// SentryDSN is the backend (Go SDK) Sentry DSN. Empty disables backend error
	// reporting entirely. Env: SENTRY_DSN (empty).
	SentryDSN string
	// SentryFrontendDSN is the browser SDK DSN. It is non-secret (browser DSNs
	// are public by design) and delivered to the client via GET /api/config.
	// Empty disables frontend error reporting. Usually a separate Sentry project
	// from SentryDSN. Env: SENTRY_FRONTEND_DSN (empty).
	SentryFrontendDSN string
	// SentryEnvironment is the environment tag (e.g. production|staging) applied
	// to both backend and frontend events. Empty leaves it unset. Env:
	// SENTRY_ENVIRONMENT (empty).
	SentryEnvironment string
}

// Load reads configuration from the process environment, applies defaults, and
// validates the result. It returns an error if a required variable is missing
// or a value fails to parse/validate.
func Load() (*Config, error) {
	// Best-effort load of a local .env into the process environment without
	// overriding already-set variables. Lets `go run ./cmd/server` pick up
	// local config; in production the env is supplied directly and no .env
	// file exists (a missing file is a no-op).
	if err := loadDotEnv(".env"); err != nil {
		return nil, fmt.Errorf("loading .env: %w", err)
	}

	cfg := &Config{}

	port, err := envInt("HTTP_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = port

	cfg.DatabasePath = envStr("DATABASE_PATH", "/data/deep-reader.db")

	trustProxy, err := envBool("TRUST_PROXY", false)
	if err != nil {
		return nil, err
	}
	cfg.TrustProxy = trustProxy
	cfg.TrustedProxies = envStrings("TRUSTED_PROXIES")

	loginMaxAttempts, err := envInt("LOGIN_MAX_ATTEMPTS", 5)
	if err != nil {
		return nil, err
	}
	cfg.LoginMaxAttempts = loginMaxAttempts

	loginWindow, err := envDuration("LOGIN_ATTEMPT_WINDOW", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.LoginAttemptWindow = loginWindow

	loginLockout, err := envDuration("LOGIN_LOCKOUT_DURATION", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.LoginLockoutDuration = loginLockout

	cfg.LLMAPIBaseURL = os.Getenv("LLM_API_BASE_URL")
	cfg.LLMAPIKey = os.Getenv("LLM_API_KEY")
	cfg.LLMModel = os.Getenv("LLM_MODEL")

	maxConcurrent, err := envInt("LLM_MAX_CONCURRENT", 2)
	if err != nil {
		return nil, err
	}
	cfg.LLMMaxConcurrent = maxConcurrent

	reqTimeout, err := envDuration("LLM_REQUEST_TIMEOUT", 60*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.LLMRequestTimeout = reqTimeout

	maxRetries, err := envInt("LLM_MAX_RETRIES", 3)
	if err != nil {
		return nil, err
	}
	cfg.LLMMaxRetries = maxRetries

	chunkTokens, err := envInt("LLM_CHUNK_TOKENS", 500)
	if err != nil {
		return nil, err
	}
	cfg.LLMChunkTokens = chunkTokens

	readTimeout, err := envDuration("READABILITY_TIMEOUT", 20*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.ReadabilityTimeout = readTimeout

	mdEnabled, err := envBool("MARKDOWN_ENABLED", true)
	if err != nil {
		return nil, err
	}
	cfg.MarkdownEnabled = mdEnabled
	cfg.MarkdownBaseURL = envStr("MARKDOWN_BASE_URL", "https://markdown.new")

	mdTimeout, err := envDuration("MARKDOWN_TIMEOUT", 45*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.MarkdownTimeout = mdTimeout

	mdDailyLimit, err := envInt("MARKDOWN_DAILY_LIMIT", 500)
	if err != nil {
		return nil, err
	}
	cfg.MarkdownDailyLimit = mdDailyLimit

	mdCost, err := envInt("MARKDOWN_COST_PER_ARTICLE", 50)
	if err != nil {
		return nil, err
	}
	cfg.MarkdownCostPerArticle = mdCost

	enrichVersion, err := envInt("ENRICHMENT_VERSION", 2)
	if err != nil {
		return nil, err
	}
	cfg.EnrichmentVersion = enrichVersion

	cfg.LogLevel = envStr("LOG_LEVEL", "info")
	cfg.LogFormat = envStr("LOG_FORMAT", "json")

	cfg.SentryDSN = envStr("SENTRY_DSN", "")
	cfg.SentryFrontendDSN = envStr("SENTRY_FRONTEND_DSN", "")
	cfg.SentryEnvironment = envStr("SENTRY_ENVIRONMENT", "")

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate enforces required fields and value ranges.
func (c *Config) validate() error {
	var errs []error

	if c.LLMAPIKey == "" {
		errs = append(errs, errors.New("LLM_API_KEY is required"))
	}
	if c.DatabasePath == "" {
		errs = append(errs, errors.New("DATABASE_PATH must not be empty"))
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		errs = append(errs, fmt.Errorf("HTTP_PORT out of range: %d", c.HTTPPort))
	}
	if c.LLMMaxConcurrent < 1 {
		errs = append(errs, fmt.Errorf("LLM_MAX_CONCURRENT must be >= 1, got %d", c.LLMMaxConcurrent))
	}
	if c.LLMMaxRetries < 0 {
		errs = append(errs, fmt.Errorf("LLM_MAX_RETRIES must be >= 0, got %d", c.LLMMaxRetries))
	}
	if c.LLMChunkTokens < 1 {
		errs = append(errs, fmt.Errorf("LLM_CHUNK_TOKENS must be >= 1, got %d", c.LLMChunkTokens))
	}
	if c.EnrichmentVersion < 1 {
		errs = append(errs, fmt.Errorf("ENRICHMENT_VERSION must be >= 1, got %d", c.EnrichmentVersion))
	}
	if c.LoginMaxAttempts < 0 {
		errs = append(errs, fmt.Errorf("LOGIN_MAX_ATTEMPTS must be >= 0, got %d", c.LoginMaxAttempts))
	}
	if c.LoginMaxAttempts > 0 {
		if c.LoginAttemptWindow <= 0 {
			errs = append(errs, fmt.Errorf("LOGIN_ATTEMPT_WINDOW must be > 0, got %s", c.LoginAttemptWindow))
		}
		if c.LoginLockoutDuration <= 0 {
			errs = append(errs, fmt.Errorf("LOGIN_LOCKOUT_DURATION must be > 0, got %s", c.LoginLockoutDuration))
		}
	}
	if c.MarkdownEnabled {
		if c.MarkdownBaseURL == "" {
			errs = append(errs, errors.New("MARKDOWN_BASE_URL must not be empty when MARKDOWN_ENABLED"))
		}
		if c.MarkdownCostPerArticle < 1 {
			errs = append(errs, fmt.Errorf("MARKDOWN_COST_PER_ARTICLE must be >= 1, got %d", c.MarkdownCostPerArticle))
		}
	}
	if !validLogLevel(c.LogLevel) {
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", c.LogLevel))
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json|text, got %q", c.LogFormat))
	}

	return errors.Join(errs...)
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

// envStr returns the trimmed env var or def when unset/empty.
func envStr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

// envStrings parses a comma-separated env var into a slice of trimmed,
// non-empty values, returning nil when unset/empty.
func envStrings(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// envInt parses an integer env var, returning def when unset/empty.
func envInt(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, raw, err)
	}
	return n, nil
}

// envBool parses a boolean env var, returning def when unset/empty. It accepts
// the strconv.ParseBool set (1/t/true/0/f/false, case-insensitive) plus the
// common "yes"/"no"/"on"/"off" spellings.
func envBool(key string, def bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	switch strings.ToLower(raw) {
	case "yes", "on":
		return true, nil
	case "no", "off":
		return false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s: invalid boolean %q: %w", key, raw, err)
	}
	return b, nil
}

// envDuration parses a Go duration env var (e.g. "60s", "2m"), returning def
// when unset/empty. A bare integer is interpreted as seconds for convenience.
func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	return d, nil
}
