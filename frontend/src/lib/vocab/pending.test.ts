// Unit tests for the pending-translation copy. The point of these is the
// offline wording: a term saved without a connection must never claim to be
// translating (WORD-CACHE-ARCH.md §18.3).

import { describe, it, expect } from 'vitest';
import { pendingTranslationLabel, pendingTranslationShortLabel, savedTermMessage } from './pending';

describe('pendingTranslationLabel', () => {
	it('says the work is in flight when online', () => {
		expect(pendingTranslationLabel(true)).toBe('Translating…');
	});

	it('promises a later translation when offline, rather than pretending to work', () => {
		const label = pendingTranslationLabel(false);
		expect(label).toContain('Offline');
		expect(label).not.toContain('Translating…');
	});
});

describe('pendingTranslationShortLabel', () => {
	it('stays short enough for a single dense row', () => {
		expect(pendingTranslationShortLabel(true)).toBe('Translating…');
		expect(pendingTranslationShortLabel(false).length).toBeLessThanOrEqual(24);
	});

	it('names the connection as the thing being waited on when offline', () => {
		expect(pendingTranslationShortLabel(false)).toBe('Waiting for connection');
	});
});

describe('savedTermMessage', () => {
	it('confirms the save alone when the translation is already there', () => {
		expect(savedTermMessage('resilient', true, true)).toBe('Saved “resilient”.');
		// Reusing an enrichment translation works offline too, so connectivity
		// must not change this message.
		expect(savedTermMessage('resilient', true, false)).toBe('Saved “resilient”.');
	});

	it('says the translation is running when online', () => {
		expect(savedTermMessage('resilient', false, true)).toBe('Saved “resilient” — translating…');
	});

	it('tells the user the save survived and the translation comes later when offline', () => {
		const msg = savedTermMessage('resilient', false, false);
		expect(msg).toContain('Saved “resilient”');
		expect(msg).toContain('back online');
	});

	it('always leads with the save, whatever the state', () => {
		for (const translated of [true, false]) {
			for (const online of [true, false]) {
				expect(
					savedTermMessage('take off', translated, online).startsWith('Saved “take off”')
				).toBe(true);
			}
		}
	});
});
