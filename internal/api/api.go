// Package api implements the HTTP layer of Deep Reader: the Fiber v3 router,
// request handlers, middleware (bearer auth, slog request logging, panic
// recovery, dev-only CORS, ingestion rate limiting) and embedded static serving
// of the SvelteKit PWA.
//
// Route map (architecture spec §9), all JSON unless noted:
//
//	GET    /healthz                         (no auth) liveness
//	GET    /api/config                      bootstrap / delta sync (?since=RFC3339)
//	GET    /api/articles/:id                full enriched payload (409 if not enriched)
//	POST   /api/articles                    {url} -> {id,status} (rate limited)
//	DELETE /api/articles/:id                remove from library
//	POST   /api/articles/:id/reenrich       requeue to pending
//	PUT    /api/articles/:id/progress       LWW progress upsert -> {applied}
//	PATCH  /api/settings                    partial settings update
//	GET    /api/stats                        library counters
//	GET    /*                               embedded PWA (no auth, SPA fallback)
//
// Construct with New (which uses the embedded web.FS and slog.Default) and call
// Server.App to obtain the *fiber.App for Listen / Test, or Server.Listen /
// Server.Shutdown for lifecycle management.
package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"

	"deep-reader/internal/config"
	"deep-reader/internal/ports"
	"deep-reader/web"
)

// Server wires the Fiber app, dependencies, and configuration for the HTTP
// layer. Construct it with New.
type Server struct {
	cfg    *config.Config
	store  ports.Store
	ingest ports.Ingestor
	log    *slog.Logger
	app    *fiber.App
}

// Option customises Server construction. It exists primarily so tests can
// inject a fake static filesystem and a discard logger; production code uses
// New, which applies the embedded web.FS and slog.Default.
type Option func(*serverOptions)

type serverOptions struct {
	siteFS fs.FS
	log    *slog.Logger
}

// WithStaticFS overrides the embedded PWA filesystem (used in tests).
func WithStaticFS(siteFS fs.FS) Option {
	return func(o *serverOptions) { o.siteFS = siteFS }
}

// WithLogger overrides the request/error logger (used in tests).
func WithLogger(log *slog.Logger) Option {
	return func(o *serverOptions) { o.log = log }
}

// New builds the HTTP server: it constructs the Fiber app, installs middleware,
// registers the API routes under /api (bearer-protected) plus the
// unauthenticated /healthz, and mounts the embedded PWA at the origin root.
//
// The signature matches the ports.go integration contract
// (New(cfg, store, ingestor)); functional options are additive and optional.
func New(cfg *config.Config, st ports.Store, ing ports.Ingestor, opts ...Option) *Server {
	o := &serverOptions{siteFS: web.FS(), log: slog.Default()}
	for _, opt := range opts {
		opt(o)
	}

	s := &Server{
		cfg:    cfg,
		store:  st,
		ingest: ing,
		log:    o.log,
	}
	s.app = s.buildApp(o.siteFS)
	return s
}

// App returns the underlying *fiber.App, e.g. for app.Test in unit tests.
func (s *Server) App() *fiber.App { return s.app }

// Listen starts serving on cfg.HTTPPort and blocks until the server stops.
func (s *Server) Listen() error {
	return s.app.Listen(":"+strconv.Itoa(s.cfg.HTTPPort), fiber.ListenConfig{
		DisableStartupMessage: true,
	})
}

// Shutdown gracefully stops the server, waiting up to the given timeout for
// in-flight requests to complete.
func (s *Server) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return s.app.ShutdownWithContext(ctx)
}

// buildApp assembles the Fiber app, middleware chain, and routes.
func (s *Server) buildApp(siteFS fs.FS) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "deep-reader",
		ErrorHandler: s.errorHandler,
	})

	// Global middleware: recover first so panics in anything below become 500s,
	// then structured request logging, then dev-only CORS.
	app.Use(newRecover())
	app.Use(newRequestLogger(s.log))
	if c := newCORS(s.devMode()); c != nil {
		app.Use(c)
	}

	// Operational, unauthenticated.
	app.Get("/healthz", s.healthz)

	// All /api/* routes require the bearer token.
	api := app.Group("/api", newAuthMiddleware(s.cfg.AuthToken))

	api.Get("/config", s.getConfig)
	api.Get("/stats", s.getStats)

	api.Get("/articles/:id", s.getArticle)
	api.Post("/articles", s.addArticle, s.ingestRateLimiter())
	api.Delete("/articles/:id", s.deleteArticle)
	api.Post("/articles/:id/reenrich", s.reenrichArticle)
	api.Put("/articles/:id/progress", s.putProgress)

	api.Patch("/settings", s.patchSettings)

	// Embedded PWA at the origin root, no auth, SPA fallback. Registered last so
	// it only handles paths not claimed by /healthz or /api.
	registerStatic(app, siteFS)

	return app
}

// devMode reports whether the server runs in development mode, which loosens
// CORS. We key this off LOG_LEVEL=debug per the task brief.
func (s *Server) devMode() bool {
	return s.cfg.LogLevel == "debug"
}

// ingestRateLimiter limits POST /api/articles to a modest rate. The user is
// single, so this only guards against a runaway script, not abuse.
func (s *Server) ingestRateLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        20,
		Expiration: time.Minute,
		LimitReached: func(c fiber.Ctx) error {
			return sendError(c, fiber.StatusTooManyRequests, "rate limit exceeded; slow down")
		},
	})
}

// errorHandler is the app-wide fallback for errors returned by handlers or
// raised by middleware (including recovered panics, surfaced as *fiber.Error
// with status 500). It emits the standard JSON apiError envelope and never
// leaks internals.
func (s *Server) errorHandler(c fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	msg := "internal server error"

	var fe *fiber.Error
	if errors.As(err, &fe) {
		status = fe.Code
		if status < 500 {
			msg = fe.Message
		}
	}
	if status >= 500 {
		s.log.Error("unhandled error", slog.String("path", c.Path()), slog.Any("error", err))
	}
	return sendError(c, status, msg)
}
