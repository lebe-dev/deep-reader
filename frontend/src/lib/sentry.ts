// Browser Sentry initialisation.
//
// The DSN is delivered at runtime via GET /api/config (see `SentryConfig`), not
// baked at build time — the SPA is built once and embedded in the Go binary,
// while the DSN is set per-deployment via the SENTRY_FRONTEND_DSN env var. So we
// initialise lazily, once the first config response arrives.
//
// Errors thrown before that first response (early framework boot) are not
// captured — an accepted trade-off for runtime configurability.

import * as Sentry from '@sentry/sveltekit';
import type { SentryConfig } from '$lib/types';

let initialized = false;

/**
 * Initialise the browser Sentry SDK from the server-provided config. Idempotent
 * and safe to call repeatedly (e.g. on every `refreshAuth`); does nothing when
 * the DSN is empty (reporting disabled) or when not running in a browser.
 *
 * Errors only: `tracesSampleRate` is 0, so no performance traces are sent. The
 * default integrations install the global `onerror` / `unhandledrejection`
 * handlers; SvelteKit's own error boundary is wired up separately in
 * `hooks.client.ts`.
 */
export function initSentry(cfg: SentryConfig | undefined): void {
	if (initialized || !cfg?.dsn || typeof window === 'undefined') return;
	initialized = true;

	Sentry.init({
		dsn: cfg.dsn,
		environment: cfg.environment || undefined,
		release: cfg.release || undefined,
		tracesSampleRate: 0
	});
}
