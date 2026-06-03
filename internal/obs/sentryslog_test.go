package obs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// recordingTransport captures events synchronously so tests can assert on them
// without flushing a background transport.
type recordingTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *recordingTransport) Configure(sentry.ClientOptions)        {}
func (t *recordingTransport) Flush(time.Duration) bool              { return true }
func (t *recordingTransport) FlushWithContext(context.Context) bool { return true }
func (t *recordingTransport) Close()                                {}
func (t *recordingTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func (t *recordingTransport) captured() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// newHub builds a hub backed by a recording transport for assertions.
func newHub(t *testing.T) (*sentry.Hub, *recordingTransport) {
	t.Helper()
	transport := &recordingTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:       "https://test@example.com/1",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return sentry.NewHub(client, sentry.NewScope()), transport
}

func newLogger(hub *sentry.Hub) *slog.Logger {
	base := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewSentryHandler(base, WithHub(hub)))
}

func TestErrorAttrIsCapturedAsException(t *testing.T) {
	hub, transport := newHub(t)
	log := newLogger(hub)

	log.Error("enrich: permanent error", "err", errors.New("context deadline exceeded"))

	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Level != sentry.LevelError {
		t.Errorf("level = %q, want error", ev.Level)
	}
	if len(ev.Exception) == 0 {
		t.Fatalf("want an exception entry, got none")
	}
	if got := ev.Exception[len(ev.Exception)-1].Value; got != "context deadline exceeded" {
		t.Errorf("exception value = %q", got)
	}
	// Message carries both the log message and the error so backends that
	// ignore the exception list still render a meaningful title.
	if want := "enrich: permanent error: context deadline exceeded"; ev.Message != want {
		t.Errorf("message = %q, want %q", ev.Message, want)
	}
	if ev.Exception[len(ev.Exception)-1].Stacktrace == nil {
		t.Errorf("want a stack trace on the outermost exception")
	}
}

func TestScopedAttrsAreAttachedToContext(t *testing.T) {
	hub, transport := newHub(t)
	log := newLogger(hub).With("worker", 0, "article_id", "01KT75")

	log.Error("boom", "err", errors.New("nope"))

	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	logCtx := events[0].Contexts["log"]
	if logCtx["article_id"] != "01KT75" {
		t.Errorf("article_id = %v, want 01KT75", logCtx["article_id"])
	}
	if logCtx["worker"] != int64(0) {
		t.Errorf("worker = %v (%T), want int64(0)", logCtx["worker"], logCtx["worker"])
	}
	// Scalars are also tags so backends that hide custom contexts still expose
	// them for display and search.
	if events[0].Tags["article_id"] != "01KT75" {
		t.Errorf("article_id tag = %q, want 01KT75", events[0].Tags["article_id"])
	}
	if events[0].Tags["worker"] != "0" {
		t.Errorf("worker tag = %q, want 0", events[0].Tags["worker"])
	}
}

func TestMessageWithoutErrorIsCapturedAsMessage(t *testing.T) {
	hub, transport := newHub(t)
	log := newLogger(hub)

	log.Error("plain failure")

	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Message != "plain failure" {
		t.Errorf("message = %q", events[0].Message)
	}
	if len(events[0].Exception) != 0 {
		t.Errorf("want no exception entry, got %d", len(events[0].Exception))
	}
}

func TestSubErrorLevelsAreNotForwarded(t *testing.T) {
	hub, transport := newHub(t)
	log := newLogger(hub)

	log.Info("info")
	log.Warn("warn", "err", errors.New("transient"))
	log.Debug("debug")

	if n := len(transport.captured()); n != 0 {
		t.Fatalf("want 0 forwarded events, got %d", n)
	}
}

func TestNoClientIsNoop(t *testing.T) {
	// A hub without a client mirrors "Sentry not configured": nothing is sent
	// and nothing panics.
	hub := sentry.NewHub(nil, sentry.NewScope())
	base := slog.NewTextHandler(io.Discard, nil)
	log := slog.New(NewSentryHandler(base, WithHub(hub)))

	log.Error("should be dropped", "err", errors.New("x"))
}
