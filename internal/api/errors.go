package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/ports"
)

// apiError is the JSON body returned for any non-2xx API response. The shape is
// stable so the PWA can surface a message without guessing at the structure.
type apiError struct {
	Error string `json:"error"`
}

// sendError writes status + a JSON apiError body. It is the single place
// handlers funnel error responses through so the wire format stays consistent.
func sendError(c fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(apiError{Error: msg})
}

// mapAddError translates an ingest.Add error into an HTTP status + message.
//
// Add no longer fetches content (that happens asynchronously in the worker, so
// fetch failures surface as the fetch_failed stage on the article rather than
// here). The only client error Add can return is a malformed URL; everything
// else is a server-side failure (5xx).
func mapAddError(err error) (status int, msg string) {
	if errors.Is(err, ports.ErrNotFound) {
		return fiber.StatusNotFound, "not found"
	}
	// NormalizeURL failures (empty URL, no host, parse error) are wrapped with
	// fmt.Errorf and carry no sentinel; classify them as a bad request via the
	// dedicated helper below.
	if isBadURL(err) {
		return fiber.StatusBadRequest, "invalid URL"
	}
	return fiber.StatusInternalServerError, "failed to add the article"
}

// isBadURL reports whether err looks like a URL-normalisation failure from the
// ingest pipeline (which wraps with "ingest: normalize URL: ..."). These are
// client errors. We match on the wrapped message because the ingest package
// returns plain fmt.Errorf values without a sentinel.
func isBadURL(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, frag := range []string{"normalize URL", "URL has no host", "empty URL", "empty text", "parse:"} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}
