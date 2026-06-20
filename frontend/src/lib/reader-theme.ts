// Reader theme — a three-way light / sepia / dark selector layered on top of
// mode-watcher. Light and dark map to mode-watcher's mode (the `.dark` class);
// sepia is a light-mode custom theme applied as `data-theme="sepia"` on <html>
// and styled in app.css. Keeping the three mutually exclusive matches how
// readers think about a reading palette. Named exports only.

import { setMode, setTheme } from 'mode-watcher';

export type ReaderTheme = 'light' | 'sepia' | 'dark';

export interface ReaderThemeOption {
	value: ReaderTheme;
	label: string;
}

export const READER_THEME_OPTIONS: ReaderThemeOption[] = [
	{ value: 'light', label: 'Light' },
	{ value: 'sepia', label: 'Sepia' },
	{ value: 'dark', label: 'Dark' }
];

/** Derive the active reader theme from mode-watcher's mode + custom theme. */
export function resolveReaderTheme(
	mode: 'light' | 'dark' | undefined,
	theme: string | undefined
): ReaderTheme {
	if (theme === 'sepia') return 'sepia';
	return mode === 'dark' ? 'dark' : 'light';
}

/** Apply a reader theme via mode-watcher (mode class + data-theme attribute). */
export function applyReaderTheme(value: ReaderTheme) {
	if (value === 'sepia') {
		// Sepia is a light-based palette; force light mode, then layer the theme.
		setMode('light');
		setTheme('sepia');
		return;
	}
	// Clear any custom theme so the plain light/dark variables apply.
	setTheme('');
	setMode(value);
}
