// Reader font preference — persisted to localStorage.
// Named exports only; no default export.

import { browser } from '$app/environment';

export type ReaderFont =
	| 'pt-serif'
	| 'libre-baskerville'
	| 'merriweather'
	| 'atkinson-hyperlegible';

export interface ReaderFontOption {
	value: ReaderFont;
	label: string;
	css: string;
	/** Shown under the picker; empty for the plain typographic choices. */
	note?: string;
}

export const READER_FONT_OPTIONS: ReaderFontOption[] = [
	{ value: 'pt-serif', label: 'PT Serif', css: "'PT Serif', serif" },
	{ value: 'libre-baskerville', label: 'Libre Baskerville', css: "'Libre Baskerville', serif" },
	{ value: 'merriweather', label: 'Merriweather', css: "'Merriweather', serif" },
	{
		value: 'atkinson-hyperlegible',
		label: 'Atkinson Hyperlegible',
		css: "'Atkinson Hyperlegible', sans-serif",
		note: 'Designed for low vision — letterforms that are hard to confuse.'
	}
];

const STORAGE_KEY = 'reader-font';
const DEFAULT_FONT: ReaderFont = 'pt-serif';

function readStoredFont(): ReaderFont {
	if (!browser) return DEFAULT_FONT;
	const stored = localStorage.getItem(STORAGE_KEY);
	if (READER_FONT_OPTIONS.some((o) => o.value === stored)) return stored as ReaderFont;
	return DEFAULT_FONT;
}

export const readerFont = $state({ value: readStoredFont() });

export function setReaderFont(font: ReaderFont) {
	readerFont.value = font;
	if (browser) localStorage.setItem(STORAGE_KEY, font);
}

export function getReaderFontCss(font: ReaderFont): string {
	return READER_FONT_OPTIONS.find((o) => o.value === font)?.css ?? READER_FONT_OPTIONS[0].css;
}
