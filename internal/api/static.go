package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// indexFile is the SPA entry document served as the fallback for client-side
// routes (e.g. /article/abc) so a hard refresh still boots the PWA.
const indexFile = "index.html"

// cacheNoCache marks responses that must be revalidated with the server before
// reuse (the HTML shell, the service worker, the manifest). "no-cache" means
// "store but revalidate", not "do not store".
const cacheNoCache = "no-cache"

// registerStatic mounts the embedded PWA at the origin root WITHOUT auth.
//
// Concrete assets (e.g. /_app/..., /manifest.webmanifest) are served directly
// from the embedded FS with correct content-types. Any GET navigation that does
// not resolve to a real file falls back to index.html so client-side routing
// works on refresh. Requests under /api or /healthz are never handled here:
// those routes are registered before the static mount and short-circuit it.
func registerStatic(app *fiber.App, siteFS fs.FS) {
	fallback := newSPAFallback(siteFS)

	// Strip conditional validators on the no-cache shell before the static
	// middleware sees them. The embedded FS (go:embed) reports a zero modtime
	// (year 0001), so Fiber's static handler answers EVERY If-Modified-Since
	// with 304 Not Modified — even after a new build. The browser then keeps
	// its stale index.html / service-worker.js across deploys: the update
	// banner reappears after each reload and never sticks. Forcing a full 200
	// for these tiny, must-revalidate files fixes it; content-hashed
	// /_app/immutable/ assets keep their (correct) 304 revalidation.
	app.Use("/", func(c fiber.Ctx) error {
		if cacheControlFor(c.Path()) == cacheNoCache {
			c.Request().Header.Del(fiber.HeaderIfModifiedSince)
			c.Request().Header.Del(fiber.HeaderIfNoneMatch)
		}
		return c.Next()
	})

	app.Use("/", static.New("", static.Config{
		FS:              siteFS,
		IndexNames:      []string{indexFile},
		NotFoundHandler: fallback,
		// Cache-Control is assigned per path (see cacheControlFor) instead of a
		// blanket MaxAge. A one-year cache on index.html / service-worker.js is
		// what makes the PWA "update available" banner reappear forever: the
		// browser keeps serving a stale shell that points at old asset hashes, so
		// new builds never take effect. Only fingerprinted /_app/immutable/ files
		// may be cached long-term. See docs/nginx.md.
		ModifyResponse: func(c fiber.Ctx) error {
			c.Set(fiber.HeaderCacheControl, cacheControlFor(c.Path()))
			return nil
		},
	}))
}

// cacheControlFor returns the Cache-Control value for a static path. Only
// content-hashed build assets are cached long-term; the HTML shell, the service
// worker and the manifest must be revalidated so updates roll out promptly.
func cacheControlFor(path string) string {
	switch {
	case strings.HasPrefix(path, "/_app/immutable/"):
		// Fingerprinted by SvelteKit — the URL changes when the content does.
		return "public, max-age=31536000, immutable"
	case path == "/service-worker.js" || path == "/manifest.webmanifest":
		return cacheNoCache
	case hasFileExtension(path):
		// Non-fingerprinted assets (icons, robots.txt): brief cache, revalidated.
		return "public, max-age=3600"
	default:
		// The HTML shell and SPA routes must always reflect the latest build.
		return cacheNoCache
	}
}

// newSPAFallback returns a handler that serves index.html for GET/HEAD
// navigations whose path did not match a static file, enabling SPA deep-link
// refreshes. Non-GET requests, asset-looking paths (those with a file
// extension), and a missing index.html all yield 404 so genuinely-absent
// resources are not masked.
func newSPAFallback(siteFS fs.FS) fiber.Handler {
	index, err := fs.ReadFile(siteFS, indexFile)
	indexAvailable := err == nil

	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return sendError(c, fiber.StatusNotFound, "not found")
		}
		// A path segment with a dot (e.g. /favicon.png, /robots.txt) is an asset
		// request, not a client route; do not mask its absence with the SPA shell.
		if hasFileExtension(c.Path()) {
			return sendError(c, fiber.StatusNotFound, "not found")
		}
		if !indexAvailable {
			// No frontend build embedded yet (placeholder .gitkeep only). Keep
			// the server usable for API-only operation.
			return sendError(c, fiber.StatusNotFound, "frontend not built")
		}
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		// The shell must be revalidated; it references per-build asset hashes.
		c.Set(fiber.HeaderCacheControl, cacheNoCache)
		return c.Status(http.StatusOK).Send(index)
	}
}

// hasFileExtension reports whether the last path segment contains a dot,
// indicating a concrete file (asset) rather than a client-side route.
func hasFileExtension(path string) bool {
	slash := strings.LastIndexByte(path, '/')
	last := path[slash+1:]
	return strings.ContainsRune(last, '.')
}
