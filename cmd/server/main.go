// Command server is the Deep Reader backend entrypoint. It loads configuration
// from the environment, initialises structured logging, opens the SQLite store
// (running migrations), constructs the LLM client, extractor, enrichment worker
// pool and ingestion pipeline, builds the HTTP server (Fiber v3 + embedded PWA),
// starts the workers, and listens until SIGINT/SIGTERM triggers a graceful
// shutdown.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"deep-reader/internal/api"
	"deep-reader/internal/config"
	"deep-reader/internal/enrich"
	"deep-reader/internal/extract"
	"deep-reader/internal/ingest"
	"deep-reader/internal/llm"
	"deep-reader/internal/markdown"
	"deep-reader/internal/ports"
	"deep-reader/internal/store"
	"deep-reader/internal/version"
)

// shutdownTimeout bounds how long graceful shutdown waits for in-flight
// requests before forcing the server down.
const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		// Logging may not be configured yet; use the default logger.
		slog.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

// run performs startup, blocks on the server, and orchestrates graceful
// shutdown. Returning an error from here surfaces a non-zero exit code.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	slog.SetDefault(log)
	log.Info("starting deep-reader",
		slog.String("version", version.Version),
		slog.Int("http_port", cfg.HTTPPort),
		slog.String("database_path", cfg.DatabasePath),
		slog.String("llm_base_url", cfg.LLMAPIBaseURL),
		slog.String("llm_model", cfg.LLMModel),
		slog.Int("llm_max_concurrent", cfg.LLMMaxConcurrent),
		slog.Int("llm_max_retries", cfg.LLMMaxRetries),
		slog.Duration("llm_request_timeout", cfg.LLMRequestTimeout),
		slog.Duration("readability_timeout", cfg.ReadabilityTimeout),
		slog.Int("enrichment_version", cfg.EnrichmentVersion),
		slog.String("log_level", cfg.LogLevel),
		slog.String("log_format", cfg.LogFormat),
	)

	// rootCtx is cancelled on the first SIGINT/SIGTERM; it drives both the
	// worker pool lifetime and the shutdown sequence.
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.NewSQLite(rootCtx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := st.Close(); cerr != nil {
			log.Error("store close failed", slog.Any("error", cerr))
		}
	}()

	llmClient := llm.New(cfg)

	// Extraction: readability is always available; when markdown.new is enabled
	// it becomes the primary source with readability as the budgeted fallback.
	readabilityExtractor := extract.New(cfg)
	var extractor ports.Extractor = readabilityExtractor
	if cfg.MarkdownEnabled {
		extractor = markdown.NewChain(markdown.New(cfg), readabilityExtractor, st, cfg)
		log.Info("markdown.new extraction enabled",
			slog.String("base_url", cfg.MarkdownBaseURL),
			slog.Int("daily_limit", cfg.MarkdownDailyLimit),
			slog.Int("cost_per_article", cfg.MarkdownCostPerArticle),
		)
	}

	pool := enrich.NewPool(cfg, st, llmClient)
	ingestor := ingest.New(cfg, st, extractor, pool)
	log.Debug("components initialised: llm client, extractor, enrichment pool, ingestor")

	// Start the worker pool in its own goroutine; it blocks until rootCtx is
	// cancelled.
	go pool.Start(rootCtx)

	srv := api.New(cfg, st, ingestor, api.WithLogger(log))

	// Serve in a goroutine so main can wait on the signal context.
	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", slog.Int("port", cfg.HTTPPort))
		serveErr <- srv.Listen()
	}()

	select {
	case err := <-serveErr:
		// Listen returned before a signal — propagate the failure.
		return err
	case <-rootCtx.Done():
		log.Info("shutdown signal received; draining")
	}

	if err := srv.Shutdown(shutdownTimeout); err != nil {
		log.Error("graceful shutdown failed", slog.Any("error", err))
		return err
	}
	log.Info("shutdown complete")
	return nil
}

// newLogger builds a slog.Logger honouring cfg.LogLevel (debug|info|warn|error)
// and cfg.LogFormat (json|text), writing to stderr.
func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}

	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}

// parseLevel maps the config log level string to a slog.Level. Validation in
// config.Load guarantees one of the four values, so the default is unreachable
// in practice but kept for safety.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
