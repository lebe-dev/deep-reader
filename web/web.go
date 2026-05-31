// Package web embeds the built SvelteKit PWA into the Go binary so the single
// server self-serves the client from the same origin (required for the Service
// Worker and cookie-free bearer auth).
//
// The build pipeline (Justfile / Dockerfile) copies the SvelteKit build output
// into web/dist/ before `go build`. A committed web/dist/.gitkeep placeholder
// keeps this package compiling before any frontend build exists.
//
// There is no runtime static directory: static assets are served exclusively
// from this embedded filesystem (see ports.go INTEGRATION CONTRACT — the api
// package serves web.FS()).
package web

import (
	"embed"
	"io/fs"
)

// The all: prefix is required so SvelteKit's _app/ directory and dotfiles
// (e.g. .vite, hashed assets) are included in the embedded tree.
//
//go:embed all:dist
var dist embed.FS

// FS returns the embedded site rooted at the contents of dist/, so callers see
// a root-relative filesystem (index.html, _app/, manifest, etc.) without the
// leading "dist/" path segment.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// fs.Sub on a static embed.FS with a constant subdir cannot fail at
		// runtime; a failure means the embed directive is broken, which is a
		// build/programming error.
		panic("web: failed to sub embedded dist FS: " + err.Error())
	}
	return sub
}
