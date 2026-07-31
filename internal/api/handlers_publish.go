package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/publish"
)

// publishArticle handles POST /api/articles/:id/publish: it renders the
// article's translated text into a standalone HTML file, stores it under a
// fresh unguessable token, and records the publication with the TTL currently
// configured in settings.
//
// Only enriched articles can be published — there is nothing to show for an
// article the LLM has not translated yet.
func (s *Server) publishArticle(c fiber.Ctx) error {
	if s.pub == nil {
		return sendError(c, fiber.StatusServiceUnavailable, "public pages are not available: the page directory could not be opened")
	}

	id := c.Params("id")
	ctx := c.Context()

	var req model.PublishRequest
	if err := c.Bind().JSON(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}

	article, err := s.store.GetArticle(ctx, id)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article not found")
		}
		return s.serverError(c, "publish: get article", err)
	}

	payload, err := s.store.GetArticlePayload(ctx, id)
	if err != nil {
		return s.serverError(c, "publish: get article payload", err)
	}
	if payload.Status != model.StatusEnriched || payload.Enrichment == nil {
		return sendError(c, fiber.StatusConflict, "article is not enriched yet")
	}

	title, description, err := publicationMetadata(req, article)
	if err != nil {
		return sendError(c, fiber.StatusBadRequest, err.Error())
	}

	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return s.serverError(c, "publish: get settings", err)
	}

	token, err := publish.NewToken()
	if err != nil {
		return s.serverError(c, "publish: mint token", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	pub := model.Publication{
		Token:       token,
		URL:         s.publicPageURL(c, token),
		ArticleID:   article.ID,
		Title:       title,
		Description: description,
		PublishedAt: now,
		ExpiresAt:   expiryFor(now, settings.PublicPageTTLHours),
	}

	html, err := publish.RenderPage(publish.Page{
		Title:        pub.Title,
		Description:  pub.Description,
		URL:          pub.URL,
		Author:       article.Author,
		SourceURL:    article.SourceURL,
		SourceDomain: article.SourceDomain,
		Lang:         settings.TargetLanguage,
		SourceLang:   article.Lang,
		PublishedAt:  pub.PublishedAt,
		Blocks:       publish.Blocks(payload),
	})
	if err != nil {
		return s.serverError(c, "publish: render page", err)
	}

	// Write the file first: a page on disk with no record is invisible (and the
	// orphan sweep collects it), whereas a record with no page would hand out a
	// link that 404s.
	if err := s.pub.Write(token, html); err != nil {
		return s.serverError(c, "publish: write page", err)
	}

	previous, err := s.store.ReplacePublication(ctx, pub)
	if err != nil {
		if rerr := s.pub.Remove(token); rerr != nil {
			s.log.Warn("could not remove page file after a failed publish", slog.Any("error", rerr))
		}
		return s.serverError(c, "publish: record publication", err)
	}
	if previous != "" && previous != token {
		if err := s.pub.Remove(previous); err != nil {
			s.log.Warn("could not remove superseded page file", slog.Any("error", err))
		}
	}

	s.log.Info("article published",
		slog.String("article_id", article.ID),
		slog.Time("expires_at", pub.ExpiresAt),
	)
	return c.Status(fiber.StatusCreated).JSON(pub)
}

// getPublication handles GET /api/articles/:id/publish, reporting the article's
// current share link so the reader can show its state on load.
func (s *Server) getPublication(c fiber.Ctx) error {
	pub, err := s.store.GetPublicationByArticle(c.Context(), c.Params("id"))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article is not published")
		}
		return s.serverError(c, "get publication", err)
	}

	// An expired publication is indistinguishable from an unpublished article
	// for the owner too: the link no longer works, so the UI should offer to
	// publish again rather than show a dead link.
	if pub.Expired(time.Now()) {
		return sendError(c, fiber.StatusNotFound, "article is not published")
	}

	pub.URL = s.publicPageURL(c, pub.Token)
	return c.JSON(pub)
}

