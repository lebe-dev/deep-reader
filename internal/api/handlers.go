package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/version"
)

// getConfig handles GET /api/config — the single bootstrap / delta-sync
// endpoint. It returns settings, article metadata, and progress. An optional
// ?since=<RFC3339> narrows the article and progress lists to records updated at
// or after that instant (delta sync). ServerTime is the authoritative cursor
// the client persists for its next sync.
func (s *Server) getConfig(c fiber.Ctx) error {
	ctx := c.Context()

	initialized, err := s.store.IsInitialized(ctx)
	if err != nil {
		return s.serverError(c, "is initialized", err)
	}
	authed := initialized && s.authenticate(c)

	// Unauthenticated (or uninitialized) callers receive only the auth flag so
	// the client can route to /setup or /login; no library data leaks.
	if !authed {
		return c.JSON(model.ConfigResponse{
			Auth:       model.AuthStatus{Initialized: initialized, Authenticated: false},
			ServerTime: time.Now().UTC(),
		})
	}

	since, err := parseSince(c.Query("since"))
	if err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid since: expected RFC3339 timestamp")
	}

	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return s.serverError(c, "get settings", err)
	}
	metas, err := s.store.ListArticleMeta(ctx, since)
	if err != nil {
		return s.serverError(c, "list article meta", err)
	}
	progress, err := s.store.ListProgress(ctx, since)
	if err != nil {
		return s.serverError(c, "list progress", err)
	}

	budget, err := s.markdownBudget(ctx)
	if err != nil {
		return s.serverError(c, "markdown budget", err)
	}

	return c.JSON(model.ConfigResponse{
		Auth:           model.AuthStatus{Initialized: true, Authenticated: true},
		Settings:       settings,
		Articles:       metas,
		Progress:       progress,
		MarkdownBudget: budget,
		ServerInfo:     serverInfoFromConfig(s.cfg),
		ServerTime:     time.Now().UTC(),
	})
}

// markdownBudget assembles the markdown.new daily budget from deployment config
// and today's consumption. When markdown.new is disabled it returns a
// zero-value budget with Enabled=false.
func (s *Server) markdownBudget(ctx context.Context) (model.MarkdownBudget, error) {
	if !s.cfg.MarkdownEnabled {
		return model.MarkdownBudget{Enabled: false}, nil
	}

	used, err := s.store.MarkdownUnitsUsedToday(ctx)
	if err != nil {
		return model.MarkdownBudget{}, err
	}

	limit := s.cfg.MarkdownDailyLimit
	cost := s.cfg.MarkdownCostPerArticle
	remaining := max(limit-used, 0)

	articlesRemaining := 0
	if cost > 0 {
		articlesRemaining = remaining / cost
	}

	return model.MarkdownBudget{
		Enabled:           true,
		DailyLimit:        limit,
		CostPerArticle:    cost,
		UnitsUsed:         used,
		UnitsRemaining:    remaining,
		ArticlesRemaining: articlesRemaining,
	}, nil
}

// getArticle handles GET /api/articles/:id — the full enriched payload. If the
// article is not yet enriched it returns 409 Conflict (the payload's Status
// still communicates the state). Unknown ids return 404.
func (s *Server) getArticle(c fiber.Ctx) error {
	id := c.Params("id")

	payload, err := s.store.GetArticlePayload(c.Context(), id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "get article payload", err)
	}

	if payload.Status != model.StatusEnriched || payload.Enrichment == nil {
		return c.Status(fiber.StatusConflict).JSON(payload)
	}

	// Enriched articles are immutable; allow aggressive client caching.
	c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
	return c.JSON(payload)
}

// addArticle handles POST /api/articles. Body: {url}. It ingests via the
// Ingestor (dedup-transparent) and returns {id,status}. Blocked-host,
// unparseable, too-large, and malformed-URL conditions map to 4xx.
func (s *Server) addArticle(c fiber.Ctx) error {
	var req model.AddArticleRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if req.URL == "" {
		return sendError(c, fiber.StatusBadRequest, "url is required")
	}

	article, err := s.ingest.Add(c.Context(), req.URL)
	if err != nil {
		status, msg := mapAddError(err)
		if status >= 500 {
			s.log.Error("ingest add failed", slog.String("url", req.URL), slog.Any("error", err))
		}
		return sendError(c, status, msg)
	}

	return c.Status(fiber.StatusCreated).JSON(model.AddArticleResponse{
		ID:     article.ID,
		Status: article.Status,
	})
}

// deleteArticle handles DELETE /api/articles/:id, removing it from the library.
func (s *Server) deleteArticle(c fiber.Ctx) error {
	id := c.Params("id")

	if err := s.store.DeleteArticle(c.Context(), id); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "delete article", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// retryArticle handles POST /api/articles/:id/retry, resuming a failed article
// from the stage that failed (re-fetch or re-enrich). It returns the article's
// resulting status. Unknown ids return 404.
func (s *Server) retryArticle(c fiber.Ctx) error {
	id := c.Params("id")

	if err := s.ingest.Retry(c.Context(), id); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "retry article", err)
	}

	// Reflect the post-retry status (queued for a re-fetch, fetched for a
	// re-enrich) so the client can update its optimistic state.
	status := model.StatusQueued
	if a, err := s.store.GetArticle(c.Context(), id); err == nil {
		status = a.Status
	}
	return c.Status(fiber.StatusAccepted).JSON(model.AddArticleResponse{
		ID:     id,
		Status: status,
	})
}

