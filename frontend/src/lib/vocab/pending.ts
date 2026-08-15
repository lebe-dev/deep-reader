/**
 * The copy shown for a saved term whose translation has not arrived yet
 * (WORD-CACHE-ARCH.md §18.3).
 *
 * Saving works offline by construction: the entry is written locally and the
 * translation is fetched by the outbox drain whenever a connection exists. The
 * UI must say which of those two states it is in — "Translating…" while offline
 * is a lie, and it is the kind of lie that makes a user tap the same word again
 * wondering whether the app lost it.
 *
 * Kept as pure functions in one module so the reader popover, the /words row and
 * the save toast cannot drift apart, and so the wording is unit-testable.
 *
 * Named exports only; no default export.
 */

/**
 * Full-width label for the reader popover: the panel is 288px wide and wraps,
 * so it can afford a sentence that explains what happens next.
 */
export function pendingTranslationLabel(online: boolean): string {
	return online ? 'Translating…' : 'Offline — will translate when you reconnect';
}

/**
 * Short label for the /words row, which is one truncated line next to the word.
 */
export function pendingTranslationShortLabel(online: boolean): string {
	return online ? 'Translating…' : 'Waiting for connection';
}

/**
 * The toast shown right after a manual save. It always leads with the fact that
 * the save itself succeeded — that part is never in doubt, online or not — and
 * only then says where the translation stands.
 */
export function savedTermMessage(surface: string, translated: boolean, online: boolean): string {
	const saved = `Saved “${surface}”`;
	if (translated) return `${saved}.`;
	return online ? `${saved} — translating…` : `${saved} — will translate when you're back online.`;
}
