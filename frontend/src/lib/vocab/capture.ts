/**
 * Capture: recording every word or phrase the reader taps for a translation
 * (WORD-CACHE-ARCH.md §5).
 *
 * Capture happens where the popover content is RESOLVED, not inside
 * TokenRenderer — that component is a deliberately dumb renderer with no
 * article id. It is fire-and-forget: a capture failure must never break the
 * popover the user is actually looking at.
 */

import { enqueueOutbox, db } from '$lib/db';
import { captureError } from '$lib/sentry';
import { findCoveringSentence, sliceText } from '$lib/components/reader/reader-utils';
import type { PopoverContent } from '$lib/components/reader/reader-utils';
import type { Enrichment, LookupEvent, LookupKind, Token, VocabEntry } from '$lib/types';
import { lemmaOf, phraseEntryKey, wordEntryKey } from './key';

/** Context sentences are truncated to this many characters, on a word boundary. */
export const MAX_CONTEXT_LEN = 300;

/**
 * Positions already captured during the life of this reader page, keyed by
 * `${articleId}:${kind}:${spanStart}`.
 *
 * This only avoids queuing redundant outbox rows while the page is open; the
 * authoritative de-duplication is the SQLite unique index, which holds across
 * sessions and devices. Re-tapping a word is therefore free, and no debounce or
 * dwell timer is needed anywhere.
 */
const capturedThisSession = new Set<string>();

/** Reset the in-memory dedup set (called when the reader loads an article). */
export function resetCaptureSession(): void {
	capturedThisSession.clear();
}

/** Everything captureLookup needs from the reader page. */
export interface CaptureInput {
	articleId: string;
	articleTitle: string;
	content: PopoverContent;
	tokens: Token[];
	originalText: string;
	enrichment: Enrichment;
	/** Target translation language, baked into the entry key (§17.1). */
	targetLang: string;
}

/**
 * Record one lookup: build the event, queue it for the backend, and optimistically
 * update the local aggregate so the word lights up elsewhere in this article
 * immediately.
 *
 * Returns the event that was queued, or `null` when the tap was a repeat of a
 * position already captured on this page.
 */
export async function captureLookup(input: CaptureInput): Promise<LookupEvent | null> {
	const event = buildLookupEvent(input);
	if (event === null) return null;

	const dedupKey = `${event.article_id}:${event.kind}:${event.span_start}`;
	if (capturedThisSession.has(dedupKey)) return null;
	capturedThisSession.add(dedupKey);

	try {
		await enqueueOutbox('lookup', event);
		await upsertLocalEntry(event);
	} catch (err) {
		// Roll back the dedup marker so a later tap can retry the capture.
		capturedThisSession.delete(dedupKey);
		captureError(err, { area: 'vocab', extra: { op: 'captureLookup', kind: event.kind } });
		return null;
	}
	return event;
}

/**
 * Build the lookup event for a resolved popover. Pure — no storage, no clock
 * beyond `now`, so it is unit-testable without a DOM.
 *
 * Returns null when the content cannot be keyed (an out-of-range span, or a
 * phrase whose lemmas normalize to nothing).
 */
