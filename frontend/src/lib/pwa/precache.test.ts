import { describe, it, expect } from 'vitest';
import { precacheAll, type CacheLike } from './precache';

describe('precacheAll', () => {
	it('caches every asset and reports no failures when all succeed', async () => {
		const added: string[] = [];
		const cache: CacheLike = {
			add: async (r) => {
				added.push(r);
			}
		};

		const failed = await precacheAll(cache, ['/a', '/b', '/c']);

		expect(failed).toEqual([]);
		expect(added).toEqual(['/a', '/b', '/c']);
	});

	it('still caches the good assets and returns only the ones that failed', async () => {
		const added: string[] = [];
		const cache: CacheLike = {
			add: async (r) => {
				if (r === '/b') throw new Error('Request failed');
				added.push(r);
			}
		};

		const failed = await precacheAll(cache, ['/a', '/b', '/c']);

		expect(failed).toEqual(['/b']);
		expect(added).toEqual(['/a', '/c']);
	});

	it('resolves (never rejects) even when every asset fails', async () => {
		const cache: CacheLike = {
			add: async () => {
				throw new Error('boom');
			}
		};

		await expect(precacheAll(cache, ['/x', '/y'])).resolves.toEqual(['/x', '/y']);
	});
});
