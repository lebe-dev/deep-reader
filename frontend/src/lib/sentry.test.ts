import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SentryConfig } from '$lib/types';

// Mock the SDK so the tests assert how we call it, without a real transport.
const init = vi.fn();
vi.mock('@sentry/sveltekit', () => ({ init }));

// Fresh module per test so the module-level `initialized` guard resets.
async function freshInitSentry() {
	vi.resetModules();
	init.mockClear();
	return (await import('./sentry')).initSentry;
}

const fullConfig: SentryConfig = {
	dsn: 'https://public@example.ingest.sentry.io/1',
	environment: 'test',
	release: '1.2.3'
};

describe('initSentry', () => {
	// initSentry only runs in a browser; the vitest env is node, so provide a
	// minimal window stub.
	beforeEach(() => {
		init.mockClear();
		vi.stubGlobal('window', {});
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('does nothing when the DSN is empty (reporting disabled)', async () => {
		const initSentry = await freshInitSentry();
		initSentry({ dsn: '', environment: 'test', release: '1.2.3' });
		expect(init).not.toHaveBeenCalled();
	});

	it('does nothing when the config is undefined', async () => {
		const initSentry = await freshInitSentry();
		initSentry(undefined);
		expect(init).not.toHaveBeenCalled();
	});

	it('initialises errors-only (tracesSampleRate 0) from the config', async () => {
		const initSentry = await freshInitSentry();
		initSentry(fullConfig);
		expect(init).toHaveBeenCalledTimes(1);
		expect(init).toHaveBeenCalledWith(
			expect.objectContaining({
				dsn: fullConfig.dsn,
				environment: 'test',
				release: '1.2.3',
				tracesSampleRate: 0
			})
		);
	});

	it('is idempotent across repeated calls', async () => {
		const initSentry = await freshInitSentry();
		initSentry(fullConfig);
		initSentry(fullConfig);
		expect(init).toHaveBeenCalledTimes(1);
	});

	it('omits empty environment and release', async () => {
		const initSentry = await freshInitSentry();
		initSentry({ dsn: fullConfig.dsn, environment: '', release: '' });
		expect(init).toHaveBeenCalledWith(
			expect.objectContaining({ environment: undefined, release: undefined })
		);
	});
});
