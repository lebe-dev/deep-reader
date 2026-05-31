// Persistent storage request — reduces eviction risk on iOS (spec §6, §10).
// Call once on first app launch / after the user grants permission.

/**
 * Request persistent storage from the browser.
 * Returns true if the permission was granted, false otherwise.
 * No-ops gracefully when the Storage API is unavailable (SSR / old browsers).
 */
export async function requestPersistentStorage(): Promise<boolean> {
	if (typeof navigator === 'undefined' || !navigator.storage?.persist) return false;

	try {
		const persisted = await navigator.storage.persist();
		return persisted;
	} catch {
		return false;
	}
}
