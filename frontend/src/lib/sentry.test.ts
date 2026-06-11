import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SentryConfig } from '$lib/types';

// Mock the SDK so the tests assert how we call it, without a real transport.
const init = vi.fn();
const captureException = vi.fn();
const captureMessage = vi.fn();
const addBreadcrumb = vi.fn();
const setUser = vi.fn();
vi.mock('@sentry/sveltekit', () => ({
	init,
	captureException,
	captureMessage,
	addBreadcrumb,
	setUser
}));

type SentryModule = typeof import('./sentry');

// Env keys the module reads for the eager build-time init.
const ENV_KEYS = ['VITE_SENTRY_DSN', 'VITE_SENTRY_ENVIRONMENT', 'VITE_SENTRY_RELEASE'] as const;

function env(): Record<string, unknown> {
	return import.meta.env as unknown as Record<string, unknown>;
}

function clearEnv() {
	for (const k of ENV_KEYS) delete env()[k];
}

// Fresh module per test so the module-level `initialized` guard resets and the
// eager env-init re-runs against the current `import.meta.env`.
async function freshModule(): Promise<SentryModule> {
	vi.resetModules();
	init.mockClear();
	captureException.mockClear();
	captureMessage.mockClear();
	addBreadcrumb.mockClear();
	setUser.mockClear();
	return import('./sentry');
}

const fullConfig: SentryConfig = {
	dsn: 'https://public@example.ingest.sentry.io/1',
	environment: 'test',
	release: '1.2.3'
};

describe('initSentry', () => {
	// Sentry only runs in a browser; the vitest env is node, so provide a minimal
	// window stub and a clean env before each test.
	beforeEach(() => {
		vi.stubGlobal('window', {});
		clearEnv();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		clearEnv();
	});

	it('does nothing when the DSN is empty (reporting disabled)', async () => {
		const { initSentry } = await freshModule();
		initSentry({ dsn: '', environment: 'test', release: '1.2.3' });
		expect(init).not.toHaveBeenCalled();
	});

	it('does nothing when the config is undefined', async () => {
		const { initSentry } = await freshModule();
		initSentry(undefined);
		expect(init).not.toHaveBeenCalled();
	});

	it('initialises errors-only (tracesSampleRate 0) from the config', async () => {
		const { initSentry } = await freshModule();
		initSentry(fullConfig);
		expect(init).toHaveBeenCalledTimes(1);
		expect(init).toHaveBeenCalledWith(
			expect.objectContaining({
				dsn: fullConfig.dsn,
				environment: 'test',
				release: '1.2.3',
				tracesSampleRate: 0,
				sendDefaultPii: false
			})
		);
	});

	it('is idempotent across repeated calls with the same DSN', async () => {
		const { initSentry } = await freshModule();
		initSentry(fullConfig);
		initSentry(fullConfig);
		expect(init).toHaveBeenCalledTimes(1);
	});

	it('omits empty environment and release', async () => {
		const { initSentry } = await freshModule();
		initSentry({ dsn: fullConfig.dsn, environment: '', release: '' });
		expect(init).toHaveBeenCalledWith(
			expect.objectContaining({ environment: undefined, release: undefined })
		);
	});

	it('does not run outside a browser', async () => {
		vi.unstubAllGlobals();
		const { initSentry } = await freshModule();
		initSentry(fullConfig);
		expect(init).not.toHaveBeenCalled();
	});
});

describe('eager env-DSN init (finding 2: boot/first-contact errors)', () => {
	beforeEach(() => {
		vi.stubGlobal('window', {});
		clearEnv();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		clearEnv();
	});

	it('initialises eagerly on import when VITE_SENTRY_DSN is set', async () => {
		env().VITE_SENTRY_DSN = 'https://envpublic@example.ingest.sentry.io/9';
		env().VITE_SENTRY_ENVIRONMENT = 'staging';
		env().VITE_SENTRY_RELEASE = '9.9.9';
		await freshModule();
		expect(init).toHaveBeenCalledTimes(1);
		expect(init).toHaveBeenCalledWith(
			expect.objectContaining({
				dsn: 'https://envpublic@example.ingest.sentry.io/9',
				environment: 'staging',
				release: '9.9.9',
				tracesSampleRate: 0
			})
		);
	});

	it('does not init eagerly when no env DSN is present', async () => {
		await freshModule();
		expect(init).not.toHaveBeenCalled();
	});

	it('re-initialises when /api/config delivers a different DSN than the env one', async () => {
		env().VITE_SENTRY_DSN = 'https://envpublic@example.ingest.sentry.io/9';
		const { initSentry } = await freshModule();
		expect(init).toHaveBeenCalledTimes(1);
		initSentry(fullConfig); // different runtime DSN
		expect(init).toHaveBeenCalledTimes(2);
		expect(init).toHaveBeenLastCalledWith(expect.objectContaining({ dsn: fullConfig.dsn }));
	});

	it('does not re-init when /api/config repeats the same DSN already used by the env init', async () => {
		env().VITE_SENTRY_DSN = fullConfig.dsn;
		const { initSentry } = await freshModule();
		expect(init).toHaveBeenCalledTimes(1);
		initSentry(fullConfig);
		expect(init).toHaveBeenCalledTimes(1);
	});
});

