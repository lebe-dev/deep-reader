package api

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"deep-reader/internal/auth"
)

// bearerPrefix is the case-insensitive scheme expected in the Authorization
// header for API requests.
const bearerPrefix = "bearer "

// requestIDLocalsKey is the c.Locals key under which the per-request
// correlation id is stored, so the request logger, panic recovery, and the
// error handler can attach it to every log line for a single request.
const requestIDLocalsKey = "request_id"

// requestIDOf returns the correlation id stored on the request by the
// request-id middleware, or "" when none is set (e.g. before the middleware
// ran). It is the single accessor handlers and middleware use so the Locals key
// stays internal.
func requestIDOf(c fiber.Ctx) string {
	if v, ok := c.Locals(requestIDLocalsKey).(string); ok {
		return v
	}
	return ""
}

// newRequestID returns middleware that assigns each request a correlation id
// (honouring an inbound X-Request-ID, otherwise generating one), echoes it on
// the response header, and stores it in c.Locals so downstream logging can tie
// every line of a request together. It is installed ahead of the request
// logger so the id is available for the http_request line and any 5xx/panic
// reports.
//
// We mint the id ourselves (rather than via fiber's requestid middleware) so
// the value lives under our own Locals key with one stable accessor
// (requestIDOf) and a hardened sanitiser for caller-supplied ids.
func newRequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		header := c.Get(fiber.HeaderXRequestID)
		rid := sanitizeRequestID(header)
		c.Set(fiber.HeaderXRequestID, rid)
		c.Locals(requestIDLocalsKey, rid)
		return c.Next()
	}
}

// sanitizeRequestID echoes a caller-supplied X-Request-ID when it is a sane
// short token of visible ASCII, otherwise it mints a fresh one. Bounding the
// length and charset stops a client from injecting newlines or huge values into
// our log lines via the correlation id.
func sanitizeRequestID(rid string) string {
	const maxLen = 64
	if rid == "" || len(rid) > maxLen {
		return newCorrelationID()
	}
	for i := 0; i < len(rid); i++ {
		if rid[i] < 0x20 || rid[i] > 0x7e {
			return newCorrelationID()
		}
	}
	return rid
}

// newCorrelationID mints a random hex correlation id. crypto/rand never fails
// in practice on the supported platforms; if it ever did we still return the
// (zeroed) buffer encoded, which is harmless for a log-correlation token.
func newCorrelationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// bearerToken extracts the raw token from a "Bearer <token>" Authorization
// header, returning ("", false) when the header is absent or not a bearer token.
func bearerToken(c fiber.Ctx) (string, bool) {
	header := c.Get(fiber.HeaderAuthorization)
	if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := header[len(bearerPrefix):]
	if token == "" {
		return "", false
	}
	return token, true
}

// authenticate reports whether the request carries a valid session token. It is
// the shared check used both by the auth middleware (to gate protected routes)
// and by getConfig (to decide whether to include library data and set the
// authenticated flag).
//
// The error return distinguishes a genuine store/DB failure (which must surface
// as a 5xx so it reaches Sentry and is not mistaken for a credential problem)
// from an ordinary "no/invalid token" result (authed=false, err=nil). A missing
// or malformed bearer header is not a store error, so it returns (false, nil).
func (s *Server) authenticate(c fiber.Ctx) (bool, error) {
	token, ok := bearerToken(c)
	if !ok {
		return false, nil
	}
	exists, err := s.store.SessionExists(c.Context(), auth.HashToken(token))
	if err != nil {
		s.log.Error("session lookup failed",
			slog.String("request_id", requestIDOf(c)),
			slog.Any("error", err),
		)
		return false, err
	}
	return exists, nil
}

// requireAuth is middleware that rejects requests without a valid session token.
// It guards the /api group; /healthz, the public auth endpoints (/api/config,
// /api/setup, /api/login) and the embedded static assets are left open.
//
// A store error returns 503 (not 401) so a DB outage is not masked as a
// credential failure: the request line is then 5xx and reaches Sentry.
func (s *Server) requireAuth(c fiber.Ctx) error {
	authed, err := s.authenticate(c)
	if err != nil {
		return sendError(c, fiber.StatusServiceUnavailable, "authentication temporarily unavailable")
	}
	if !authed {
		return sendError(c, fiber.StatusUnauthorized, "authentication required")
	}
	return c.Next()
}

// newRequestLogger returns middleware that logs one structured line per request
// via slog. It records method, path, status, and latency, choosing the log
// level by status class (5xx → error, 4xx → warn, else info).
func newRequestLogger(log *slog.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		case c.Path() == "/healthz":
			level = slog.LevelDebug
		}
		log.LogAttrs(c.Context(), level, "http_request",
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Duration("latency", time.Since(start)),
			slog.String("ip", c.IP()),
			slog.String("request_id", requestIDOf(c)),
		)
		return err
	}
}

// newRecover returns panic-recovery middleware. A recovered panic becomes a 500
// via the app's ErrorHandler; the StackTraceHandler below also slog.Errors the
// panic value, the stack, and the request id so the crash is attributable in
// structured logs (Fiber's recover does not log a stack by default, and Sentry
// only sees the resulting *fiber.Error without a Go stack). The request logger
// records the resulting 500 line separately.
func newRecover(log *slog.Logger) fiber.Handler {
	return recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			log.Error("http handler panic",
				slog.Any("panic", e),
				slog.String("method", c.Method()),
				slog.String("path", c.Path()),
				slog.String("request_id", requestIDOf(c)),
				slog.String("stack", string(debug.Stack())),
			)
		},
	})
}

// newCORS returns CORS middleware for dev mode, or nil when CORS must stay
// closed (prod). In dev (devMode true, i.e. LOG_LEVEL=debug) it is fully open so
// the SvelteKit dev server on another origin can call the API. In prod the PWA
// is served from the same origin, so the middleware is omitted entirely.
//
// NOTE: Fiber v3 treats an empty AllowOrigins slice as "allow all", so closing
// CORS means NOT registering the middleware rather than passing empty origins.
// The router skips registration when this returns nil.
func newCORS(devMode bool) fiber.Handler {
	if !devMode {
		return nil
	}
	return cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			fiber.MethodGet, fiber.MethodPost, fiber.MethodPut,
			fiber.MethodPatch, fiber.MethodDelete, fiber.MethodOptions,
		},
		AllowHeaders: []string{fiber.HeaderAuthorization, fiber.HeaderContentType},
	})
}
