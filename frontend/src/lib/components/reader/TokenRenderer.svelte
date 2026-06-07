<script lang="ts">
	// TokenRenderer — renders an article's token array as interactive inline spans.
	// Each word token is a <span class="token" data-index={i}>.
	// Whitespace/punctuation tokens are rendered verbatim (preserves layout).
	//
	// Interactions:
	//   Click word        → phrase (if in phrase range) or difficult-word popover.
	//   Long-press (touch, ≥500ms) → in-place sentence action menu (the only path
	//                      to a sentence translation; plain clicks/selection do not
	//                      open the sentence sheet).
	//
	// Reading position: IntersectionObserver tracks furthest-seen word token;
	// calls onProgress(tokenIndex) when it advances.

	import { onMount, onDestroy } from 'svelte';
	import type { Enrichment, Token } from '$lib/types';
	import {
		buildDifficultWordMap,
		buildPhraseMap,
		buildRenderSegments,
		findCoveringSentence,
		resolveClickContent,
		sliceText,
		type PopoverContent,
		type SentenceMenuContent
	} from './reader-utils';
	import ImageLightbox from './ImageLightbox.svelte';
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
		/** Called on long-press to open the in-place sentence action menu. */
		onSentenceMenu: (content: SentenceMenuContent | null, anchor: HTMLElement | null) => void;
	}

	let {
		tokens,
		originalText,
		enrichment,
		initialPosition = 0,
		onProgress,
		onWordClick,
		onSentenceMenu
	}: Props = $props();

	// ---------------------------------------------------------------------------
	// Derived lookup maps
	// ---------------------------------------------------------------------------

	const difficultWordMap = $derived(buildDifficultWordMap(enrichment));
	const phraseMap = $derived(buildPhraseMap(enrichment));
	const difficultSet = $derived(new Set(enrichment.difficult_words.map((d) => d.token_index)));

	// Static per-token class (difficult/phrase styling). Depends only on the
	// enrichment maps, so it's computed once per article — NOT on every click.
	// Highlight styling is appended cheaply in tokenClass(); previously every
	// click ran tailwind-merge (via cn()) across *all* tokens, which lagged.
	const baseTokenClass = $derived.by(() => {
		const map = new Map<number, string>();
		for (const token of tokens) {
			const isDifficult = difficultSet.has(token.index);
			const isPhrase = phraseMap.has(token.index);
			map.set(
				token.index,
				cn(
					'token cursor-pointer rounded-sm px-[1px] transition-colors duration-100',
					isDifficult &&
						'underline decoration-dotted decoration-amber-500/70 decoration-1 underline-offset-3 dark:decoration-amber-400/60',
					isPhrase &&
						!isDifficult &&
						'underline decoration-solid decoration-sky-400/60 decoration-1 underline-offset-3 dark:decoration-sky-500/50'
				)
			);
		}
		return map;
	});

	// ---------------------------------------------------------------------------
	// Render segments
	// ---------------------------------------------------------------------------
	//
	// Word tokens, the reconstructed gaps between them, and any markdown
	// image/link spans surviving in originalText. See buildRenderSegments.

	const segments = $derived(buildRenderSegments(tokens, originalText));

	// ---------------------------------------------------------------------------
	// Highlight state
	// ---------------------------------------------------------------------------

	let highlightedPhraseRange: { start: number; end: number } | null = $state(null);
	let highlightedWordIndex: number | null = $state(null);

	// Zoomed image shown in the full-screen lightbox (null = closed).
	let lightboxImage: { url: string; alt: string } | null = $state(null);

	function clearHighlight() {
		highlightedPhraseRange = null;
		highlightedWordIndex = null;
	}

	// ---------------------------------------------------------------------------
	// DOM refs and intersection observer
	// ---------------------------------------------------------------------------

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

		// Skip the initial IntersectionObserver batch that fires for all observed
		// elements at once. That batch reflects the viewport state on mount (not
		// actual reading), and if the article fits in the viewport it would advance
		// furthestSeen to the last token — causing every subsequent open to scroll
		// to the end of the page.
		let settling = true;

		observer = new IntersectionObserver(
			(entries) => {
				if (settling) {
					settling = false;
					return;
				}
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
	// True between a fired long-press and the trailing synthetic click the browser
	// emits on finger-up; used to swallow that click so it can't open the sentence
	// sheet on top of the just-opened action menu.
	let longPressFired = false;
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

	function showSentenceMenuForToken(tokenIndex: number, anchor: HTMLElement) {
		const sentence = findCoveringSentence(tokenIndex, enrichment.sentences);
		if (!sentence) return;
		onSentenceMenu(
			{
				kind: 'sentence-menu',
				original: sliceText(tokens, sentence.start_index, sentence.end_index, originalText),
				translation: sentence.translation
			},
			anchor
		);
	}

	// ---------------------------------------------------------------------------
	// Click handler
	// ---------------------------------------------------------------------------

	function handleWordClick(event: MouseEvent | TouchEvent, tokenIndex: number) {
		// Swallow the synthetic click that trails a long-press so it doesn't open
		// the sentence sheet over the action menu.
		if (longPressFired) {
			longPressFired = false;
			return;
		}

		const target = event.currentTarget as HTMLElement;

		const result = resolveClickContent(
			tokenIndex,
			tokens,
			originalText,
			difficultWordMap,
			phraseMap
		);

		// Plain words (no difficult-word / phrase enrichment) are inert on click —
		// sentence translation is reachable only via the long-press action menu.
		if (!result) {
			clearHighlight();
			onWordClick(null, null);
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

	// Touch: short tap = click, long press = sentence action menu.
	function handleTouchStart(event: TouchEvent, tokenIndex: number) {
		clearLongPress();
		longPressFired = false;
		// Capture the anchor now; currentTarget is null once the timer fires.
		const anchor = event.currentTarget as HTMLElement;
		longPressTimer = setTimeout(() => {
			longPressTimer = undefined;
			longPressFired = true;
			clearHighlight();
			onWordClick(null, null);
			showSentenceMenuForToken(tokenIndex, anchor);
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

	// ---------------------------------------------------------------------------
	// Per-token CSS classes
	// ---------------------------------------------------------------------------

	function tokenClass(index: number): string {
		const base = baseTokenClass.get(index) ?? 'token cursor-pointer rounded-sm px-[1px]';
		const isHighlightedPhrase =
			highlightedPhraseRange !== null &&
			index >= highlightedPhraseRange.start &&
			index <= highlightedPhraseRange.end;
		const isHighlightedWord = highlightedWordIndex === index;

		// Cheap string append in the hot path — no tailwind-merge per click.
		return isHighlightedPhrase || isHighlightedWord
			? `${base} bg-primary/15 text-primary rounded`
			: base;
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div class="reader-content" role="document">
	{#each segments as segment, i (i)}
		{#if segment.kind === 'word'}
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
		{:else if segment.kind === 'image'}
			<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_element_interactions -->
			<img
				class="reader-image"
				src={segment.url}
				alt={segment.alt}
				loading="lazy"
				onclick={() => (lightboxImage = { url: segment.url, alt: segment.alt })}
			/>
		{:else if segment.kind === 'link'}
			<a class="reader-link" href={segment.url} target="_blank" rel="noopener noreferrer"
				>{segment.text}</a
			>
		{:else}
			{segment.text}
		{/if}
	{/each}
</div>

<ImageLightbox image={lightboxImage} onclose={() => (lightboxImage = null)} />

<style>
	.reader-content {
		/* Driven by the synced Appearance settings via custom properties set on an
		   ancestor; the fallbacks reproduce the previous hard-coded defaults. */
		font-size: var(--reader-font-size, 1.0625rem);
		line-height: var(--reader-line-height, 1.8);
		font-family: var(--font-sans, sans-serif);
		overflow-wrap: break-word;
		word-break: break-word;
		/* Preserve exact whitespace/newlines from the token stream. */
		white-space: pre-wrap;
	}

	/* Prevent iOS callout / selection on word tokens. */
	:global(.token) {
		-webkit-touch-callout: none;
		-webkit-user-select: none;
		user-select: none;
	}

	/* Markdown images rendered inline in the token stream. */
	.reader-image {
		display: block;
		max-width: 100%;
		height: auto;
		margin: 1.25rem auto;
		border-radius: 0.5rem;
		cursor: zoom-in;
	}

	/* Markdown links rendered inline in the token stream. */
	.reader-link {
		color: var(--color-primary, #3b82f6);
		text-decoration: underline;
		text-decoration-thickness: 1px;
		text-underline-offset: 2px;
		cursor: pointer;
	}
</style>