export function buildLookupEvent(
	input: CaptureInput,
	now: Date = new Date(),
	id: string = newEventId()
): LookupEvent | null {
	const { content, tokens, originalText, enrichment, targetLang } = input;

	const kind: LookupKind = content.kind;
	const spanStart = content.kind === 'phrase' ? content.startIndex : content.tokenIndex;
	const spanEnd = content.kind === 'phrase' ? content.endIndex : content.tokenIndex;

	const token = tokens[spanStart];
	if (!token) return null;

	const entryKey =
		content.kind === 'phrase'
			? phraseEntryKey(targetLang, tokens, content.startIndex, content.endIndex)
			: wordEntryKey(targetLang, token);
	if (entryKey === '' || entryKey.endsWith(':')) return null;

	const lemma =
		content.kind === 'phrase'
			? tokens
					.slice(content.startIndex, content.endIndex + 1)
					.map(lemmaOf)
					.join(' ')
			: lemmaOf(token);

	return {
		id,
		entry_key: entryKey,
		kind,
		article_id: input.articleId,
		article_title: input.articleTitle,
		span_start: spanStart,
		span_end: spanEnd,
		surface: content.original,
		lemma,
		translation: content.kind === 'phrase' ? content.translationOrDefinition : content.translation,
		...(content.kind === 'word' && content.cefrLevel ? { cefr_level: content.cefrLevel } : {}),
		...(content.kind === 'phrase' && content.phraseType ? { phrase_type: content.phraseType } : {}),
		context: contextFor(spanStart, tokens, originalText, enrichment),
		occurred_at: now.toISOString()
	};
}

/**
 * The covering sentence, truncated to MAX_CONTEXT_LEN on a word boundary.
 * Empty when no sentence covers the token.
 */
export function contextFor(
	tokenIndex: number,
	tokens: Token[],
	originalText: string,
	enrichment: Enrichment
): string {
	const sentence = findCoveringSentence(tokenIndex, enrichment.sentences);
	if (!sentence) return '';
	const text = sliceText(tokens, sentence.start_index, sentence.end_index, originalText).trim();
	return truncateOnWordBoundary(text, MAX_CONTEXT_LEN);
}

/**
 * Cut `text` to at most `limit` characters, preferring the last whitespace so a
 * word is not sliced in half. An ellipsis marks that something was removed.
 */
export function truncateOnWordBoundary(text: string, limit: number): string {
	if (text.length <= limit) return text;
	const head = text.slice(0, limit);
	const lastSpace = head.lastIndexOf(' ');
	// Only honour the boundary if it is not absurdly early (a single very long
	// "word" would otherwise truncate to almost nothing).
	const cut = lastSpace > limit * 0.5 ? head.slice(0, lastSpace) : head;
	return cut.trimEnd() + '…';
}

/**
 * Apply the event to the local aggregate so other occurrences of the same lemma
 * light up at once. This row mirrors SERVER state, so a brand-new entry starts
 * at `count: 0` with an empty `updated_at`: the pending outbox row supplies the
 * +1 the UI displays (§6.2), and the next pull replaces the row wholesale. That
 * is what keeps the optimistic count from drifting.
 */
async function upsertLocalEntry(event: LookupEvent): Promise<void> {
	const existing = await db.vocab_entries.get(event.entry_key);
	if (existing) {
		// Refresh only the "latest" facets; count and timestamps stay server-owned.
		await db.vocab_entries.put({
			...existing,
			latest_translation: event.translation,
			latest_cefr_level: event.cefr_level,
			latest_phrase_type: event.phrase_type,
			latest_context: event.context,
			latest_article_id: event.article_id,
			latest_article_title: event.article_title,
			deleted_at: undefined
		});
		return;
	}

	const fresh: VocabEntry = {
		entry_key: event.entry_key,
		kind: event.kind,
		lemma: event.lemma,
		target_lang: event.entry_key.split(':')[1] ?? '',
		surface_forms: [event.surface],
		count: 0,
		first_seen: event.occurred_at,
		last_seen: event.occurred_at,
		latest_translation: event.translation,
		latest_cefr_level: event.cefr_level,
		latest_phrase_type: event.phrase_type,
		latest_context: event.context,
		latest_article_id: event.article_id,
		latest_article_title: event.article_title,
		updated_at: ''
	};
	await db.vocab_entries.put(fresh);
}

/**
 * A v4 uuid for the event. Ordering comes from `occurred_at`, so randomness is
 * fine; the id only has to make a retried delivery idempotent.
 */
function newEventId(): string {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return crypto.randomUUID();
	}
	// Older WebViews without randomUUID still need a unique id.
	return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}
