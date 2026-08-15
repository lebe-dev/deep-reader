// Unit tests for the vocabulary change notifier.

import { describe, it, expect, vi } from 'vitest';
import { notifyVocabChanged, onVocabChanged } from './changes';

describe('onVocabChanged', () => {
	it('calls every subscriber on notify', () => {
		const a = vi.fn();
		const b = vi.fn();
		const offA = onVocabChanged(a);
		const offB = onVocabChanged(b);

		notifyVocabChanged();

		expect(a).toHaveBeenCalledTimes(1);
		expect(b).toHaveBeenCalledTimes(1);
		offA();
		offB();
	});

	it('stops calling a subscriber after it unsubscribes', () => {
		const listener = vi.fn();
		const off = onVocabChanged(listener);
		off();

		notifyVocabChanged();

		expect(listener).not.toHaveBeenCalled();
	});

	it('registering the same function twice still notifies it once', () => {
		const listener = vi.fn();
		const off1 = onVocabChanged(listener);
		const off2 = onVocabChanged(listener);

		notifyVocabChanged();

		expect(listener).toHaveBeenCalledTimes(1);
		off1();
		off2();
	});

	it('keeps notifying the others when one subscriber throws', () => {
		const boom = vi.fn(() => {
			throw new Error('subscriber exploded');
		});
		const after = vi.fn();
		const offBoom = onVocabChanged(boom);
		const offAfter = onVocabChanged(after);

		expect(() => notifyVocabChanged()).not.toThrow();
		expect(after).toHaveBeenCalledTimes(1);
		offBoom();
		offAfter();
	});

	it('is a no-op with no subscribers', () => {
		expect(() => notifyVocabChanged()).not.toThrow();
	});
});
