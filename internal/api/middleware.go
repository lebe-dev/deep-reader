package api

import (
	"crypto/subtle"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

// bearerPrefix is the case-insensitive scheme expected in the Authorization
// header for API requests.
const bearerPrefix = "bearer "

// newAuthMiddleware returns middleware that enforces the shared bearer token.
//
// It is mounted on the /api group only; /healthz and the embedded static assets
// are intentionally left unauthenticated. The token comparison is
// constant-time to avoid leaking it through timing.
func newAuthMiddleware(token string) fiber.Handler {
	want := []byte(token)
	return func(c fiber.Ctx) error {
		header := c.Get(fiber.HeaderAuthorization)
		if header == "" {
			return sendError(c, fiber.StatusUnauthorized, "missing Authorization header")
		}
		if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
			return sendError(c, fiber.StatusUnauthorized, "Authorization header must use the Bearer scheme")
		}
		got := []byte(header[len(bearerPrefix):])
		if subtle.ConstantTimeCompare(got, want) != 1 {
			return sendError(c, fiber.StatusUnauthorized, "invalid token")
		}
		return c.Next()
	}
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
