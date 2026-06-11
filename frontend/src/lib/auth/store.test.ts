import { describe, it, expect, vi, beforeEach } from 'vitest';

// ---------------------------------------------------------------------------
// Test scaffolding
// ---------------------------------------------------------------------------
//
// `store.svelte.ts` declares its state with the Svelte 5 `$state` rune. The
// vitest config runs in plain node WITHOUT the Svelte compiler plugin (the
// store's auth logic is framework-agnostic), so the `$state` macro is an
// undefined free identifier at import time. Reactivity is irrelevant for these
// logic tests, so we shim it to an identity function that returns a plain
// mutable object. Must be installed before the module is imported.
(globalThis as Record<string, unknown>).$state = <T>(v: T): T => v;

// Typed error doubles matching $lib/api's real classes (instanceof-checked by
// the store). Declared at module scope so the vi.mock factory and the tests
// share the same constructors.
class OfflineError extends Error {
	constructor(message = 'Network unavailable') {
		super(message);
		this.name = 'OfflineError';
	}
}
class ApiError extends Error {
	readonly status: number;
	readonly body: string;
	constructor(status: number, body = '') {
		super(`API error ${status}`);
		this.name = 'ApiError';
		this.status = status;
		this.body = body;
	}
}

// --- $lib/api mock -----------------------------------------------------------
const getConfig = vi.fn();
const apiLogin = vi.fn();
const apiLogout = vi.fn();
const apiSetup = vi.fn();
vi.mock('$lib/api', () => ({
	getConfig,
	login: apiLogin,
	logout: apiLogout,
	setup: apiSetup,
	ApiError,
	OfflineError
}));

// --- $lib/db mock ------------------------------------------------------------
// A tiny in-memory sync_state singleton so token persistence is observable.
let syncState: Record<string, unknown> = {};
const getSyncState = vi.fn(async () => syncState);
const updateSyncState = vi.fn(async (patch: Record<string, unknown>) => {
	syncState = { ...syncState, ...patch };
});
vi.mock('$lib/db', () => ({ getSyncState, updateSyncState }));

// --- $lib/sentry mock --------------------------------------------------------
const initSentry = vi.fn();
const setSentryUser = vi.fn();
const clearSentryUser = vi.fn();
const captureError = vi.fn();
vi.mock('$lib/sentry', () => ({ initSentry, setSentryUser, clearSentryUser, captureError }));

type StoreModule = typeof import('./store.svelte');

/** Re-import the store with a clean module-level `authState` and reset mocks. */
async function freshStore(initialSync: Record<string, unknown> = {}): Promise<StoreModule> {
	vi.resetModules();
	getConfig.mockReset();
	apiLogin.mockReset();
	apiLogout.mockReset();
	apiSetup.mockReset();
	getSyncState.mockClear();
	updateSyncState.mockClear();
	initSentry.mockClear();
	setSentryUser.mockClear();
	clearSentryUser.mockClear();
	captureError.mockClear();
	syncState = { ...initialSync };
	return import('./store.svelte');
}

/** A minimal ConfigResponse with the only fields refreshAuth reads. */
function config(initialized: boolean, authenticated: boolean, dsn = '') {
	return {
		auth: { initialized, authenticated },
		sentry: { dsn, environment: '', release: '' }
	};
}

// ---------------------------------------------------------------------------
// refreshAuth — online success
// ---------------------------------------------------------------------------

describe('refreshAuth: online (getConfig succeeds)', () => {
	it('mirrors server auth status and marks the check complete', async () => {
		const { refreshAuth, authState } = await freshStore();
		getConfig.mockResolvedValue(config(true, true));

		await refreshAuth();

		expect(authState.initialized).toBe(true);
		expect(authState.authenticated).toBe(true);
		expect(authState.checked).toBe(true);
	});

	it('reflects an unauthenticated server response', async () => {
		const { refreshAuth, authState } = await freshStore();
		getConfig.mockResolvedValue(config(true, false));

		await refreshAuth();

		expect(authState.initialized).toBe(true);
		expect(authState.authenticated).toBe(false);
		expect(authState.checked).toBe(true);
	});

	it('configures Sentry from the server-provided DSN', async () => {
		const { refreshAuth } = await freshStore();
		const cfg = config(true, true, 'https://public@example.ingest.sentry.io/1');
		getConfig.mockResolvedValue(cfg);

		await refreshAuth();

		expect(initSentry).toHaveBeenCalledWith(cfg.sentry);
	});
});

// ---------------------------------------------------------------------------
// refreshAuth — offline fallback (the regression that silently logs users out)
// ---------------------------------------------------------------------------

