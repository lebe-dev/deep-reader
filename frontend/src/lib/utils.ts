import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChild<T> = T extends { child?: any } ? Omit<T, 'child'> : T;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, 'children'> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };

/**
 * Enrichment-completeness threshold: below this sentence-coverage fraction the
 * UI flags an article as only partially enriched (the LLM left part of the text
 * untranslated).
 */
export const COVERAGE_WARN_THRESHOLD = 0.9;

export interface CoverageDisplay {
	/** Rounded percentage 0–100. */
	pct: number;
	/** True when coverage is below the warning threshold (incomplete enrichment). */
	low: boolean;
}

/**
 * Derive the enrichment-coverage display for a coverage fraction in [0,1].
 * Non-finite input (e.g. an older cached payload missing the field) is treated
 * as fully enriched so we never flash a spurious warning for legacy data.
 */
export function coverageDisplay(coverage: number): CoverageDisplay {
	if (!Number.isFinite(coverage)) return { pct: 100, low: false };
	const clamped = Math.max(0, Math.min(1, coverage));
	return { pct: Math.round(clamped * 100), low: coverage < COVERAGE_WARN_THRESHOLD };
}
