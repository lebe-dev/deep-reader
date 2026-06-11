import { describe, it, expect, vi, beforeAll, afterEach } from 'vitest';
import type { FontSize, LineHeight } from '$lib/types';
import {
	FONT_SIZE_OPTIONS,
	LINE_HEIGHT_OPTIONS,
	DEFAULT_FONT_SIZE,
	DEFAULT_LINE_HEIGHT,
	fontSizeRem,
	lineHeightMultiplier
} from './reader-typography';

// `reader-font.svelte.ts` reads `browser` from $app/environment (a SvelteKit
// virtual module not present in the standalone node test env) and declares a
// module-level `$state(...)` value. We mock the former and shim the latter so
// the module imports and its pure fallback/validation logic is exercisable.
let browserFlag = false;
vi.mock('$app/environment', () => ({
	get browser() {
		return browserFlag;
	}
}));

beforeAll(() => {
	// Outside the Svelte compiler `$state` is just an identity box; that is all
	// `readerFont` needs to hold a plain value for these logic-only assertions.
	(globalThis as unknown as { $state: <T>(v: T) => T }).$state = (v) => v;
});

// ---------------------------------------------------------------------------
// reader-typography.ts — pure preset lookups with default fallback
// ---------------------------------------------------------------------------

describe('fontSizeRem', () => {
	it('returns the matching preset rem for each known value', () => {
		for (const opt of FONT_SIZE_OPTIONS) {
			expect(fontSizeRem(opt.value)).toBe(opt.rem);
		}
	});

	it('falls back to the default preset for an unknown value', () => {
		const expected = FONT_SIZE_OPTIONS.find((o) => o.value === DEFAULT_FONT_SIZE)!.rem;
		expect(fontSizeRem('zzz' as unknown as FontSize)).toBe(expected);
	});

	it('falls back to the default preset for undefined input', () => {
		const expected = FONT_SIZE_OPTIONS.find((o) => o.value === DEFAULT_FONT_SIZE)!.rem;
		expect(fontSizeRem(undefined)).toBe(expected);
	});
});

describe('lineHeightMultiplier', () => {
	it('returns the matching preset multiplier for each known value', () => {
		for (const opt of LINE_HEIGHT_OPTIONS) {
			expect(lineHeightMultiplier(opt.value)).toBe(opt.multiplier);
		}
	});

	it('falls back to the default preset for an unknown value', () => {
		const expected = LINE_HEIGHT_OPTIONS.find((o) => o.value === DEFAULT_LINE_HEIGHT)!.multiplier;
		expect(lineHeightMultiplier('huge' as unknown as LineHeight)).toBe(expected);
	});

	it('falls back to the default preset for undefined input', () => {
		const expected = LINE_HEIGHT_OPTIONS.find((o) => o.value === DEFAULT_LINE_HEIGHT)!.multiplier;
		expect(lineHeightMultiplier(undefined)).toBe(expected);
	});
});

// ---------------------------------------------------------------------------
// reader-font.svelte.ts — CSS fallback + persisted-value validation
// ---------------------------------------------------------------------------

type ReaderFontModule = typeof import('./reader-font.svelte');

// Re-import fresh per test so the module-level readStoredFont() re-evaluates
// against the current `browser` flag and localStorage stub.
async function freshFontModule(): Promise<ReaderFontModule> {
	vi.resetModules();
	return import('./reader-font.svelte');
}

function stubLocalStorage(initial: Record<string, string> = {}) {
	const store = new Map<string, string>(Object.entries(initial));
	vi.stubGlobal('localStorage', {
		getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
		setItem: (k: string, v: string) => {
			store.set(k, v);
		},
		removeItem: (k: string) => {
			store.delete(k);
		},
		clear: () => store.clear()
	});
	return store;
}

afterEach(() => {
	vi.unstubAllGlobals();
	browserFlag = false;
});

describe('getReaderFontCss', () => {
	it('returns the matching CSS stack for each known font', async () => {
		const { getReaderFontCss, READER_FONT_OPTIONS } = await freshFontModule();
		for (const opt of READER_FONT_OPTIONS) {
			expect(getReaderFontCss(opt.value)).toBe(opt.css);
		}
	});

	it('falls back to the first option for an unknown font', async () => {
		const { getReaderFontCss, READER_FONT_OPTIONS } = await freshFontModule();
		// Cast through unknown: callers are typed, but a stale persisted/synced
		// value could be out of range at runtime.
		const css = getReaderFontCss(
			'comic-sans' as unknown as ReaderFontModule['READER_FONT_OPTIONS'][number]['value']
		);
		expect(css).toBe(READER_FONT_OPTIONS[0].css);
	});
});

describe('readStoredFont (via readerFont initial value)', () => {
	it('uses the default font outside the browser (no localStorage read)', async () => {
		browserFlag = false;
		// Make any accidental read explode so we prove it is skipped.
		vi.stubGlobal('localStorage', {
			getItem: () => {
				throw new Error('localStorage must not be read outside the browser');
			}
		});
		const { readerFont } = await freshFontModule();
		expect(readerFont.value).toBe('pt-serif');
	});

	it('restores a valid persisted font', async () => {
		browserFlag = true;
		stubLocalStorage({ 'reader-font': 'merriweather' });
		const { readerFont } = await freshFontModule();
		expect(readerFont.value).toBe('merriweather');
	});

	it('restores libre-baskerville as a valid persisted font', async () => {
		browserFlag = true;
		stubLocalStorage({ 'reader-font': 'libre-baskerville' });
		const { readerFont } = await freshFontModule();
		expect(readerFont.value).toBe('libre-baskerville');
	});

	it('falls back to the default for an invalid persisted value', async () => {
		browserFlag = true;
		stubLocalStorage({ 'reader-font': 'comic-sans' });
		const { readerFont } = await freshFontModule();
		expect(readerFont.value).toBe('pt-serif');
	});

	it('falls back to the default when nothing is persisted', async () => {
		browserFlag = true;
		stubLocalStorage();
		const { readerFont } = await freshFontModule();
		expect(readerFont.value).toBe('pt-serif');
	});
});

describe('setReaderFont', () => {
	it('updates the reactive value and persists in the browser', async () => {
		browserFlag = true;
		const store = stubLocalStorage();
		const { setReaderFont, readerFont } = await freshFontModule();
		setReaderFont('libre-baskerville');
		expect(readerFont.value).toBe('libre-baskerville');
		expect(store.get('reader-font')).toBe('libre-baskerville');
	});

	it('updates the value but does not persist outside the browser', async () => {
		browserFlag = false;
		vi.stubGlobal('localStorage', {
			setItem: () => {
				throw new Error('localStorage must not be written outside the browser');
			}
		});
		const { setReaderFont, readerFont } = await freshFontModule();
		setReaderFont('merriweather');
		expect(readerFont.value).toBe('merriweather');
	});
});