// putProgress handles PUT /api/articles/:id/progress. Body:
// {position,is_read,updated_at}. The store applies LWW on UpdatedAt; the
// response reports whether the incoming record won via {applied: bool}.
func (s *Server) putProgress(c fiber.Ctx) error {
	id := c.Params("id")

	var body progressRequest
	if err := c.Bind().Body(&body); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if body.UpdatedAt.IsZero() {
		return sendError(c, fiber.StatusBadRequest, "updated_at is required")
	}

	applied, err := s.store.UpsertProgress(c.Context(), model.Progress{
		ArticleID: id,
		Position:  body.Position,
		IsRead:    body.IsRead,
		UpdatedAt: body.UpdatedAt.UTC(),
	})
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "upsert progress", err)
	}

	return c.JSON(progressResponse{Applied: applied})
}

// setPinned handles PUT /api/articles/:id/pin. Body: {pinned}. It flips the
// article's library pin flag (bumping updated_at so the change syncs) and
// returns 204. Unknown ids return 404.
func (s *Server) setPinned(c fiber.Ctx) error {
	id := c.Params("id")

	var body pinRequest
	if err := c.Bind().Body(&body); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}

	if err := s.store.SetPinned(c.Context(), id, body.Pinned); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "set pinned", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// patchSettings handles PATCH /api/settings with a partial settings body. Nil
// fields are left unchanged. It returns the resulting settings.
func (s *Server) patchSettings(c fiber.Ctx) error {
	var patch model.SettingsPatch
	if err := c.Bind().Body(&patch); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if msg, ok := validateSettingsPatch(patch); !ok {
		return sendError(c, fiber.StatusBadRequest, msg)
	}

	settings, err := s.store.UpdateSettings(c.Context(), patch)
	if err != nil {
		return s.serverError(c, "update settings", err)
	}
	return c.JSON(settings)
}

// getStats handles GET /api/stats — light counters over the library. It lists
// all article metadata once and tallies by pipeline stage: in-progress (queued
// through enriching), ready (enriched), and failed (either stage).
func (s *Server) getStats(c fiber.Ctx) error {
	metas, err := s.store.ListArticleMeta(c.Context(), time.Time{})
	if err != nil {
		return s.serverError(c, "list article meta", err)
	}

	stats := statsResponse{Total: len(metas)}
	for i := range metas {
		switch metas[i].Status {
		case model.StatusEnriched:
			stats.Ready++
		case model.StatusFetchFailed, model.StatusEnrichFailed:
			stats.Failed++
		default:
			// queued, fetching, fetched, enriching
			stats.InProgress++
		}
	}
	return c.JSON(stats)
}

// healthz handles GET /healthz with an unauthenticated 200. It does not touch
// the store so it stays a cheap liveness probe for docker-compose healthcheck.
func (s *Server) healthz(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok", "version": version.Version})
}

// serverInfoFromConfig maps non-secret config fields into the wire type.
func serverInfoFromConfig(cfg *config.Config) model.ServerInfo {
	return model.ServerInfo{
		HTTPPort:     cfg.HTTPPort,
		DatabasePath: cfg.DatabasePath,

		TrustProxy:     cfg.TrustProxy,
		TrustedProxies: cfg.TrustedProxies,

		LoginMaxAttempts:     cfg.LoginMaxAttempts,
		LoginAttemptWindow:   cfg.LoginAttemptWindow.String(),
		LoginLockoutDuration: cfg.LoginLockoutDuration.String(),

		LLMAPIBaseURL:          cfg.LLMAPIBaseURL,
		LLMModel:               cfg.LLMModel,
		LLMMaxConcurrent:       cfg.LLMMaxConcurrent,
		LLMRequestTimeout:      cfg.LLMRequestTimeout.String(),
		LLMMaxRetries:          cfg.LLMMaxRetries,
		ReadabilityTimeout:     cfg.ReadabilityTimeout.String(),
		EnrichmentVersion:      cfg.EnrichmentVersion,
		MarkdownEnabled:        cfg.MarkdownEnabled,
		MarkdownBaseURL:        cfg.MarkdownBaseURL,
		MarkdownTimeout:        cfg.MarkdownTimeout.String(),
		MarkdownDailyLimit:     cfg.MarkdownDailyLimit,
		MarkdownCostPerArticle: cfg.MarkdownCostPerArticle,
		LogLevel:               cfg.LogLevel,
		LogFormat:              cfg.LogFormat,
		Version:                version.Version,
	}
}

// progressRequest is the PUT /api/articles/:id/progress body. ArticleID comes
// from the path, not the body, so it is intentionally omitted here.
type progressRequest struct {
	Position  int       `json:"position"`
	IsRead    bool      `json:"is_read"`
	UpdatedAt time.Time `json:"updated_at"`
}

// progressResponse reports the LWW outcome of a progress upsert.
type progressResponse struct {
	Applied bool `json:"applied"`
}

// pinRequest is the PUT /api/articles/:id/pin body. The id comes from the path.
type pinRequest struct {
	Pinned bool `json:"pinned"`
}

// statsResponse is the GET /api/stats body. InProgress counts articles in any
// non-terminal pipeline stage (queued/fetching/fetched/enriching), Ready counts
// enriched articles, and Failed counts either-stage failures.
type statsResponse struct {
	Total      int `json:"total"`
	InProgress int `json:"in_progress"`
	Ready      int `json:"ready"`
	Failed     int `json:"failed"`
}

// parseSince parses the optional ?since query value as RFC3339. An empty value
// yields the zero time (full sync) with no error.
func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// serverError logs an unexpected store/dependency failure and returns a 500
// without leaking internals to the client.
func (s *Server) serverError(c fiber.Ctx, op string, err error) error {
	s.log.Error("request failed", slog.String("op", op), slog.Any("error", err))
	return sendError(c, fiber.StatusInternalServerError, "internal server error")
}
