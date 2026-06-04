package api

import (
	"log/slog"
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
// authenticated flag). A store error is treated as unauthenticated and logged.
func (s *Server) authenticate(c fiber.Ctx) bool {
	token, ok := bearerToken(c)
	if !ok {
		return false
	}
	exists, err := s.store.SessionExists(c.Context(), auth.HashToken(token))
	if err != nil {
		s.log.Error("session lookup failed", slog.Any("error", err))
		return false
	}
	return exists
}

// requireAuth is middleware that rejects requests without a valid session token.
// It guards the /api group; /healthz, the public auth endpoints (/api/config,
// /api/setup, /api/login) and the embedded static assets are left open.
func (s *Server) requireAuth(c fiber.Ctx) error {
	if !s.authenticate(c) {
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
		)
		return err
	}
}

// newRecover returns panic-recovery middleware. A recovered panic becomes a 500
// via the app's ErrorHandler; the request logger above will record it.
func newRecover() fiber.Handler {
	return recover.New()
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
