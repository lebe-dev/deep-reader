<script lang="ts">
	// TokenRenderer — renders an article's token array as interactive inline spans.
	// Each word token is a <span class="token" data-index={i}>.
	// Whitespace/punctuation tokens are rendered verbatim (preserves layout).
	//
	// Interactions:
	//   Click word        → phrase (if in phrase range) or difficult-word popover.
	//   Shift-click word  → sentence sheet.
	//   Long-press (touch, ≥500ms) → sentence sheet.
	//   Text selection    → sentence sheet for covering sentence.
	//
	// Reading position: IntersectionObserver tracks furthest-seen word token;
	// calls onProgress(tokenIndex) when it advances.

	import { onMount, onDestroy } from 'svelte';
	import type { Enrichment, Token } from '$lib/types';
	import {
		buildDifficultWordMap,
		buildPhraseMap,
		findCoveringSentence,
		findCoveringSentenceForRange,
		resolveClickContent,
		sliceText,
		type PopoverContent,
		type SentenceSheetContent
	} from './reader-utils';
	import { cn } from '$lib/utils';

	interface Props {
		tokens: Token[];
		originalText: string;
		enrichment: Enrichment;
		/** Furthest-read token index (for restore-position scroll). */
		initialPosition?: number;
		/** Called with the furthest-seen token index as the user scrolls (debounce externally). */
		onProgress: (tokenIndex: number) => void;
		/** Called when word/phrase popover content changes. */
		onWordClick: (content: PopoverContent | null, anchor: HTMLElement | null) => void;
		/** Called when sentence sheet content changes. */
		onSentenceSelect: (content: SentenceSheetContent | null) => void;
	}

	let {
		tokens,
		originalText,
		enrichment,
		initialPosition = 0,
		onProgress,
		onWordClick,
		onSentenceSelect
	}: Props = $props();

	// ---------------------------------------------------------------------------
	// Derived lookup maps
	// ---------------------------------------------------------------------------

	const difficultWordMap = $derived(buildDifficultWordMap(enrichment));
	const phraseMap = $derived(buildPhraseMap(enrichment));
	const difficultSet = $derived(new Set(enrichment.difficult_words.map((d) => d.token_index)));

	// ---------------------------------------------------------------------------
	// Render segments
	// ---------------------------------------------------------------------------
	//
	// The backend tokenizer emits ONLY word tokens (no whitespace/punctuation),
	// with exact byte offsets into originalText. To render readable prose we must
	// reconstruct the gaps (spaces, punctuation, newlines) that sit between
	// consecutive word tokens from originalText itself.
	//
	// Token offsets are UTF-8 byte offsets (Go semantics), so we slice the encoded
	// byte array rather than the JS (UTF-16) string — otherwise every offset after
	// a non-ASCII character (’ — …) would be wrong.

	interface Segment {
		word: boolean;
		text: string;
		/** token index for word segments; -1 for gap segments. */
		index: number;
	}

	const segments = $derived.by<Segment[]>(() => {
		if (tokens.length === 0) return [];
		const bytes = new TextEncoder().encode(originalText);
		const decoder = new TextDecoder();
		const segs: Segment[] = [];
		let cursor = 0;
		for (const token of tokens) {
			if (token.start > cursor) {
				segs.push({
					word: false,
					text: decoder.decode(bytes.subarray(cursor, token.start)),
					index: -1
				});
			}
			segs.push({ word: true, text: token.text, index: token.index });
			cursor = token.end;
		}
		if (cursor < bytes.length) {
			segs.push({ word: false, text: decoder.decode(bytes.subarray(cursor)), index: -1 });
		}
		return segs;
	});

	// ---------------------------------------------------------------------------
	// Highlight state
	// ---------------------------------------------------------------------------

	let highlightedPhraseRange: { start: number; end: number } | null = $state(null);
	let highlightedWordIndex: number | null = $state(null);

	function clearHighlight() {
		highlightedPhraseRange = null;
		highlightedWordIndex = null;
	}

	// ---------------------------------------------------------------------------
	// DOM refs and intersection observer
	// ---------------------------------------------------------------------------

	let containerEl: HTMLDivElement | undefined = $state();
	/** token index → span element (populated by the `track` action). */
	const tokenEls = new Map<number, HTMLElement>();

	// Intentionally captures the initial prop value for scroll restoration (one-time).
	// svelte-ignore state_referenced_locally
	let furthestSeen = $state(initialPosition);
	let observer: IntersectionObserver | undefined;

	/** Svelte action: registers the span in tokenEls and observes it. */
	function track(el: HTMLElement, index: number) {
		tokenEls.set(index, el);
		observer?.observe(el);
		return {
			destroy() {
				observer?.unobserve(el);
				tokenEls.delete(index);
			}
		};
	}

	onMount(() => {
		// Restore scroll position.
		if (initialPosition > 0) {
			const el = tokenEls.get(initialPosition);
			el?.scrollIntoView({ block: 'center', behavior: 'instant' });
		}

		observer = new IntersectionObserver(
			(entries) => {
				let changed = false;
				for (const entry of entries) {
					if (!entry.isIntersecting) continue;
					const idx = Number((entry.target as HTMLElement).dataset.index);
					if (!isNaN(idx) && idx > furthestSeen) {
						furthestSeen = idx;
						changed = true;
					}
				}
				if (changed) onProgress(furthestSeen);
			},
			{ threshold: 0.5 }
		);

		// Observe already-registered elements (mounted before observer created).
		for (const [, el] of tokenEls) {
			observer.observe(el);
		}
	});

	onDestroy(() => {
		observer?.disconnect();
	});

	// ---------------------------------------------------------------------------
	// Long-press (touch devices)
	// ---------------------------------------------------------------------------

	let longPressTimer: ReturnType<typeof setTimeout> | undefined;
	const LONG_PRESS_MS = 500;

	function clearLongPress() {
		if (longPressTimer !== undefined) {
			clearTimeout(longPressTimer);
			longPressTimer = undefined;
		}
	}

	// ---------------------------------------------------------------------------
	// Sentence helpers
	// ---------------------------------------------------------------------------

	function showSentenceForToken(tokenIndex: number) {
		const sentence = findCoveringSentence(tokenIndex, enrichment.sentences);
		if (!sentence) return;
		onSentenceSelect({
			kind: 'sentence',
			original: sliceText(tokens, sentence.start_index, sentence.end_index, originalText),
			translation: sentence.translation
		});
	}

	function showSentenceForRange(startIdx: number, endIdx: number) {
		const sentence = findCoveringSentenceForRange(startIdx, endIdx, enrichment.sentences);
		if (sentence) {
			onSentenceSelect({
				kind: 'sentence',
				original: sliceText(tokens, sentence.start_index, sentence.end_index, originalText),
				translation: sentence.translation
			});
			return;
		}
		// Fallback: just start index.
		showSentenceForToken(startIdx);
	}

	// ---------------------------------------------------------------------------
	// Click handler
	// ---------------------------------------------------------------------------

	function handleWordClick(event: MouseEvent | TouchEvent, tokenIndex: number) {
		const target = event.currentTarget as HTMLElement;

		if (event instanceof MouseEvent && event.shiftKey) {
			event.preventDefault();
			clearHighlight();
			onWordClick(null, null);
			showSentenceForToken(tokenIndex);
			return;
		}

		const result = resolveClickContent(
			tokenIndex,
			tokens,
			originalText,
			difficultWordMap,
			phraseMap
		);

		if (!result) {
			clearHighlight();
			onWordClick(null, null);
			showSentenceForToken(tokenIndex);
			return;
		}

		if (result.kind === 'phrase') {
			highlightedPhraseRange = { start: result.startIndex, end: result.endIndex };
			highlightedWordIndex = null;
		} else {
			highlightedWordIndex = tokenIndex;
			highlightedPhraseRange = null;
		}

		onWordClick(result, target);
	}

	// Touch: short tap = click, long press = sentence.
	function handleTouchStart(_event: TouchEvent, tokenIndex: number) {
		clearLongPress();
		longPressTimer = setTimeout(() => {
			longPressTimer = undefined;
			clearHighlight();
			onWordClick(null, null);
			showSentenceForToken(tokenIndex);
		}, LONG_PRESS_MS);
	}

	function handleTouchEnd(event: TouchEvent, tokenIndex: number) {
		if (longPressTimer !== undefined) {
			clearLongPress();
			handleWordClick(event, tokenIndex);
		}
	}

	function handleTouchMove() {
		clearLongPress();
	}

	// Text selection → sentence sheet.
	function handleContainerMouseUp(_event: MouseEvent) {
		const selection = window.getSelection();
		if (!selection || selection.isCollapsed) return;
		if (selection.toString().trim().length < 3) return;

		const anchorEl = selection.anchorNode?.parentElement?.closest<HTMLElement>('[data-index]');
		const focusEl = selection.focusNode?.parentElement?.closest<HTMLElement>('[data-index]');
		if (!anchorEl || !focusEl) return;

		const a = Number(anchorEl.dataset.index);
		const b = Number(focusEl.dataset.index);
		if (isNaN(a) || isNaN(b)) return;

		clearHighlight();
		onWordClick(null, null);
		showSentenceForRange(Math.min(a, b), Math.max(a, b));
	}

	// ---------------------------------------------------------------------------
	// Per-token CSS classes
	// ---------------------------------------------------------------------------

	function tokenClass(index: number): string {
		const isDifficult = difficultSet.has(index);
		const isPhrase = phraseMap.has(index);
		const isHighlightedPhrase =
			highlightedPhraseRange !== null &&
			index >= highlightedPhraseRange.start &&
			index <= highlightedPhraseRange.end;
		const isHighlightedWord = highlightedWordIndex === index;

		return cn(
			'token cursor-pointer rounded-sm px-[1px] transition-colors duration-100',
			isDifficult &&
				'underline decoration-dotted decoration-amber-500/70 decoration-1 underline-offset-3 dark:decoration-amber-400/60',
			isPhrase &&
				!isDifficult &&
				'underline decoration-solid decoration-sky-400/60 decoration-1 underline-offset-3 dark:decoration-sky-500/50',
			(isHighlightedPhrase || isHighlightedWord) && 'bg-primary/15 text-primary rounded'
		);
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
	bind:this={containerEl}
	class="reader-content"
	onmouseup={handleContainerMouseUp}
	role="document"
>
	{#each segments as segment, i (i)}
		{#if segment.word}
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<span
				use:track={segment.index}
				data-index={segment.index}
				class={tokenClass(segment.index)}
				onclick={(e) => handleWordClick(e, segment.index)}
				ontouchstart={(e) => handleTouchStart(e, segment.index)}
				ontouchend={(e) => handleTouchEnd(e, segment.index)}
				ontouchmove={handleTouchMove}>{segment.text}</span
			>
		{:else}
			{segment.text}
		{/if}
	{/each}
</div>

<style>
	.reader-content {
		font-size: 1.0625rem;
		line-height: 1.8;
		font-family: var(--font-sans, sans-serif);
		overflow-wrap: break-word;
		word-break: break-word;
		/* Preserve exact whitespace/newlines from the token stream. */
		white-space: pre-wrap;
	}

	@media (max-width: 640px) {
		.reader-content {
			font-size: 1rem;
			line-height: 1.75;
		}
	}

	/* Prevent iOS callout / selection on word tokens. */
	:global(.token) {
		-webkit-touch-callout: none;
		-webkit-user-select: none;
		user-select: none;
	}
</style>