describe('refreshAuth: offline fallback (getConfig throws OfflineError)', () => {
	it('treats a stored token as authenticated so an offline user is not logged out', async () => {
		const { refreshAuth, authState } = await freshStore({ authToken: 'tok' });
		getConfig.mockRejectedValue(new OfflineError());

		await refreshAuth();

		expect(authState.authenticated).toBe(true);
		// Never held a server response, but a token implies the account exists.
		expect(authState.initialized).toBe(true);
		expect(authState.checked).toBe(true);
	});

	it('treats no stored token as unauthenticated when offline', async () => {
		const { refreshAuth, authState } = await freshStore({}); // no authToken
		getConfig.mockRejectedValue(new OfflineError());

		await refreshAuth();

		expect(authState.authenticated).toBe(false);
		expect(authState.initialized).toBe(false);
		expect(authState.checked).toBe(true);
	});

	it('preserves a previously-known initialized=true across an offline blip', async () => {
		const { refreshAuth, authState } = await freshStore({ authToken: 'tok' });
		// First, an online check establishes initialized=true / authenticated=false.
		getConfig.mockResolvedValueOnce(config(true, false));
		await refreshAuth();
		expect(authState.initialized).toBe(true);

		// Then the network drops; the offline branch must not clobber initialized.
		getConfig.mockRejectedValueOnce(new OfflineError());
		await refreshAuth();

		expect(authState.initialized).toBe(true);
		expect(authState.authenticated).toBe(true); // token present
	});

	it('does not configure Sentry on the offline path (no config arrived)', async () => {
		const { refreshAuth } = await freshStore({ authToken: 'tok' });
		getConfig.mockRejectedValue(new OfflineError());

		await refreshAuth();

		expect(initSentry).not.toHaveBeenCalled();
	});
});

// ---------------------------------------------------------------------------
// refreshAuth — generic (non-Offline) error
// ---------------------------------------------------------------------------

describe('refreshAuth: generic error (getConfig throws a non-Offline error)', () => {
	it('leaves the last-known auth state in place but still marks the check complete', async () => {
		const { refreshAuth, authState } = await freshStore({ authToken: 'tok' });
		// Seed a known-good state via an online success first.
		getConfig.mockResolvedValueOnce(config(true, true));
		await refreshAuth();

		// A subsequent generic failure must not flip auth state.
		getConfig.mockRejectedValueOnce(new ApiError(500, 'boom'));
		await refreshAuth();

		expect(authState.authenticated).toBe(true);
		expect(authState.initialized).toBe(true);
		expect(authState.checked).toBe(true);
	});

	it('does not consult the local token store on a generic error', async () => {
		const { refreshAuth } = await freshStore({ authToken: 'tok' });
		getConfig.mockRejectedValue(new Error('unexpected'));

		await refreshAuth();

		// getSyncState is only used by the OfflineError branch.
		expect(getSyncState).not.toHaveBeenCalled();
	});

	it('does not throw out of refreshAuth on a generic error', async () => {
		const { refreshAuth, authState } = await freshStore();
		getConfig.mockRejectedValue(new Error('unexpected'));

		await expect(refreshAuth()).resolves.toBeUndefined();
		expect(authState.checked).toBe(true);
	});
});

// ---------------------------------------------------------------------------
// login / setupAccount — token persistence + Sentry user
// ---------------------------------------------------------------------------

describe('login', () => {
	it('persists the returned token and marks the session authenticated', async () => {
		const { login, authState } = await freshStore();
		apiLogin.mockResolvedValue({ token: 'tok-123', username: 'alice' });

		await login('alice', 'pw');

		expect(updateSyncState).toHaveBeenCalledWith({ authToken: 'tok-123' });
		expect(authState.initialized).toBe(true);
		expect(authState.authenticated).toBe(true);
		expect(authState.username).toBe('alice');
	});

	it('associates the Sentry user after a successful login', async () => {
		const { login } = await freshStore();
		apiLogin.mockResolvedValue({ token: 'tok-123', username: 'alice' });

		await login('alice', 'pw');

		expect(setSentryUser).toHaveBeenCalledWith('alice');
	});

	it('propagates an auth failure without persisting a token', async () => {
		const { login } = await freshStore();
		apiLogin.mockRejectedValue(new ApiError(401, 'bad creds'));

		await expect(login('alice', 'wrong')).rejects.toBeInstanceOf(ApiError);
		expect(updateSyncState).not.toHaveBeenCalled();
		expect(setSentryUser).not.toHaveBeenCalled();
	});
});

