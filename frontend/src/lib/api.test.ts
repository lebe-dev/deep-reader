import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SyncState } from './db';

// ---------------------------------------------------------------------------
// Mocks
//
// api.ts pulls in `$app/environment` (browser flag), `./db` (sync-state
// singleton: base URL + bearer token) and `./sentry` (manual capture). We mock
// all three so the request() plumbing can be exercised in node against a stubbed
// global fetch, with no SvelteKit/IndexedDB/transport machinery.
// ---------------------------------------------------------------------------

// Mutable browser flag so individual tests can flip to the SSR branch.
const env = { browser: true };
vi.mock('$app/environment', () => ({
	get browser() {
		return env.browser;
	}
}));

// Mutable sync-state the mocked getSyncState returns; tests tweak per case.
let syncState: SyncState = { id: 'singleton' };
const getSyncState = vi.fn(async () => syncState);
vi.mock('./db', () => ({
	getSyncState: () => getSyncState()
}));

// Capture manual Sentry reports without a real SDK.
const captureError = vi.fn();
vi.mock('./sentry', () => ({
	captureError: (...args: unknown[]) => captureError(...args)
}));

type ApiModule = typeof import('./api');

/** A minimal Response-like object good enough for request()'s consumption. */
function fakeResponse(opts: { ok?: boolean; status?: number; body?: string }): Response {
	const status = opts.status ?? 200;
	const body = opts.body ?? '';
	return {
		ok: opts.ok ?? (status >= 200 && status < 300),
		status,
		text: async () => body
	} as unknown as Response;
}

let fetchMock: ReturnType<typeof vi.fn>;

async function load(): Promise<ApiModule> {
	return import('./api');
}

beforeEach(() => {
	env.browser = true;
	syncState = { id: 'singleton' };
	getSyncState.mockClear();
	captureError.mockClear();

	fetchMock = vi.fn();
	vi.stubGlobal('fetch', fetchMock);
	// Same-origin base for the relative-path branch.
	vi.stubGlobal('window', { location: { origin: 'https://app.example' } });
	// Online by default; offline-mapping tests flip this.
	vi.stubGlobal('navigator', { onLine: true });
});

afterEach(() => {
	vi.unstubAllGlobals();
});

// ---------------------------------------------------------------------------
// Base URL / same-origin
// ---------------------------------------------------------------------------

describe('request base URL', () => {
	it('keeps the request relative (same-origin) when no serverUrl is configured', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig();
		expect(fetchMock).toHaveBeenCalledTimes(1);
		const [target] = fetchMock.mock.calls[0];
		expect(target).toBe('/api/config');
	});

	it('prefixes a configured serverUrl and strips its trailing slash', async () => {
		syncState = { id: 'singleton', serverUrl: 'https://api.example/' };
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig();
		const [target] = fetchMock.mock.calls[0];
		expect(target).toBe('https://api.example/api/config');
	});
});

// ---------------------------------------------------------------------------
// Authorization header
// ---------------------------------------------------------------------------

describe('Authorization header', () => {
	it('sends a Bearer token when authToken is present', async () => {
		syncState = { id: 'singleton', authToken: 'tok-123' };
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig();
		const [, init] = fetchMock.mock.calls[0];
		expect((init.headers as Record<string, string>).Authorization).toBe('Bearer tok-123');
	});

	it('omits Authorization when there is no token', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig();
		const [, init] = fetchMock.mock.calls[0];
		expect('Authorization' in (init.headers as Record<string, string>)).toBe(false);
	});

	it('sets Content-Type only when a body is present', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}', status: 200 }));
		const { login } = await load();
		await login('u', 'p');
		const [, init] = fetchMock.mock.calls[0];
		expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
		expect(init.body).toBe(JSON.stringify({ username: 'u', password: 'p' }));
	});
});

// ---------------------------------------------------------------------------
// Query params (undefined omission)
// ---------------------------------------------------------------------------

describe('query params', () => {
	it('omits query params whose value is undefined', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig(undefined); // since omitted
		const [target] = fetchMock.mock.calls[0];
		expect(target).toBe('/api/config');
		expect(target).not.toContain('since');
	});

	it('includes a query param when defined (relative base keeps it relative)', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig('2026-01-01T00:00:00Z');
		const [target] = fetchMock.mock.calls[0];
		expect(target).toContain('/api/config?');
		expect(target).toContain('since=2026-01-01');
	});

	it('includes a query param against a configured (absolute) base', async () => {
		syncState = { id: 'singleton', serverUrl: 'https://api.example' };
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getConfig } = await load();
		await getConfig('2026-01-01T00:00:00Z');
		const [target] = fetchMock.mock.calls[0];
		expect(target).toContain('https://api.example/api/config?since=');
	});
});

// ---------------------------------------------------------------------------
// Body decoding: 204 / empty / JSON
// ---------------------------------------------------------------------------

