// Unit tests for the pipeline-activity store. As in store.test.ts, the
// standalone vitest config does not load the Svelte compiler, so `$state` is
// shimmed as identity.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

let browserFlag = true;
vi.mock('$app/environment', () => ({
	get browser() {
		return browserFlag;
	}
}));

// Captured Dexie query pieces, so a test can assert what was asked for and push
// results through the subscription.
const anyOfArgs: unknown[] = [];
let emit: ((count: number) => void) | undefined;
let fail: ((err: unknown) => void) | undefined;
const unsubscribe = vi.fn();

const count = vi.fn(async () => 0);
vi.mock('$lib/db', () => ({
	db: {
		articles_meta: {
			where: (field: string) => ({
				anyOf: (values: unknown[]) => {
					anyOfArgs.push({ field, values });
					return { count };
				}
			})
		}
	}
}));

vi.mock('dexie', () => ({
	liveQuery: (query: () => Promise<number>) => ({
		subscribe(handlers: { next: (v: number) => void; error: (e: unknown) => void }) {
			// Run the query once so the `where(...).anyOf(...)` assertions have
			// something to inspect.
			void query();
			emit = handlers.next;
			fail = handlers.error;
			return { unsubscribe };
		}
	})
}));

const captureError = vi.fn();
vi.mock('$lib/sentry', () => ({ captureError }));

type ProcessingModule = typeof import('./processing.svelte');

async function loadModule(): Promise<ProcessingModule> {
	vi.resetModules();
	return import('./processing.svelte');
}

beforeEach(() => {
	browserFlag = true;
	anyOfArgs.length = 0;
	emit = undefined;
	fail = undefined;
	unsubscribe.mockClear();
	captureError.mockClear();
	vi.stubGlobal('$state', <T>(v: T): T => v);
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('watchProcessing', () => {
	it('counts exactly the in-flight statuses', async () => {
		const { watchProcessing, PROCESSING_STATUSES } = await loadModule();
		watchProcessing();

		expect(anyOfArgs[0]).toEqual({ field: 'status', values: PROCESSING_STATUSES });
		// Terminal and failed states are not "the pipeline is working".
		expect(PROCESSING_STATUSES).not.toContain('enriched');
		expect(PROCESSING_STATUSES).not.toContain('fetch_failed');
		expect(PROCESSING_STATUSES).not.toContain('enrich_failed');
		expect(PROCESSING_STATUSES).not.toContain('blocked');
	});

	it('tracks the live count', async () => {
		const { watchProcessing, processing, isProcessingActive } = await loadModule();
		watchProcessing();

		expect(isProcessingActive()).toBe(false);

		emit!(3);
		expect(processing.count).toBe(3);
		expect(isProcessingActive()).toBe(true);

		emit!(0);
		expect(processing.count).toBe(0);
		expect(isProcessingActive()).toBe(false);
	});

	it('falls back to idle and reports a query failure', async () => {
		const { watchProcessing, processing } = await loadModule();
		watchProcessing();

		emit!(2);
		fail!(new Error('IndexedDB gone'));

		// Ambient decoration must never leave the shell stuck "busy".
		expect(processing.count).toBe(0);
		expect(captureError).toHaveBeenCalled();
	});

	it('unsubscribes on stop', async () => {
		const { watchProcessing } = await loadModule();
		const stop = watchProcessing();

		stop();

		expect(unsubscribe).toHaveBeenCalledTimes(1);
	});

	it('is a no-op during SSR', async () => {
		browserFlag = false;
		const { watchProcessing } = await loadModule();

		watchProcessing()();

		expect(anyOfArgs).toHaveLength(0);
		expect(unsubscribe).not.toHaveBeenCalled();
	});
});
