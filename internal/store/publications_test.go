package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// publishArticle seeds an article and publishes it, returning the publication.
func publishArticle(t *testing.T, s interface {
	CreateArticle(context.Context, *model.Article) error
	ReplacePublication(context.Context, model.Publication) (string, error)
}, url, token string, expiresAt time.Time) model.Publication {
	t.Helper()
	ctx := context.Background()

	a := makeArticle(url)
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	p := model.Publication{
		Token:       token,
		ArticleID:   a.ID,
		Title:       "Shared title",
		Description: "Shared description",
		PublishedAt: time.Now().UTC().Truncate(time.Second),
		ExpiresAt:   expiresAt,
	}
	if _, err := s.ReplacePublication(ctx, p); err != nil {
		t.Fatalf("ReplacePublication: %v", err)
	}
	return p
}

func TestPublicationRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	expires := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	want := publishArticle(t, s, "https://example.com/a", "tok-round-trip", expires)

	got, err := s.GetPublication(ctx, want.Token)
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if got.ArticleID != want.ArticleID || got.Title != want.Title || got.Description != want.Description {
		t.Fatalf("GetPublication returned %+v, want %+v", got, want)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Fatalf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}

	byArticle, err := s.GetPublicationByArticle(ctx, want.ArticleID)
	if err != nil {
		t.Fatalf("GetPublicationByArticle: %v", err)
	}
	if byArticle.Token != want.Token {
		t.Fatalf("GetPublicationByArticle token = %q, want %q", byArticle.Token, want.Token)
	}
}

func TestGetPublicationUnknownTokenIsNotFound(t *testing.T) {
	s := openStore(t)

	if _, err := s.GetPublication(context.Background(), "no-such-token"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetPublication of an unknown token = %v, want ErrNotFound", err)
	}
	if _, err := s.GetPublicationByArticle(context.Background(), "no-such-article"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetPublicationByArticle of an unpublished article = %v, want ErrNotFound", err)
	}
}