describe('response body decoding', () => {
	it('returns undefined for a 204 No Content', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ status: 204, body: 'ignored' }));
		const { logout } = await load();
		await expect(logout()).resolves.toBeUndefined();
	});

	it('returns undefined for a 200 with an empty body', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ status: 200, body: '' }));
		const { logout } = await load();
		await expect(logout()).resolves.toBeUndefined();
	});

	it('parses a JSON body', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: JSON.stringify({ token: 'abc' }) }));
		const { login } = await load();
		await expect(login('u', 'p')).resolves.toEqual({ token: 'abc' });
	});
});

// ---------------------------------------------------------------------------
// HTTP error mapping
// ---------------------------------------------------------------------------

describe('non-2xx -> ApiError', () => {
	it('throws ApiError carrying the status and body text', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ ok: false, status: 422, body: 'bad input' }));
		const { getConfig, ApiError } = await load();
		const err = await getConfig().catch((e) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect(err.status).toBe(422);
		expect(err.body).toBe('bad input');
	});

	it('does not report ApiError to Sentry (it is an expected HTTP outcome)', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ ok: false, status: 500, body: 'oops' }));
		const { getConfig } = await load();
		await getConfig().catch(() => {});
		expect(captureError).not.toHaveBeenCalled();
	});
});

// ---------------------------------------------------------------------------
// Network-failure / abort mapping (findings 1 & 2)
// ---------------------------------------------------------------------------

describe('fetch rejection mapping', () => {
	it('maps a genuine network failure to OfflineError preserving the cause', async () => {
		const cause = new TypeError('Failed to fetch');
		fetchMock.mockRejectedValue(cause);
		const { getConfig, OfflineError } = await load();
		const err = await getConfig().catch((e) => e);
		expect(err).toBeInstanceOf(OfflineError);
		expect((err as Error).cause).toBe(cause);
	});

	it('rethrows an AbortError unchanged (caller-initiated cancellation)', async () => {
		const abort = new DOMException('aborted', 'AbortError');
		fetchMock.mockRejectedValue(abort);
		const { getConfig, OfflineError } = await load();
		const err = await getConfig().catch((e) => e);
		expect(err).toBe(abort);
		expect(err).not.toBeInstanceOf(OfflineError);
	});

	it('also rethrows a plain object with name "AbortError"', async () => {
		const abort = { name: 'AbortError', message: 'aborted' };
		fetchMock.mockRejectedValue(abort);
		const { getConfig, OfflineError } = await load();
		const err = await getConfig().catch((e) => e);
		expect(err).toBe(abort);
		expect(err).not.toBeInstanceOf(OfflineError);
	});

	it('captures the underlying error to Sentry when navigator.onLine is true', async () => {
		const cause = new TypeError('CORS / mixed-content / DNS');
		fetchMock.mockRejectedValue(cause);
		const { getConfig } = await load();
		await getConfig().catch(() => {});
		expect(captureError).toHaveBeenCalledTimes(1);
		const [reported, ctx] = captureError.mock.calls[0];
		expect(reported).toBe(cause);
		expect(ctx).toMatchObject({ area: 'api', extra: { path: '/api/config' } });
	});

	it('does NOT capture when navigator.onLine is false (a real offline state)', async () => {
		vi.stubGlobal('navigator', { onLine: false });
		fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));
		const { getConfig, OfflineError } = await load();
		const err = await getConfig().catch((e) => e);
		expect(err).toBeInstanceOf(OfflineError);
		expect(captureError).not.toHaveBeenCalled();
	});

	it('does NOT capture an AbortError even when online', async () => {
		const abort = new DOMException('aborted', 'AbortError');
		fetchMock.mockRejectedValue(abort);
		const { getConfig } = await load();
		await getConfig().catch(() => {});
		expect(captureError).not.toHaveBeenCalled();
	});
});

// ---------------------------------------------------------------------------
// getArticle cache-busting / no-store (regression for the re-enrich path)
// ---------------------------------------------------------------------------

describe('getArticle cache options', () => {
	it('passes the version as a `v` query param and no explicit cache by default', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getArticle } = await load();
		await getArticle('a-1', undefined, { version: '2026-01-02T03:04:05Z' });
		const [target, init] = fetchMock.mock.calls[0];
		expect(target).toContain('/api/articles/a-1?');
		expect(target).toContain('v=2026-01-02');
		expect(init.cache).toBeUndefined();
	});

	it('passes cache:"no-store" when noStore is requested', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getArticle } = await load();
		await getArticle('a-1', undefined, { noStore: true });
		const [target, init] = fetchMock.mock.calls[0];
		expect(target).toBe('/api/articles/a-1');
		expect(init.cache).toBe('no-store');
	});

	it('omits the `v` param when no version is given', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getArticle } = await load();
		await getArticle('a-1');
		const [target] = fetchMock.mock.calls[0];
		expect(target).toBe('/api/articles/a-1');
	});

	it('percent-encodes the article id in the path', async () => {
		fetchMock.mockResolvedValue(fakeResponse({ body: '{}' }));
		const { getArticle } = await load();
		await getArticle('a/b c');
		const [target] = fetchMock.mock.calls[0];
		expect(target).toBe('/api/articles/a%2Fb%20c');
	});
});
