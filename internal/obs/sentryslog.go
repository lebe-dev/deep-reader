// Package obs wires observability concerns that sit between the application
// and third-party services. Its SentryHandler forwards high-severity slog
// records to Sentry without changing how the rest of the code logs.
package obs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/getsentry/sentry-go"
)

// forwardLevel is the minimum slog level forwarded to Sentry. WARN and above
// are forwarded; this captures 4xx HTTP responses (logged at Warn) as well as
// errors.
const forwardLevel = slog.LevelWarn

// errorChainDepth bounds how deep a wrapped-error chain is unwound into Sentry
// exception entries; it mirrors sentry-go's own default.
const errorChainDepth = 100

// errorKeys are the attribute keys conventionally holding an error value
// (e.g. log.Error("...", "err", err)). When present, the record is sent as a
// Sentry exception (with a stack-aware grouping) rather than a flat message.
var errorKeys = map[string]bool{"err": true, "error": true}

// SentryHandler decorates another slog.Handler, forwarding ERROR-and-above
// records to Sentry while delegating all records to the wrapped handler.
//
// It is safe and cheap when Sentry is not configured: the gate is the presence
// of a client on the bound hub, so with no DSN nothing is built or sent. It is
// also non-blocking — sentry-go's transport enqueues events on a buffered
// channel and ships them from a background goroutine, dropping rather than
// blocking when the buffer is full. A slow or unreachable Sentry therefore
// never stalls logging; only the shutdown Flush waits, and that is bounded.
type SentryHandler struct {
	next  slog.Handler
	hub   *sentry.Hub
	attrs []slog.Attr
}

// Option configures a SentryHandler.
type Option func(*SentryHandler)

// WithHub binds the handler to a specific hub instead of the current global
// one. Primarily useful in tests.
func WithHub(hub *sentry.Hub) Option {
	return func(h *SentryHandler) { h.hub = hub }
}

// NewSentryHandler wraps next so that ERROR-and-above records are also reported
// to Sentry. By default it binds to sentry.CurrentHub(); because sentry.Init
// attaches the client to that same hub, the handler may be constructed before
// Sentry is initialised and still pick up the client once it exists.
func NewSentryHandler(next slog.Handler, opts ...Option) *SentryHandler {
	h := &SentryHandler{next: next, hub: sentry.CurrentHub()}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Enabled mirrors the wrapped handler; the Sentry forwarding never widens what
// gets logged, it only observes records the base handler already accepts.
func (h *SentryHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle forwards qualifying records to Sentry, then delegates to the wrapped
// handler. Forwarding is skipped entirely when the level is below the threshold
// or no Sentry client is bound, so the common path adds a single comparison.
func (h *SentryHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= forwardLevel && h.hub.Client() != nil {
		h.capture(r)
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs returns a handler that carries attrs both for the wrapped handler
// and for Sentry context. Accumulating them here lets us surface logger-scoped
// fields (worker, article_id, …) alongside the captured event.
func (h *SentryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	merged = append(merged, h.attrs...)
	merged = append(merged, attrs...)
	return &SentryHandler{next: h.next.WithAttrs(attrs), hub: h.hub, attrs: merged}
}

// WithGroup delegates to the wrapped handler. Group nesting is not reflected in
// Sentry's context keys — this codebase uses flat attributes — but the base
// handler still renders groups in the log output.
func (h *SentryHandler) WithGroup(name string) slog.Handler {
	return &SentryHandler{next: h.next.WithGroup(name), hub: h.hub, attrs: h.attrs}
}

// capture builds and sends a Sentry event from a record.
//
// We construct the event explicitly rather than calling CaptureException so the
// result is legible across Sentry-compatible backends: full Sentry groups on
// the exception chain and stack trace, while leaner backends that don't parse
// the Go SDK's exception list still get a readable title from Message. Scalar
// attributes go to tags — which such backends surface and index for search —
// and the full set is mirrored into a "log" context for richer UIs.
func (h *SentryHandler) capture(r slog.Record) {
	var captured error
	fields := sentry.Context{}
	tags := map[string]string{}

	collect := func(a slog.Attr) {
		if captured == nil && errorKeys[a.Key] {
			if e, ok := a.Value.Any().(error); ok {
				captured = e
				return
			}
		}
		v := a.Value.Any()
		fields[a.Key] = v
		tags[a.Key] = fmt.Sprintf("%v", v)
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		collect(a)
		return true
	})

	event := sentry.NewEvent()
	event.Level = sentryLevel(r.Level)
	if captured != nil {
		// Include the error in the title so it is visible even where the
		// exception list is ignored; the exception still drives grouping and
		// carries the stack trace on backends that read it.
		event.Message = r.Message + ": " + captured.Error()
		event.SetException(captured, errorChainDepth)
	} else {
		event.Message = r.Message
	}

	hub := h.hub
	hub.WithScope(func(scope *sentry.Scope) {
		if len(tags) > 0 {
			scope.SetTags(tags)
		}
		if len(fields) > 0 {
			scope.SetContext("log", fields)
		}
		hub.CaptureEvent(event)
	})
}

// sentryLevel maps a slog level onto the closest Sentry severity.
func sentryLevel(l slog.Level) sentry.Level {
	switch {
	case l >= slog.LevelError:
		return sentry.LevelError
	case l >= slog.LevelWarn:
		return sentry.LevelWarning
	case l >= slog.LevelInfo:
		return sentry.LevelInfo
	default:
		return sentry.LevelDebug
	}
}
