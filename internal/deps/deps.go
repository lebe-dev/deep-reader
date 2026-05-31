// Package deps is a dependency anchor. It blank-imports the project's required
// third-party modules so that `go mod tidy` keeps them as direct requirements
// in go.mod even before the feature packages that use them exist.
//
// This is necessary because downstream agents author code against these
// dependencies in parallel but are not permitted to run `go get` / `go mod
// tidy`; the modules must already be present and resolved. Once every feature
// package imports its dependencies for real, this anchor may be removed.
package deps

import (
	_ "github.com/go-shiori/go-readability"
	_ "github.com/gofiber/fiber/v3"
	_ "github.com/oklog/ulid/v2"
	_ "github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)
