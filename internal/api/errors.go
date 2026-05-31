package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/extract"
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
// SSRF / unparseable / too-large / malformed-URL conditions are client errors
// (4xx): the user supplied a URL we will not or cannot ingest. Everything else
// is treated as a server-side failure (5xx). The boolean reports whether the
// error was recognised as a client error; callers may log 5xx cases.
func mapAddError(err error) (status int, msg string) {
	switch {
	case errors.Is(err, extract.ErrBlockedHost):
		return fiber.StatusUnprocessableEntity, "URL host is not allowed"
	case errors.Is(err, extract.ErrUnparseable):
		return fiber.StatusUnprocessableEntity, "could not extract article content from URL"
	case errors.Is(err, extract.ErrTooLarge):
		return fiber.StatusUnprocessableEntity, "article content is too large"
	case errors.Is(err, ports.ErrNotFound):
		return fiber.StatusNotFound, "not found"
	}
	// NormalizeURL failures (empty URL, no host, parse error) are wrapped with
	// fmt.Errorf and carry no sentinel; classify them as a bad request via the
	// dedicated helper below.
	if isBadURL(err) {
		return fiber.StatusBadRequest, "invalid URL"
	}
	return fiber.StatusBadGateway, "failed to fetch or extract the article"
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
	for _, frag := range []string{"normalize URL", "URL has no host", "empty URL", "parse:"} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}
