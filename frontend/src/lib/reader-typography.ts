// Reader typography presets — the single source of truth mapping the synced
// Settings.font_size / Settings.line_height enums to concrete CSS values.
// Shared by the Appearance settings tab (preview) and the article reader.
// Named exports only; no default export.

import type { FontSize, LineHeight } from '$lib/types';

export interface FontSizeOption {
	value: FontSize;
	label: string;
	/** CSS font-size applied to the reader body. */
	rem: string;
}

export interface LineHeightOption {
	value: LineHeight;
	label: string;
	/** Unitless line-height multiplier (scales with any font size). */
	multiplier: number;
}

// 'm' / 'normal' reproduce the previously hard-coded reader typography
// (17px text, 1.8 line-height) and are the seeded backend defaults.
export const FONT_SIZE_OPTIONS: FontSizeOption[] = [
	{ value: 's', label: 'S', rem: '0.9375rem' },
	{ value: 'm', label: 'M', rem: '1.0625rem' },
	{ value: 'l', label: 'L', rem: '1.25rem' },
	{ value: 'xl', label: 'XL', rem: '1.5rem' }
];

export const LINE_HEIGHT_OPTIONS: LineHeightOption[] = [
	{ value: 'compact', label: 'Compact', multiplier: 1.5 },
	{ value: 'normal', label: 'Normal', multiplier: 1.8 },
	{ value: 'relaxed', label: 'Relaxed', multiplier: 2.1 }
];

export const DEFAULT_FONT_SIZE: FontSize = 'm';
export const DEFAULT_LINE_HEIGHT: LineHeight = 'normal';

/** CSS font-size for a preset, falling back to the default on unknown input. */
export function fontSizeRem(value: FontSize | undefined): string {
	const opt = FONT_SIZE_OPTIONS.find((o) => o.value === value);
	return (opt ?? FONT_SIZE_OPTIONS.find((o) => o.value === DEFAULT_FONT_SIZE)!).rem;
}

/** Line-height multiplier for a preset, falling back to the default. */
export function lineHeightMultiplier(value: LineHeight | undefined): number {
	const opt = LINE_HEIGHT_OPTIONS.find((o) => o.value === value);
	return (opt ?? LINE_HEIGHT_OPTIONS.find((o) => o.value === DEFAULT_LINE_HEIGHT)!).multiplier;
}
