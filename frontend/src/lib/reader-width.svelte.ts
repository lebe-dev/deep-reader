// Reader column width preference — persisted to localStorage.
// Constrains the reading measure (line length) for comfortable reading; an
// over-wide column is the single biggest readability regression. Named exports
// only; no default export. Mirrors reader-font.svelte.ts.

import { browser } from '$app/environment';

export type ReaderWidth = 'narrow' | 'medium' | 'wide';

export interface ReaderWidthOption {
	value: ReaderWidth;
	label: string;
	/** CSS max-width applied to the reader content column. */
	rem: string;
}

// 'medium' (~40rem ≈ 640px) keeps a ~66-character measure for the default body
// size — the readability optimum. 'wide' reproduces the previous full-container
// width. All values stay within the page's max-w-3xl (48rem) main container.
export const READER_WIDTH_OPTIONS: ReaderWidthOption[] = [
	{ value: 'narrow', label: 'Narrow', rem: '32rem' },
	{ value: 'medium', label: 'Medium', rem: '40rem' },
	{ value: 'wide', label: 'Wide', rem: '48rem' }
];

const STORAGE_KEY = 'reader-width';
const DEFAULT_WIDTH: ReaderWidth = 'medium';

function readStoredWidth(): ReaderWidth {
	if (!browser) return DEFAULT_WIDTH;
	const stored = localStorage.getItem(STORAGE_KEY);
	if (stored === 'narrow' || stored === 'medium' || stored === 'wide') return stored;
	return DEFAULT_WIDTH;
}

export const readerWidth = $state({ value: readStoredWidth() });

export function setReaderWidth(width: ReaderWidth) {
	readerWidth.value = width;
	if (browser) localStorage.setItem(STORAGE_KEY, width);
}

/** CSS max-width for a preset, falling back to the default on unknown input. */
export function getReaderWidthRem(width: ReaderWidth): string {
	return (
		READER_WIDTH_OPTIONS.find((o) => o.value === width) ??
		READER_WIDTH_OPTIONS.find((o) => o.value === DEFAULT_WIDTH)!
	).rem;
}