describe('setupAccount', () => {
	it('persists the returned token and marks the account initialized + authenticated', async () => {
		const { setupAccount, authState } = await freshStore();
		apiSetup.mockResolvedValue({ token: 'tok-setup', username: 'bob' });

		await setupAccount('bob', 'pw');

		expect(updateSyncState).toHaveBeenCalledWith({ authToken: 'tok-setup' });
		expect(authState.initialized).toBe(true);
		expect(authState.authenticated).toBe(true);
		expect(authState.username).toBe('bob');
	});

	it('associates the Sentry user after a successful setup', async () => {
		const { setupAccount } = await freshStore();
		apiSetup.mockResolvedValue({ token: 'tok-setup', username: 'bob' });

		await setupAccount('bob', 'pw');

		expect(setSentryUser).toHaveBeenCalledWith('bob');
	});
});

// ---------------------------------------------------------------------------
// clearSession — nulls the token and clears the Sentry user
// ---------------------------------------------------------------------------

describe('clearSession', () => {
	it('clears the stored token and the authenticated/username state', async () => {
		const { login, clearSession, authState } = await freshStore();
		apiLogin.mockResolvedValue({ token: 'tok', username: 'alice' });
		await login('alice', 'pw');
		updateSyncState.mockClear();

		await clearSession();

		expect(updateSyncState).toHaveBeenCalledWith({ authToken: undefined });
		expect(authState.authenticated).toBe(false);
		expect(authState.username).toBeUndefined();
	});

	it('clears the Sentry user', async () => {
		const { clearSession } = await freshStore({ authToken: 'tok' });

		await clearSession();

		expect(clearSentryUser).toHaveBeenCalledTimes(1);
	});
});

// ---------------------------------------------------------------------------
// logout — best-effort server call + error capture policy
// ---------------------------------------------------------------------------

describe('logout', () => {
	it('calls the server then clears the local session on success', async () => {
		const { logout, authState } = await freshStore({ authToken: 'tok' });
		apiLogout.mockResolvedValue(undefined);

		await logout();

		expect(apiLogout).toHaveBeenCalledTimes(1);
		expect(updateSyncState).toHaveBeenCalledWith({ authToken: undefined });
		expect(authState.authenticated).toBe(false);
		expect(clearSentryUser).toHaveBeenCalledTimes(1);
		expect(captureError).not.toHaveBeenCalled();
	});

	it('still clears the local session when the server call fails offline (no capture)', async () => {
		const { logout, authState } = await freshStore({ authToken: 'tok' });
		apiLogout.mockRejectedValue(new OfflineError());

		await logout();

		expect(authState.authenticated).toBe(false);
		expect(updateSyncState).toHaveBeenCalledWith({ authToken: undefined });
		// Offline is routine — must not produce a Sentry signal.
		expect(captureError).not.toHaveBeenCalled();
	});

	it('swallows an expected 4xx (e.g. already-revoked token) without capturing', async () => {
		const { logout, authState } = await freshStore({ authToken: 'tok' });
		apiLogout.mockRejectedValue(new ApiError(401, 'unauthorized'));

		await logout();

		expect(authState.authenticated).toBe(false);
		expect(captureError).not.toHaveBeenCalled();
	});

	it('captures an unexpected 5xx (systemic backend problem) before clearing', async () => {
		const { logout, authState } = await freshStore({ authToken: 'tok' });
		const err = new ApiError(500, 'server error');
		apiLogout.mockRejectedValue(err);

		await logout();

		expect(captureError).toHaveBeenCalledTimes(1);
		expect(captureError).toHaveBeenCalledWith(
			err,
			expect.objectContaining({
				area: 'auth',
				extra: expect.objectContaining({ action: 'logout' })
			})
		);
		// The local session is still dropped despite the capture.
		expect(authState.authenticated).toBe(false);
		expect(updateSyncState).toHaveBeenCalledWith({ authToken: undefined });
	});

	it('captures an unexpected non-Api exception', async () => {
		const { logout } = await freshStore({ authToken: 'tok' });
		const err = new TypeError('boom');
		apiLogout.mockRejectedValue(err);

		await logout();

		expect(captureError).toHaveBeenCalledWith(err, expect.objectContaining({ area: 'auth' }));
	});
});

// ---------------------------------------------------------------------------
// initial state
// ---------------------------------------------------------------------------

describe('authState defaults', () => {
	it('starts unauthenticated and unchecked', async () => {
		const { authState } = await freshStore();
		expect(authState.authenticated).toBe(false);
		expect(authState.checked).toBe(false);
		expect(authState.initialized).toBeUndefined();
		expect(authState.username).toBeUndefined();
	});
});

// Reset the global shim between files is unnecessary (identity fn), but keep the
// per-test mock baseline clean.
beforeEach(() => {
	syncState = {};
});
