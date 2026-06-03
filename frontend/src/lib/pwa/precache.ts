// Resilient precache for the service-worker install step.
//
// The browser's Cache.addAll is ATOMIC: if a single asset can't be fetched with
// a 2xx response, the whole promise rejects, the SW install fails, and the page
// sees "Failed to execute 'addAll' on 'Cache': Request failed" — leaving the PWA
// with no installed worker at all.
//
// Across deploys this is easy to hit: the service-worker.js is served no-cache
// (always fresh) while a momentarily-unavailable font, a stale CDN edge, or a
// mid-deploy asset-hash swap makes one entry 404. One missing optional asset
// must not brick the entire install — so we add each asset independently and
// report the failures instead of aborting.

/** Minimal slice of the Cache interface we depend on — keeps this unit-testable. */
export interface CacheLike {
	add(request: string): Promise<void>;
}

/**
 * Add every asset to the cache, tolerating individual failures.
 * Never rejects; returns the assets that could not be cached so the caller can
 * log them.
 */
export async function precacheAll(cache: CacheLike, assets: string[]): Promise<string[]> {
	const results = await Promise.allSettled(assets.map((asset) => cache.add(asset)));
	return assets.filter((_, i) => results[i].status === 'rejected');
}
