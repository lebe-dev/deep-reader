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

// registerStatic mounts the embedded PWA at the origin root WITHOUT auth.
//
// Concrete assets (e.g. /_app/..., /manifest.webmanifest) are served directly
// from the embedded FS with correct content-types. Any GET navigation that does
// not resolve to a real file falls back to index.html so client-side routing
// works on refresh. Requests under /api or /healthz are never handled here:
// those routes are registered before the static mount and short-circuit it.
func registerStatic(app *fiber.App, siteFS fs.FS) {
	fallback := newSPAFallback(siteFS)

	app.Use("/", static.New("", static.Config{
		FS:              siteFS,
		IndexNames:      []string{indexFile},
		NotFoundHandler: fallback,
		// One year for hashed assets is safe; index.html itself is revalidated
		// by the service worker, and SvelteKit fingerprints _app/ assets.
		MaxAge: 31536000,
	}))
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
