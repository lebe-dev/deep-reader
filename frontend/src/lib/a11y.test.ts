// Unit tests for the reduced-motion helpers. The interesting cases are the
// degraded ones: no browser and no matchMedia must not throw, because these are
// called from module-level UI code that also runs during SSR.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

let browserFlag = true;
vi.mock('$app/environment', () => ({
	get browser() {
		return browserFlag;
	}
}));

type A11yModule = typeof import('./a11y');

let originalWindow: unknown;

async function load(): Promise<A11yModule> {
	vi.resetModules();
	return import('./a11y');
}

function stubMatchMedia(matches: boolean) {
	(globalThis as { window?: unknown }).window = {
		matchMedia: (query: string) => ({ matches: query.includes('reduce') && matches })
	};
}

beforeEach(() => {
	originalWindow = (globalThis as { window?: unknown }).window;
	browserFlag = true;
});

afterEach(() => {
	(globalThis as { window?: unknown }).window = originalWindow;
	vi.restoreAllMocks();
});

describe('prefersReducedMotion', () => {
	it('reports the media query result', async () => {
		stubMatchMedia(true);
		const { prefersReducedMotion } = await load();
		expect(prefersReducedMotion()).toBe(true);
	});

	it('is false when the user has not asked for reduced motion', async () => {
		stubMatchMedia(false);
		const { prefersReducedMotion } = await load();
		expect(prefersReducedMotion()).toBe(false);
	});

	it('is false outside the browser', async () => {
		browserFlag = false;
		stubMatchMedia(true);
		const { prefersReducedMotion } = await load();
		expect(prefersReducedMotion()).toBe(false);
	});

	it('is false when matchMedia is unavailable', async () => {
		(globalThis as { window?: unknown }).window = {};
		const { prefersReducedMotion } = await load();
		expect(prefersReducedMotion()).toBe(false);
	});
});

describe('scrollBehavior', () => {
	it('drops the animation when motion is reduced', async () => {
		stubMatchMedia(true);
		const { scrollBehavior } = await load();
		expect(scrollBehavior()).toBe('auto');
	});

	it('keeps smooth scrolling otherwise', async () => {
		stubMatchMedia(false);
		const { scrollBehavior } = await load();
		expect(scrollBehavior()).toBe('smooth');
	});
});