func TestReplacePublicationSupersedesThePreviousToken(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	first := publishArticle(t, s, "https://example.com/b", "tok-first", time.Now().UTC().Add(time.Hour))

	second := first
	second.Token = "tok-second"
	previous, err := s.ReplacePublication(ctx, second)
	if err != nil {
		t.Fatalf("ReplacePublication: %v", err)
	}
	if previous != "tok-first" {
		t.Fatalf("ReplacePublication returned previous token %q, want %q", previous, "tok-first")
	}

	// The superseded link must stop resolving, and the article must hold
	// exactly one publication.
	if _, err := s.GetPublication(ctx, "tok-first"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("superseded token still resolves: %v", err)
	}
	tokens, err := s.ListPublicationTokens(ctx)
	if err != nil {
		t.Fatalf("ListPublicationTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0] != "tok-second" {
		t.Fatalf("live tokens = %v, want [tok-second]", tokens)
	}
}

func TestDeletePublicationByArticleReturnsTheTokenToSweep(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	p := publishArticle(t, s, "https://example.com/c", "tok-delete", time.Now().UTC().Add(time.Hour))

	token, err := s.DeletePublicationByArticle(ctx, p.ArticleID)
	if err != nil {
		t.Fatalf("DeletePublicationByArticle: %v", err)
	}
	if token != p.Token {
		t.Fatalf("returned token %q, want %q", token, p.Token)
	}
	if _, err := s.GetPublication(ctx, p.Token); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("publication survived deletion: %v", err)
	}
	if _, err := s.DeletePublicationByArticle(ctx, p.ArticleID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestDeletingAnArticleRemovesItsPublication(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	p := publishArticle(t, s, "https://example.com/d", "tok-cascade", time.Now().UTC().Add(time.Hour))

	if err := s.DeleteArticle(ctx, p.ArticleID); err != nil {
		t.Fatalf("DeleteArticle: %v", err)
	}
	if _, err := s.GetPublication(ctx, p.Token); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("publication outlived its article: %v", err)
	}
}

func TestPruneExpiredPublications(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	nowish := time.Now().UTC().Truncate(time.Second)

	expired := publishArticle(t, s, "https://example.com/e1", "tok-expired", nowish.Add(-time.Hour))
	live := publishArticle(t, s, "https://example.com/e2", "tok-live", nowish.Add(time.Hour))
	// An expiry-less publication is never swept.
	forever := publishArticle(t, s, "https://example.com/e3", "tok-forever", time.Time{})

	pruned, err := s.PruneExpiredPublications(ctx, nowish)
	if err != nil {
		t.Fatalf("PruneExpiredPublications: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != expired.Token {
		t.Fatalf("pruned = %v, want [%s]", pruned, expired.Token)
	}

	if _, err := s.GetPublication(ctx, expired.Token); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("expired publication survived: %v", err)
	}
	for _, keep := range []model.Publication{live, forever} {
		if _, err := s.GetPublication(ctx, keep.Token); err != nil {
			t.Fatalf("publication %q was swept but should have been kept: %v", keep.Token, err)
		}
	}
}

func TestListArticleMetaCarriesTheLivePublicationToken(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	nowish := time.Now().UTC().Truncate(time.Second)

	live := publishArticle(t, s, "https://example.com/m1", "tok-meta-live", nowish.Add(time.Hour))
	expired := publishArticle(t, s, "https://example.com/m2", "tok-meta-expired", nowish.Add(-time.Hour))
	unpublished := makeArticle("https://example.com/m3")
	if err := s.CreateArticle(ctx, unpublished); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	metas, err := s.ListArticleMeta(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListArticleMeta: %v", err)
	}

	tokens := map[string]string{}
	for _, m := range metas {
		tokens[m.ID] = m.PublicToken
	}
	if got := tokens[live.ArticleID]; got != live.Token {
		t.Errorf("published article carries token %q, want %q", got, live.Token)
	}
	// An expired link is as good as none — the library must not offer it.
	if got := tokens[expired.ArticleID]; got != "" {
		t.Errorf("expired publication leaked token %q into the library", got)
	}
	if got := tokens[unpublished.ID]; got != "" {
		t.Errorf("unpublished article carries token %q, want none", got)
	}
}

func TestPublishingRidesTheDeltaSync(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// An article last touched an hour ago, so a client that synced half an hour
	// ago would not see it again — unless publishing bumps its updated_at.
	a := makeArticle("https://example.com/delta")
	a.CreatedAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	a.UpdatedAt = a.CreatedAt
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	since := time.Now().UTC().Add(-30 * time.Minute)
	if metas, err := s.ListArticleMeta(ctx, since); err != nil {
		t.Fatalf("ListArticleMeta: %v", err)
	} else if len(metas) != 0 {
		t.Fatalf("the article is in the delta before publishing: %+v", metas)
	}

	if _, err := s.ReplacePublication(ctx, model.Publication{
		Token:       "tok-delta",
		ArticleID:   a.ID,
		Title:       "Shared",
		PublishedAt: time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("ReplacePublication: %v", err)
	}

	metas, err := s.ListArticleMeta(ctx, since)
	if err != nil {
		t.Fatalf("ListArticleMeta: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != a.ID {
		t.Fatalf("publishing did not put the article in the delta: %+v", metas)
	}
	if metas[0].PublicToken != "tok-delta" {
		t.Fatalf("delta carries token %q, want tok-delta", metas[0].PublicToken)
	}

	// Revoking must reach the client as an article that is no longer published,
	// rather than leaving the stale token in the last delta the client saw.
	if _, err := s.DeletePublicationByArticle(ctx, a.ID); err != nil {
		t.Fatalf("DeletePublicationByArticle: %v", err)
	}

	metas, err = s.ListArticleMeta(ctx, since)
	if err != nil {
		t.Fatalf("ListArticleMeta after revoke: %v", err)
	}
	if len(metas) != 1 || metas[0].PublicToken != "" {
		t.Fatalf("revoking did not reach the delta as an unpublished article: %+v", metas)
	}
}

func TestSettingsCarryPublicPageTTL(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.PublicPageTTLHours != model.DefaultPublicPageTTLHours {
		t.Fatalf("seeded PublicPageTTLHours = %d, want %d", got.PublicPageTTLHours, model.DefaultPublicPageTTLHours)
	}

	ttl := 12
	updated, err := s.UpdateSettings(ctx, model.SettingsPatch{PublicPageTTLHours: &ttl})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if updated.PublicPageTTLHours != ttl {
		t.Fatalf("updated PublicPageTTLHours = %d, want %d", updated.PublicPageTTLHours, ttl)
	}

	reread, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after update: %v", err)
	}
	if reread.PublicPageTTLHours != ttl {
		t.Fatalf("persisted PublicPageTTLHours = %d, want %d", reread.PublicPageTTLHours, ttl)
	}
}
