import { describe, it, expect } from 'vitest';
import { coverageDisplay, COVERAGE_WARN_THRESHOLD } from './utils';

describe('coverageDisplay', () => {
	it('reports full coverage as 100% and not low', () => {
		expect(coverageDisplay(1)).toEqual({ pct: 100, low: false });
	});

	it('rounds the fraction to a whole percent', () => {
		expect(coverageDisplay(0.736).pct).toBe(74);
		expect(coverageDisplay(0.734).pct).toBe(73);
	});

	it('flags coverage below the warning threshold as low', () => {
		const justBelow = coverageDisplay(COVERAGE_WARN_THRESHOLD - 0.01);
		expect(justBelow.low).toBe(true);
	});

	it('does not flag coverage exactly at the threshold', () => {
		expect(coverageDisplay(COVERAGE_WARN_THRESHOLD).low).toBe(false);
	});

	it('clamps out-of-range fractions into 0–100 for display', () => {
		expect(coverageDisplay(1.5)).toEqual({ pct: 100, low: false });
		expect(coverageDisplay(-0.2).pct).toBe(0);
		// Negative is still below the threshold, so it stays flagged.
		expect(coverageDisplay(-0.2).low).toBe(true);
	});

	it('treats non-finite input (legacy cached payload) as fully enriched', () => {
		expect(coverageDisplay(NaN)).toEqual({ pct: 100, low: false });
		expect(coverageDisplay(Infinity)).toEqual({ pct: 100, low: false });
		// undefined slips through untyped boundaries (older IndexedDB rows).
		expect(coverageDisplay(undefined as unknown as number)).toEqual({ pct: 100, low: false });
	});
});
