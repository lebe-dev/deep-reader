// Reader enrichment-marker visibility — persisted to localStorage.
// When off, the difficult-word / phrase underlines are hidden so the text reads
// clean; tokens stay interactive (tap still opens the translation). Named
// exports only; no default export. Mirrors reader-font.svelte.ts.

import { browser } from '$app/environment';

const STORAGE_KEY = 'reader-marks';
const DEFAULT_SHOW = true;

function readStoredShow(): boolean {
	if (!browser) return DEFAULT_SHOW;
	// Only an explicit "0" turns markers off; anything else keeps the default on.
	return localStorage.getItem(STORAGE_KEY) !== '0';
}

export const readerMarks = $state({ show: readStoredShow() });

export function setReaderMarks(show: boolean) {
	readerMarks.show = show;
	if (browser) localStorage.setItem(STORAGE_KEY, show ? '1' : '0');
}
