import { describe, it, expect, vi } from 'vitest';

// reader-width.svelte.ts reads `browser` from $app/environment (absent in the
// node test env) and declares a module-level `$state(...)` that executes at
// import time. Mock the former and shim the latter — both must be in place
// BEFORE the import below, hence vi.hoisted (which runs before module imports).
vi.mock('$app/environment', () => ({ browser: false }));
vi.hoisted(() => {
	(globalThis as unknown as { $state: <T>(v: T) => T }).$state = (v) => v;
});

import { READER_WIDTH_OPTIONS, getReaderWidthRem } from './reader-width.svelte';

describe('getReaderWidthRem', () => {
	it('returns the configured rem for each preset', () => {
		for (const opt of READER_WIDTH_OPTIONS) {
			expect(getReaderWidthRem(opt.value)).toBe(opt.rem);
		}
	});

	it('falls back to the medium default on unknown input', () => {
		const medium = READER_WIDTH_OPTIONS.find((o) => o.value === 'medium')!;
		// @ts-expect-error — deliberately passing an invalid value.
		expect(getReaderWidthRem('huge')).toBe(medium.rem);
	});

	it('keeps every preset within the max-w-3xl (48rem) container', () => {
		for (const opt of READER_WIDTH_OPTIONS) {
			expect(parseFloat(opt.rem)).toBeLessThanOrEqual(48);
		}
	});
});
