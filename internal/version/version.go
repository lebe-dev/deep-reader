// Package version exposes the build version of the binary. The value is the
// content of the repository's VERSION file, injected at link time via
//
//	-ldflags "-X deep-reader/internal/version.Version=$(cat VERSION)"
//
// (see the Justfile build recipe and the Dockerfile). It defaults to "dev" for
// plain `go run ./cmd/server` builds where no ldflag is set.
package version

// Version is the project version, set at build time. Do not assign elsewhere.
var Version = "dev"
