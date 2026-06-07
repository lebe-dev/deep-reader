import { describe, it, expect } from 'vitest';
import type { ArticleMeta } from '$lib/types';
import { readingProgressPercent, compareLibrary, sortLibrary } from './library-utils';

// Minimal ArticleMeta factory — only the fields the helpers read matter; the
// rest are filled with sane defaults.
function meta(over: Partial<ArticleMeta> & Pick<ArticleMeta, 'id'>): ArticleMeta {
	return {
		source_url: 'https://example.com',
		title: 'T',
		author: '',
		source_domain: 'example.com',
		lang: 'en',
		status: 'enriched',
		pinned: false,
		enrichment_version: 1,
		created_at: '2024-01-01T00:00:00Z',
		updated_at: '2024-01-01T00:00:00Z',
		token_count: 100,
		enrichment_coverage: 1,
		...over
	};
}

describe('readingProgressPercent', () => {
	it('returns 0 when token count is zero or negative', () => {
		expect(readingProgressPercent(5, 0)).toBe(0);
		expect(readingProgressPercent(5, -3)).toBe(0);
	});

	it('returns 0 for a negative position', () => {
		expect(readingProgressPercent(-1, 100)).toBe(0);
	});

	it('counts the furthest-seen token as seen (position + 1)', () => {
		// Token index 0 of 100 → 1/100 = 1%.
		expect(readingProgressPercent(0, 100)).toBe(1);
		// Halfway: index 49 of 100 → 50/100 = 50%.
		expect(readingProgressPercent(49, 100)).toBe(50);
	});

	it('reaches 100% at the last token', () => {
		expect(readingProgressPercent(99, 100)).toBe(100);
	});

	it('clamps an out-of-range position to 100', () => {
		expect(readingProgressPercent(500, 100)).toBe(100);
	});

	it('rounds to the nearest integer', () => {
		// index 0 of 3 → 1/3 = 33.33% → 33.
		expect(readingProgressPercent(0, 3)).toBe(33);
	});
});

describe('compareLibrary', () => {
	it('orders pinned articles before unpinned ones', () => {
		const pinned = meta({ id: 'a', pinned: true });
		const plain = meta({ id: 'b', pinned: false });
		expect(compareLibrary(pinned, plain, new Set())).toBeLessThan(0);
		expect(compareLibrary(plain, pinned, new Set())).toBeGreaterThan(0);
	});

	it('orders unread articles before read ones', () => {
		const a = meta({ id: 'a' });
		const b = meta({ id: 'b' });
		const read = new Set(['b']);
		expect(compareLibrary(a, b, read)).toBeLessThan(0);
	});

	it('prioritises pin over read state', () => {
		const pinnedRead = meta({ id: 'a', pinned: true });
		const unpinnedUnread = meta({ id: 'b', pinned: false });
		const read = new Set(['a']);
		// Pinned-but-read still comes before unpinned-unread.
		expect(compareLibrary(pinnedRead, unpinnedUnread, read)).toBeLessThan(0);
	});

	it('treats equal pin+read state as equal', () => {
		const a = meta({ id: 'a' });
		const b = meta({ id: 'b' });
		expect(compareLibrary(a, b, new Set())).toBe(0);
	});

	it('orders processing articles before pinned ones', () => {
		const processing = meta({ id: 'a', status: 'enriching' });
		const pinned = meta({ id: 'b', pinned: true });
		expect(compareLibrary(processing, pinned, new Set())).toBeLessThan(0);
		expect(compareLibrary(pinned, processing, new Set())).toBeGreaterThan(0);
	});

	it('treats all in-flight statuses as processing', () => {
		const statuses = ['queued', 'fetching', 'fetched', 'enriching'] as const;
		const enriched = meta({ id: 'z' });
		for (const status of statuses) {
			const a = meta({ id: 'a', status });
			expect(compareLibrary(a, enriched, new Set())).toBeLessThan(0);
		}
	});
});

describe('sortLibrary', () => {
	it('groups processing first, then pinned, then unread, then read', () => {
		// Input is pre-sorted by created_at desc: newest first.
		const input = [
			meta({ id: 'newest-read' }),
			meta({ id: 'newest-unread' }),
			meta({ id: 'pinned-old', pinned: true }),
			meta({ id: 'processing', status: 'enriching' }),
			meta({ id: 'old-unread' })
		];
		const read = new Set(['newest-read']);

		const ordered = sortLibrary(input, read).map((a) => a.id);

		expect(ordered).toEqual([
			'processing',
			'pinned-old',
			'newest-unread',
			'old-unread',
			'newest-read'
		]);
	});

	it('does not mutate the input array', () => {
		const input = [meta({ id: 'a' }), meta({ id: 'b', pinned: true })];
		const copy = [...input];
		sortLibrary(input, new Set());
		expect(input).toEqual(copy);
	});
});
