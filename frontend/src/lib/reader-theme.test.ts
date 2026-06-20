import { describe, it, expect, vi, beforeEach } from 'vitest';

const setMode = vi.fn();
const setTheme = vi.fn();
vi.mock('mode-watcher', () => ({
	setMode: (m: string) => setMode(m),
	setTheme: (t: string) => setTheme(t)
}));

import { resolveReaderTheme, applyReaderTheme } from './reader-theme';

beforeEach(() => {
	setMode.mockClear();
	setTheme.mockClear();
});

describe('resolveReaderTheme', () => {
	it('reports sepia whenever the custom theme is sepia, regardless of mode', () => {
		expect(resolveReaderTheme('light', 'sepia')).toBe('sepia');
		expect(resolveReaderTheme('dark', 'sepia')).toBe('sepia');
	});

	it('maps mode to light / dark when no custom theme is set', () => {
		expect(resolveReaderTheme('dark', '')).toBe('dark');
		expect(resolveReaderTheme('light', '')).toBe('light');
		expect(resolveReaderTheme(undefined, undefined)).toBe('light');
	});
});

describe('applyReaderTheme', () => {
	it('forces light mode and layers the sepia data-theme', () => {
		applyReaderTheme('sepia');
		expect(setMode).toHaveBeenCalledWith('light');
		expect(setTheme).toHaveBeenCalledWith('sepia');
	});

	it('clears the custom theme and sets the mode for light / dark', () => {
		applyReaderTheme('dark');
		expect(setTheme).toHaveBeenCalledWith('');
		expect(setMode).toHaveBeenCalledWith('dark');

		setMode.mockClear();
		setTheme.mockClear();
		applyReaderTheme('light');
		expect(setTheme).toHaveBeenCalledWith('');
		expect(setMode).toHaveBeenCalledWith('light');
	});
});
