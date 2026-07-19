// Reactive sync-status store (Svelte 5 runes).
// Drives the UI connectivity indicator, pending-count badge, and last-synced time.
// Spec §6 "Sync engine", §10 "Offline-first".
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { sync } from './engine';
import { db, getSyncState } from '$lib/db';
import { isOnline, onNetworkChange } from '$lib/platform/network';
import { onForeground } from '$lib/platform/lifecycle';
import type { MarkdownBudget } from '$lib/types';

// ---------------------------------------------------------------------------
// Reactive state (Svelte 5 $state rune)
// ---------------------------------------------------------------------------

export interface SyncStatus {
	/** Whether the browser believes it has network access. */
	online: boolean;
	/** ISO timestamp of the last successful pull, or undefined. */
	lastSyncedAt: string | undefined;
	/** Number of outbox entries waiting to be flushed. */
	pending: number;
	/** Last sync error, if any. */
	error?: string;
	/** markdown.new daily budget from the last successful pull. */
	markdownBudget?: MarkdownBudget;
}

export const syncStatus = $state<SyncStatus>({
	online: browser ? navigator.onLine : true,
	lastSyncedAt: undefined,
	pending: 0,
	error: undefined,
	markdownBudget: undefined
});

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

let syncInFlight = false;

async function runSync(): Promise<void> {
	if (syncInFlight) return;
	syncInFlight = true;
	syncStatus.error = undefined;

	try {
		await sync();
		// Refresh reactive state after a successful sync. Stamp the last-synced
		// time from a real local clock rather than the server cursor: the cursor
		// wire contract (server_time vs cursor) leaves state.cursor undefined, so
		// binding the indicator to it meant "Synced" never appeared. A local
		// timestamp is the correct source for a "last successful pull" UI label.
		const state = await getSyncState();
		syncStatus.lastSyncedAt = new Date().toISOString();
		syncStatus.markdownBudget = state.markdownBudget;
		syncStatus.pending = await db.outbox.count();
	} catch (err) {
		syncStatus.error = err instanceof Error ? err.message : String(err);
	} finally {
		syncInFlight = false;
	}
}

async function refreshPending(): Promise<void> {
	if (!browser) return;
	syncStatus.pending = await db.outbox.count();
}

// ---------------------------------------------------------------------------
// Lifecycle — call initSync() once from the root layout.
// ---------------------------------------------------------------------------

const FOREGROUND_INTERVAL_MS = 60_000; // 1 minute

let intervalHandle: ReturnType<typeof setInterval> | undefined;

/**
 * Initialise event listeners and start the foreground sync interval.
 * Call once from the SvelteKit root layout's `onMount`.
 * Returns a cleanup function to call on `onDestroy`.
 */
export function initSync(): () => void {
	if (!browser) return () => {};

	// Seed online state. navigator.onLine is a fine synchronous first guess; the
	// authoritative status (native plugin) resolves right after.
	syncStatus.online = navigator.onLine;
	isOnline()
		.then((online) => {
			syncStatus.online = online;
		})
		.catch(console.warn);

	// Seed the cached markdown budget so the UI shows it even before (or without)
	// a fresh sync — e.g. when the app opens offline.
	getSyncState()
		.then((state) => {
			syncStatus.markdownBudget = state.markdownBudget;
		})
		.catch(console.warn);

	// Live-update pending count whenever the outbox table changes. Named so the
	// cleanup below can unsubscribe it (an inline arrow could not be removed).
	function handleOutboxCreating() {
		void refreshPending();
	}

	// Connectivity and foreground triggers come from the platform layer: window
	// events on web, @capacitor/network + @capacitor/app on native (§6.3, §6.4).
	const offNetwork = onNetworkChange((online) => {
		syncStatus.online = online;
		if (online) runSync().catch(console.warn);
	});
	const offForeground = onForeground(() => {
		runSync().catch(console.warn);
	});
	db.outbox.hook('creating', handleOutboxCreating);

	// Initial sync on mount.
	runSync().catch(console.warn);

	// Periodic foreground sync.
	intervalHandle = setInterval(() => {
		if (syncStatus.online) runSync().catch(console.warn);
	}, FOREGROUND_INTERVAL_MS);

	return () => {
		offNetwork();
		offForeground();
		clearInterval(intervalHandle);
		intervalHandle = undefined;
		// Unsubscribe the Dexie hook via its DexieEvent so the listener is fully
		// torn down (the inline subscribe form has no return handle to remove).
		db.outbox.hook.creating.unsubscribe(handleOutboxCreating);
	};
}
