// Browser Sentry initialisation and the project's manual-capture policy.
//
// The DSN can arrive two ways:
//
//  1. Build-time (eager) — an optional `VITE_SENTRY_DSN` baked into the SPA. We
//     init from it on module load so that boot / first-contact failures (errors
//     thrown before the first GET /api/config resolves) are still reported.
//
//  2. Runtime (reconfigure) — the canonical, per-deployment DSN delivered via
//     GET /api/config (see `SentryConfig`). The SPA is built once and embedded in
//     the Go binary while the DSN is set per-deployment via SENTRY_FRONTEND_DSN,
//     so `initSentry` reconfigures the SDK when that DSN differs from the env one.
//
// On top of init, this module is the single home for the manual-capture policy
// the non-UI layers (sync engine, db, api, service worker, bootstrap) consume.
// Those layers handle their own errors with early returns / console logging that
// never reaches the SDK's global onerror handler, so the most failure-prone
// subsystem (offline sync / IndexedDB) is otherwise invisible in production. The
// helpers below give it a deliberate, scrubbed path to Sentry.

import * as Sentry from '@sentry/sveltekit';
import type { SentryConfig } from '$lib/types';

/** Logical subsystem an event came from — becomes the `area` tag. */
export type SentryArea = 'sync' | 'idb' | 'api' | 'sw' | 'reader' | 'bootstrap' | 'auth' | 'ui';

/**
 * The event type `beforeSend` receives, derived straight from the installed
 * SDK's `init` signature so it stays correct across Sentry versions without
 * naming a transitively re-exported type.
 */
type SentryEvent = Parameters<
	NonNullable<NonNullable<Parameters<typeof Sentry.init>[0]>['beforeSend']>
>[0];

/** Max length we let a single string field reach before truncating (finding 3). */
const MAX_STRING_LEN = 2000;

// The DSN the SDK is currently configured with (`''` when not configured). We
// track it rather than a plain boolean so a runtime config carrying a different
// DSN than the build-time one re-initialises the SDK.
let activeDsn = '';

/** Whether the SDK is configured and manual captures should be forwarded. */
function isConfigured(): boolean {
	return activeDsn !== '' && typeof window !== 'undefined';
}

/**
 * Remove sensitive material before any event leaves the browser (finding 3).
 * Bearer session tokens (Authorization headers) and pasted article text / form
 * bodies can otherwise reach Sentry via manual captures. Runs for every event,
 * automatic or manual.
 */
function scrub(event: Record<string, unknown>): Record<string, unknown> {
	const request = event.request as Record<string, unknown> | undefined;
	if (request) {
		const headers = request.headers as Record<string, unknown> | undefined;
		if (headers) {
			for (const key of Object.keys(headers)) {
				if (key.toLowerCase() === 'authorization') delete headers[key];
			}
		}
		// Request bodies may carry credentials or full article text — never send.
		delete request.data;
	}

	const extra = event.extra as Record<string, unknown> | undefined;
	if (extra) {
		for (const key of Object.keys(extra)) {
			const value = extra[key];
			if (typeof value === 'string' && value.length > MAX_STRING_LEN) {
				extra[key] = `${value.slice(0, MAX_STRING_LEN)}…[truncated]`;
			}
		}
	}

	return event;
}

/** Build the shared `Sentry.init` options for a given DSN/env/release. */
function initOptions(dsn: string, environment: string, release: string) {
	return {
		dsn,
		environment: environment || undefined,
		release: release || undefined,
		// Errors only: no performance traces.
		tracesSampleRate: 0,
		// Do not auto-attach IPs / cookies / request bodies. We add minimal context
		// ourselves (see setSentryUser) and scrub the rest in beforeSend.
		sendDefaultPii: false,
		beforeSend: (event: SentryEvent): SentryEvent =>
			scrub(event as unknown as Record<string, unknown>) as unknown as SentryEvent
	};
}

function configure(dsn: string, environment: string, release: string): void {
	if (typeof window === 'undefined') return;
	if (!dsn || dsn === activeDsn) return;
	activeDsn = dsn;
	Sentry.init(initOptions(dsn, environment, release));
}

// Eager build-time init (finding 2). Reads an optional baked-in DSN so errors
// during early framework boot — before the first /api/config — are captured. A
// no-op when the env DSN is unset (the common case: the runtime DSN takes over).
const envDsn = String(import.meta.env.VITE_SENTRY_DSN ?? '');
if (envDsn) {
	configure(
		envDsn,
		String(import.meta.env.VITE_SENTRY_ENVIRONMENT ?? ''),
		String(import.meta.env.VITE_SENTRY_RELEASE ?? '')
	);
}

/**
 * (Re)configure the browser Sentry SDK from the server-provided config. Safe to
 * call repeatedly (e.g. on every `refreshAuth`): it no-ops when the DSN is empty
 * (reporting disabled), unchanged from the currently-active DSN, or when not
 * running in a browser, and re-initialises when the runtime DSN differs from the
 * build-time one.
 *
 * Errors only: `tracesSampleRate` is 0, so no performance traces are sent. The
 * default integrations install the global `onerror` / `unhandledrejection`
 * handlers; SvelteKit's own error boundary is wired up separately in
 * `hooks.client.ts`.
 */
export function initSentry(cfg: SentryConfig | undefined): void {
	if (!cfg?.dsn) return;
	configure(cfg.dsn, cfg.environment, cfg.release);
}

/** Options shared by the manual-capture helpers. */
export interface CaptureContext {
	/** Subsystem tag so events can be filtered by failure area. */
	area: SentryArea;
	/** Structured detail (http status, outbox kind, ids…). Scrubbed before send. */
	extra?: Record<string, unknown>;
}

/**
 * Report an error from a non-UI layer that swallows its own failures. No-op when
 * Sentry is not configured, so callers can call it unconditionally. The original
 * `console.error`/`console.warn` at the call site should be kept for local
 * debugging.
 */
export function captureError(error: unknown, ctx: CaptureContext): void {
	if (!isConfigured()) return;
	Sentry.captureException(error, { tags: { area: ctx.area }, extra: ctx.extra });
}

/**
 * Report a noteworthy non-error condition (e.g. `persist() === false`, a dropped
 * outbox entry described as a message). No-op when Sentry is not configured.
 */
export function captureWarning(message: string, ctx: CaptureContext): void {
	if (!isConfigured()) return;
	Sentry.captureMessage(message, { level: 'warning', tags: { area: ctx.area }, extra: ctx.extra });
}

/**
 * Leave a `sync`-category breadcrumb (sync start/finish, dispatch). Breadcrumbs
 * give the trail leading up to a later captured error. No-op when not configured.
 */
export function addSyncBreadcrumb(message: string, data?: Record<string, unknown>): void {
	if (!isConfigured()) return;
	Sentry.addBreadcrumb({ category: 'sync', message, level: 'info', data });
}

/**
 * Associate subsequent events with the logged-in user. Single-user app, so the
 * username is the only identifier we attach — no email / IP (sendDefaultPii is
 * false). No-op when Sentry is not configured.
 */
export function setSentryUser(username: string): void {
	if (!isConfigured()) return;
	Sentry.setUser({ username });
}

/** Clear the associated user on logout / session expiry. */
export function clearSentryUser(): void {
	if (!isConfigured()) return;
	Sentry.setUser(null);
}