// unpublishArticle handles DELETE /api/articles/:id/publish: the link stops
// working immediately and its page file is deleted.
func (s *Server) unpublishArticle(c fiber.Ctx) error {
	token, err := s.store.DeletePublicationByArticle(c.Context(), c.Params("id"))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return sendError(c, fiber.StatusNotFound, "article is not published")
		}
		return s.serverError(c, "unpublish article", err)
	}

	if s.pub != nil {
		if err := s.pub.Remove(token); err != nil {
			// The record is already gone, so the link is dead either way; the
			// orphan sweep will collect the file.
			s.log.Warn("could not remove page file on unpublish", slog.Any("error", err))
		}
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// servePublicPage handles GET /p/:token — the only unauthenticated content
// route. It is a TTL check plus a file read: the page was fully rendered at
// publish time, so nothing is assembled per request.
func (s *Server) servePublicPage(c fiber.Ctx) error {
	token := c.Params("token")
	if s.pub == nil || !publish.ValidToken(token) {
		return s.sendPublicNotFound(c)
	}

	pub, err := s.store.GetPublication(c.Context(), token)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return s.sendPublicNotFound(c)
		}
		return s.serverError(c, "serve public page", err)
	}
	if pub.Expired(time.Now()) {
		return s.sendPublicNotFound(c)
	}

	html, err := s.pub.Read(token)
	if err != nil {
		if errors.Is(err, publish.ErrNotFound) {
			return s.sendPublicNotFound(c)
		}
		return s.serverError(c, "read public page", err)
	}

	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	// Revalidate rather than cache: unpublishing must take effect promptly even
	// with a proxy in front, and link-preview crawlers re-fetch anyway.
	c.Set(fiber.HeaderCacheControl, cacheNoCache)
	return c.Status(fiber.StatusOK).Send(html)
}

// sendPublicNotFound answers an unknown, expired or unpublished link. It is an
// HTML page rather than the API's JSON envelope because the audience is a
// person who followed a link, and it is deliberately a plain 404: an expired
// link should say no more than a wrong one.
func (s *Server) sendPublicNotFound(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	c.Set(fiber.HeaderCacheControl, cacheNoCache)
	return c.Status(fiber.StatusNotFound).Send(publish.NotFoundPage())
}

// publicPageURL builds the absolute share link. It prefers the configured
// public origin and otherwise derives one from the publishing request, which is
// correct behind the documented reverse proxy.
func (s *Server) publicPageURL(c fiber.Ctx, token string) string {
	base := s.cfg.PublicBaseURL
	if base == "" {
		base = strings.TrimRight(c.BaseURL(), "/")
	}
	return base + "/p/" + token
}

// publicationMetadata resolves and validates the page's title and description,
// falling back to the article's own title and summary when the client sends a
// field empty.
func publicationMetadata(req model.PublishRequest, a *model.Article) (title, description string, err error) {
	title = strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(a.Title)
	}
	if title == "" {
		return "", "", errors.New("title is required")
	}
	if len([]rune(title)) > model.MaxPublicationTitleLen {
		return "", "", errors.New("title is too long")
	}

	description = strings.TrimSpace(req.Description)
	if description == "" {
		description = strings.TrimSpace(a.Summary)
	}
	if len([]rune(description)) > model.MaxPublicationDescriptionLen {
		return "", "", errors.New("description is too long")
	}
	return title, description, nil
}

// expiryFor turns the configured TTL into an absolute deadline. A TTL of 0
// means the link never expires, represented as the zero time.
func expiryFor(now time.Time, ttlHours int) time.Time {
	if ttlHours <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(ttlHours) * time.Hour)
}

// sweepPublications drops publications whose TTL has elapsed and deletes every
// page file that no live publication points at — including those orphaned when
// an article was deleted and its row went with it (ON DELETE CASCADE).
//
// It is best-effort housekeeping: failures are logged, never fatal.
func (s *Server) sweepPublications(ctx context.Context) {
	if s.pub == nil {
		return
	}

	expired, err := s.store.PruneExpiredPublications(ctx, time.Now().UTC())
	if err != nil {
		s.log.Warn("pruning expired publications failed", slog.Any("error", err))
		return
	}
	for _, token := range expired {
		if err := s.pub.Remove(token); err != nil {
			s.log.Warn("removing an expired page file failed", slog.Any("error", err))
		}
	}

	live, err := s.store.ListPublicationTokens(ctx)
	if err != nil {
		s.log.Warn("listing live publications failed", slog.Any("error", err))
		return
	}
	orphans, err := s.pub.RemoveOrphans(live)
	if err != nil {
		s.log.Warn("sweeping orphaned page files failed", slog.Any("error", err))
	}

	if len(expired) > 0 || orphans > 0 {
		s.log.Info("public pages swept",
			slog.Int("expired", len(expired)),
			slog.Int("orphaned_files", orphans),
		)
	}
}

// SweepPublications runs one housekeeping pass over the published pages. The
// server runs it at startup and on a timer (see RunPublicationSweeper); it is
// exported so a caller can trigger it directly.
func (s *Server) SweepPublications(ctx context.Context) { s.sweepPublications(ctx) }

// publicationSweepInterval is how often expired pages are collected. Expiry is
// enforced on every request regardless — this only reclaims disk.
const publicationSweepInterval = time.Hour

// RunPublicationSweeper sweeps published pages once and then hourly until ctx
// is cancelled. Run it in its own goroutine.
func (s *Server) RunPublicationSweeper(ctx context.Context) {
	s.sweepPublications(ctx)

	ticker := time.NewTicker(publicationSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweepPublications(ctx)
		}
	}
}