describe('beforeSend scrubbing (finding 3)', () => {
	beforeEach(() => {
		vi.stubGlobal('window', {});
		clearEnv();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		clearEnv();
	});

	// Pull the configured beforeSend out of the init() call so we can exercise it
	// directly against representative event shapes.
	async function getBeforeSend() {
		const { initSentry } = await freshModule();
		initSentry(fullConfig);
		const cfg = init.mock.calls[0][0] as { beforeSend?: (e: unknown) => unknown };
		expect(typeof cfg.beforeSend).toBe('function');
		return cfg.beforeSend!;
	}

	it('strips Authorization request headers', async () => {
		const beforeSend = await getBeforeSend();
		const event = {
			request: {
				headers: { Authorization: 'Bearer secret-token', 'Content-Type': 'application/json' }
			}
		};
		const out = beforeSend(event) as typeof event;
		expect(out.request.headers.Authorization).toBeUndefined();
		expect(out.request.headers['Content-Type']).toBe('application/json');
	});

	it('strips a lowercase authorization header too', async () => {
		const beforeSend = await getBeforeSend();
		const event = { request: { headers: { authorization: 'Bearer secret' } } };
		const out = beforeSend(event) as { request: { headers: Record<string, string> } };
		expect(out.request.headers.authorization).toBeUndefined();
	});

	it('truncates long extra string fields (pasted article text / body)', async () => {
		const beforeSend = await getBeforeSend();
		const longText = 'x'.repeat(5000);
		const event = { extra: { text: longText, body: longText, status: 500 } };
		const out = beforeSend(event) as { extra: Record<string, unknown> };
		expect((out.extra.text as string).length).toBeLessThan(longText.length);
		expect(out.extra.text as string).toContain('[truncated]');
		expect((out.extra.body as string).length).toBeLessThan(longText.length);
		// Non-string values are left intact.
		expect(out.extra.status).toBe(500);
	});

	it('drops the request body field entirely', async () => {
		const beforeSend = await getBeforeSend();
		const event = { request: { data: 'username=a&password=b' } };
		const out = beforeSend(event) as { request: Record<string, unknown> };
		expect(out.request.data).toBeUndefined();
	});

	it('passes through an event with nothing sensitive', async () => {
		const beforeSend = await getBeforeSend();
		const event = { message: 'hello', tags: { area: 'sync' } };
		const out = beforeSend(event);
		expect(out).toEqual(event);
	});
});

describe('capture helpers (finding 1)', () => {
	beforeEach(() => {
		vi.stubGlobal('window', {});
		clearEnv();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		clearEnv();
	});

	it('captureError forwards to Sentry.captureException with area/extra context', async () => {
		const mod = await freshModule();
		mod.initSentry(fullConfig);
		const err = new Error('boom');
		mod.captureError(err, { area: 'sync', extra: { kind: 'add', http_status: 400 } });
		expect(captureException).toHaveBeenCalledTimes(1);
		expect(captureException).toHaveBeenCalledWith(
			err,
			expect.objectContaining({
				tags: expect.objectContaining({ area: 'sync' }),
				extra: expect.objectContaining({ kind: 'add', http_status: 400 })
			})
		);
	});

	it('captureError is a no-op when Sentry is not configured', async () => {
		const mod = await freshModule();
		// No initSentry, no env DSN -> not configured.
		mod.captureError(new Error('boom'), { area: 'sync' });
		expect(captureException).not.toHaveBeenCalled();
	});

	it('captureMessage forwards with a level and area tag', async () => {
		const mod = await freshModule();
		mod.initSentry(fullConfig);
		mod.captureWarning('persist() returned false', { area: 'idb' });
		expect(captureMessage).toHaveBeenCalledWith(
			'persist() returned false',
			expect.objectContaining({ level: 'warning', tags: expect.objectContaining({ area: 'idb' }) })
		);
	});

	it('addSyncBreadcrumb forwards a categorised breadcrumb', async () => {
		const mod = await freshModule();
		mod.initSentry(fullConfig);
		mod.addSyncBreadcrumb('sync start', { phase: 'pull' });
		expect(addBreadcrumb).toHaveBeenCalledWith(
			expect.objectContaining({
				category: 'sync',
				message: 'sync start',
				data: expect.objectContaining({ phase: 'pull' })
			})
		);
	});

	it('addSyncBreadcrumb is a no-op when Sentry is not configured', async () => {
		const mod = await freshModule();
		mod.addSyncBreadcrumb('sync start');
		expect(addBreadcrumb).not.toHaveBeenCalled();
	});

	it('setSentryUser sets a minimal user (id only, no PII)', async () => {
		const mod = await freshModule();
		mod.initSentry(fullConfig);
		mod.setSentryUser('alice');
		expect(setUser).toHaveBeenCalledWith({ username: 'alice' });
	});

	it('clearSentryUser nulls the user', async () => {
		const mod = await freshModule();
		mod.initSentry(fullConfig);
		mod.clearSentryUser();
		expect(setUser).toHaveBeenCalledWith(null);
	});

	it('user helpers are no-ops when Sentry is not configured', async () => {
		const mod = await freshModule();
		mod.setSentryUser('alice');
		mod.clearSentryUser();
		expect(setUser).not.toHaveBeenCalled();
	});
});
