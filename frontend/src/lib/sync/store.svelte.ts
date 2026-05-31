// Reactive sync-status store (Svelte 5 runes).
// Drives the UI connectivity indicator, pending-count badge, and last-synced time.
// Spec §6 "Sync engine", §10 "Offline-first".
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { sync } from './engine';
import { db, getSyncState } from '$lib/db';
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
		// Refresh reactive state after a successful sync.
		const state = await getSyncState();
		syncStatus.lastSyncedAt = state.cursor; // cursor doubles as last-sync timestamp
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

	// Seed online state.
	syncStatus.online = navigator.onLine;

	// Seed the cached markdown budget so the UI shows it even before (or without)
	// a fresh sync — e.g. when the app opens offline.
	getSyncState()
		.then((state) => {
			syncStatus.markdownBudget = state.markdownBudget;
		})
		.catch(console.warn);

	function handleOnline() {
		syncStatus.online = true;
		runSync().catch(console.warn);
	}

	function handleOffline() {
		syncStatus.online = false;
	}

	function handleFocus() {
		runSync().catch(console.warn);
	}

	window.addEventListener('online', handleOnline);
	window.addEventListener('offline', handleOffline);
	window.addEventListener('focus', handleFocus);

	// Initial sync on mount.
	runSync().catch(console.warn);

	// Periodic foreground sync.
	intervalHandle = setInterval(() => {
		if (syncStatus.online) runSync().catch(console.warn);
	}, FOREGROUND_INTERVAL_MS);

	// Live-update pending count whenever the outbox table changes.
	db.outbox.hook('creating', () => {
		void refreshPending();
	});

	return () => {
		window.removeEventListener('online', handleOnline);
		window.removeEventListener('offline', handleOffline);
		window.removeEventListener('focus', handleFocus);
		clearInterval(intervalHandle);
		// Dexie hooks are unsubscribed by calling the returned hook instance.
		// The hook API doesn't return a cleanup fn in the same way, but we
		// can safely ignore it for a singleton store that lives for the app lifetime.
	};
}
