// Reader font preference — persisted to localStorage.
// Named exports only; no default export.

import { browser } from '$app/environment';

export type ReaderFont = 'pt-serif' | 'libre-baskerville' | 'merriweather';

export interface ReaderFontOption {
	value: ReaderFont;
	label: string;
	css: string;
}

export const READER_FONT_OPTIONS: ReaderFontOption[] = [
	{ value: 'pt-serif', label: 'PT Serif', css: "'PT Serif', serif" },
	{ value: 'libre-baskerville', label: 'Libre Baskerville', css: "'Libre Baskerville', serif" },
	{ value: 'merriweather', label: 'Merriweather', css: "'Merriweather', serif" }
];

const STORAGE_KEY = 'reader-font';
const DEFAULT_FONT: ReaderFont = 'pt-serif';

function readStoredFont(): ReaderFont {
	if (!browser) return DEFAULT_FONT;
	const stored = localStorage.getItem(STORAGE_KEY);
	if (stored === 'pt-serif' || stored === 'libre-baskerville' || stored === 'merriweather') {
		return stored;
	}
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
