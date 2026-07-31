import { describe, expect, it } from 'vitest';
import { formatExpiry, formatTtl } from './publish-utils';

describe('formatTtl', () => {
	it('reports a zero or negative lifetime as never expiring', () => {
		expect(formatTtl(0)).toBe('never');
		expect(formatTtl(-5)).toBe('never');
	});

	it('keeps sub-day lifetimes in hours', () => {
		expect(formatTtl(1)).toBe('1 hour');
		expect(formatTtl(12)).toBe('12 hours');
		expect(formatTtl(23)).toBe('23 hours');
	});

	it('scales the unit with the lifetime', () => {
		expect(formatTtl(24)).toBe('1 day');
		expect(formatTtl(72)).toBe('3 days');
		expect(formatTtl(24 * 14)).toBe('2 weeks');
		expect(formatTtl(24 * 60)).toBe('2 months');
		expect(formatTtl(8760)).toBe('1 year');
	});
});

describe('formatExpiry', () => {
	const now = new Date('2026-07-31T12:00:00Z');

	it('returns null when the link never expires', () => {
		expect(formatExpiry(undefined, now)).toBeNull();
		expect(formatExpiry('', now)).toBeNull();
	});

	it('returns null for an unparseable deadline rather than NaN wording', () => {
		expect(formatExpiry('not a date', now)).toBeNull();
	});

	it('counts down in the largest sensible unit', () => {
		expect(formatExpiry('2026-08-03T12:00:00Z', now)).toBe('Expires in 3 days');
		expect(formatExpiry('2026-07-31T18:00:00Z', now)).toBe('Expires in 6 hours');
		expect(formatExpiry('2026-07-31T12:30:00Z', now)).toBe('Expires in 30 minutes');
	});

	it('reports an elapsed deadline as expired, never as a negative duration', () => {
		expect(formatExpiry('2026-07-31T11:00:00Z', now)).toBe('Expired');
		expect(formatExpiry('2026-07-31T12:00:00Z', now)).toBe('Expired');
	});
});
