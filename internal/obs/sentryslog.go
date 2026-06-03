// Package obs wires observability concerns that sit between the application
// and third-party services. Its SentryHandler forwards high-severity slog
// records to Sentry without changing how the rest of the code logs.
package obs

import (
	"context"
	"log/slog"

	"github.com/getsentry/sentry-go"
)

// forwardLevel is the minimum slog level forwarded to Sentry. ERROR and above
// are worth an alert; INFO/WARN stay in the log stream only.
const forwardLevel = slog.LevelError

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

// capture builds and sends a Sentry event from a record. An error-typed
// attribute (err/error) becomes a CaptureException for proper grouping;
// otherwise the message is sent via CaptureMessage. All other attributes are
// attached as a "log" context block for triage.
func (h *SentryHandler) capture(r slog.Record) {
	var captured error
	fields := sentry.Context{"message": r.Message}

	collect := func(a slog.Attr) {
		if captured == nil && errorKeys[a.Key] {
			if e, ok := a.Value.Any().(error); ok {
				captured = e
				return
			}
		}
		fields[a.Key] = a.Value.Any()
	}
	for _, a := range h.attrs {
		collect(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		collect(a)
		return true
	})

	hub := h.hub
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentryLevel(r.Level))
		scope.SetContext("log", fields)
		if captured != nil {
			hub.CaptureException(captured)
			return
		}
		hub.CaptureMessage(r.Message)
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
