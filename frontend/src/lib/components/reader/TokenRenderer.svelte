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
	//   Right-click (desktop) → the same sentence action menu, the pointer-based
	//                      counterpart of the touch long-press.
	//
	// Keyboard: the annotated words form ONE composite widget with a roving
	// tabindex, so Tab enters the article text once instead of stopping on every
	// word in it. Within the text:
	//   ← / →             → previous / next annotated word.
	//   Enter / Space     → the same panel a click opens.
	//   Shift+F10, Menu   → the sentence action menu (the keyboard's right-click).
	//   Escape            → close whatever is open, focus stays on the word.
	// Only annotated words take part: a plain word is inert to a click too, so
	// making it a stop would promise something the reader cannot deliver.
	//
	// Reading position: IntersectionObserver tracks furthest-seen word token;
	// calls onProgress(tokenIndex) when it advances.

	import { onMount, onDestroy } from 'svelte';
	import type { Enrichment, Token } from '$lib/types';
	import {
		buildDifficultWordMap,
		buildInteractiveIndices,
		buildPhraseMap,
		buildRenderSegments,
		findCoveringSentence,
		resolveClickContent,
		sliceText,
		stepInteractiveIndex,
		WORD_POPOVER_ID,
		type PopoverContent,
		type SentenceMenuContent
	} from './reader-utils';
	import { buildMarkdownBlocks, type InlineMark, type InlineSegment } from './markdown-blocks';
	import { buildOverlayIndices, emptyVocabIndex, type VocabIndex } from '$lib/vocab/overlay';
	import ImageLightbox from './ImageLightbox.svelte';
	import { cn } from '$lib/utils';

	interface Props {
		tokens: Token[];
		originalText: string;
		enrichment: Enrichment;
		/** Article content format. 'markdown' renders Markdown structure (headings,
		 *  lists, blockquotes, emphasis, code, tables) while keeping words
		 *  interactive; anything else renders the token stream as prose. */
		format?: string;
		/** Furthest-read token index (for restore-position scroll). */
		initialPosition?: number;
		/** Called with the furthest-seen token index as the user scrolls (debounce externally). */
		onProgress: (tokenIndex: number) => void;
		/**
		 * The user's collected vocabulary, as a prebuilt matcher index. Words in
		 * it are hinted from the local dictionary in place of the annotations the
		 * LLM no longer produces (WORD-CACHE-ARCH.md §10). Defaults to an empty
		 * index, which renders exactly as before this feature existed.
		 */
		vocabIndex?: VocabIndex;
		/** Called when word/phrase popover content changes. */
		onWordClick: (content: PopoverContent | null, anchor: HTMLElement | null) => void;
		/** Called on long-press to open the in-place sentence action menu. */
		onSentenceMenu: (content: SentenceMenuContent | null, anchor: HTMLElement | null) => void;
	}

	let {
		tokens,
		originalText,
		enrichment,
		format,
		initialPosition = 0,
		vocabIndex = emptyVocabIndex(),
		onProgress,
		onWordClick,
		onSentenceMenu
	}: Props = $props();

	// ---------------------------------------------------------------------------
	// Derived lookup maps
	// ---------------------------------------------------------------------------

	const difficultWordMap = $derived(buildDifficultWordMap(enrichment));
	const phraseMap = $derived(buildPhraseMap(enrichment));
	// The difficult-word token indices — the keys of difficultWordMap, reused here
	// rather than re-reading enrichment.difficult_words (which may be null).
	const difficultSet = $derived(new Set(difficultWordMap.keys()));

	// Tokens the vocabulary overlay decorates: known words and phrases the
	// enrichment did NOT annotate. Built once per (article, vocabulary) pair,
	// never per click.
	const vocabSet = $derived(
		buildOverlayIndices(vocabIndex, tokens, (i) => difficultSet.has(i) || phraseMap.has(i))
	);

	// Every token that answers to activation — the set resolveClickContent will
	// return something for. These are the ones exposed as buttons; the rest stay
	// plain text for pointer and assistive tech alike.
	const clickableSet = $derived.by(() => {
		const set = new Set<number>();
		for (const token of tokens) {
			const i = token.index;
			if (difficultSet.has(i) || phraseMap.has(i) || vocabSet.has(i)) set.add(i);
		}
		return set;
	});

	// Static per-token class (difficult/phrase styling). Depends only on the
	// enrichment maps, so it's computed once per article — NOT on every click.
	// Highlight styling is appended cheaply in tokenClass(); previously every
	// click ran tailwind-merge (via cn()) across *all* tokens, which lagged.
	const baseTokenClass = $derived.by(() => {
		const map = new Map<number, string>();
		for (const token of tokens) {
			const isDifficult = difficultSet.has(token.index);
			const isPhrase = phraseMap.has(token.index);
			const isVocab = vocabSet.has(token.index);
			map.set(
				token.index,
				cn(
					'token rounded-sm px-[1px] transition-colors duration-100',
					// Quiet, theme-aware markers: a thin underline in a muted shade of the
					// text colour rather than a bright accent, so half-underlined text
					// still reads as prose. Dotted = difficult word, solid = phrase.
					isDifficult &&
						'underline decoration-dotted decoration-foreground/30 decoration-1 underline-offset-4',
					isPhrase &&
						!isDifficult &&
						'underline decoration-solid decoration-foreground/25 decoration-1 underline-offset-4',
					// The overlay marks what the LLM no longer marks, plus every
					// inflected repeat, so density rises. One step quieter than a
					// difficult word is what keeps a page of collected vocabulary
					// from reading as a page of underlines (§10.3).
					isVocab &&
						!isDifficult &&
						!isPhrase &&
						'underline decoration-dotted decoration-foreground/20 decoration-1 underline-offset-4'
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

	// Markdown rendering: when the article is Markdown, parse it into structural
	// blocks (headings, lists, blockquotes, code, tables) whose inline runs keep
	// the same interactive word tokens. Plain articles use the flat `segments`.
	const isMarkdown = $derived(format === 'markdown');
	const blocks = $derived(isMarkdown ? buildMarkdownBlocks(tokens, originalText) : []);

	/** Map inline emphasis marks to the CSS classes that render them. */
	function marksClass(marks: InlineMark[]): string {
		if (marks.length === 0) return '';
		let c = '';
		if (marks.includes('strong')) c += ' font-semibold';
		if (marks.includes('em')) c += ' italic';
		if (marks.includes('strike')) c += ' line-through';
		if (marks.includes('code')) c += ' reader-inline-code';
		return c;
	}

	// ---------------------------------------------------------------------------
	// Highlight state
	// ---------------------------------------------------------------------------

	let highlightedPhraseRange: { start: number; end: number } | null = $state(null);
	let highlightedWordIndex: number | null = $state(null);

	// The token the open popover describes, so it can point at the panel with
	// aria-describedby: a screen reader then reads the translation as part of the
	// word instead of leaving it to an aria-live announcement the user may have
	// already scrolled past.
	let describedIndex: number | null = $state(null);

	// Zoomed image shown in the full-screen lightbox (null = closed).
	let lightboxImage: { url: string; alt: string } | null = $state(null);

	function clearHighlight() {
		highlightedPhraseRange = null;
		highlightedWordIndex = null;
		describedIndex = null;
	}

	// ---------------------------------------------------------------------------
	// Keyboard navigation (roving tabindex)
	// ---------------------------------------------------------------------------

	/** The annotated words, in reading order, one stop per phrase. */
	const interactiveIndices = $derived(
		buildInteractiveIndices(tokens, difficultSet, phraseMap, vocabSet)
	);

	/** The word that currently owns the article's single tab stop. */
	let activeIndex: number | null = $state(null);

	// Before anything has been focused, the tab stop sits at the first annotated
	// word at or after the restored reading position — tabbing into a
	// half-finished article should not throw the reader back to its first page.
	const rovingIndex = $derived(
		activeIndex ??
			stepInteractiveIndex(interactiveIndices, initialPosition > 0 ? initialPosition - 1 : null, 1)
	);

	function moveFocus(from: number, direction: 1 | -1) {
		const next = stepInteractiveIndex(interactiveIndices, from, direction);
		if (next === null || next === from) return;
		activeIndex = next;
		tokenEls.get(next)?.focus();
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

	function showSentenceMenuForToken(tokenIndex: number, anchor: HTMLElement, viaKeyboard = false) {
		const sentence = findCoveringSentence(tokenIndex, enrichment.sentences);
		if (!sentence) return;
		onSentenceMenu(
			{
				kind: 'sentence-menu',
				original: sliceText(tokens, sentence.start_index, sentence.end_index, originalText),
				translation: sentence.translation,
				viaKeyboard
			},
			anchor
		);
	}

	// ---------------------------------------------------------------------------
	// Click handler
	// ---------------------------------------------------------------------------

	// Open the word/phrase panel for a token. Shared by pointer and keyboard
	// activation so the two can never drift apart.
	function openWordPanel(anchor: HTMLElement, tokenIndex: number) {
		const result = resolveClickContent(
			tokenIndex,
			tokens,
			originalText,
			difficultWordMap,
			phraseMap,
			vocabIndex
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
		describedIndex = tokenIndex;

		onWordClick(result, anchor);
	}

	function handleWordClick(event: MouseEvent | TouchEvent, tokenIndex: number) {
		// Swallow the synthetic click that trails a long-press so it doesn't open
		// the sentence sheet over the action menu.
		if (longPressFired) {
			longPressFired = false;
			return;
		}

		// Keep the keyboard's tab stop where the pointer last worked, so switching
		// between mouse and keyboard resumes in the same place.
		activeIndex = tokenIndex;
		openWordPanel(event.currentTarget as HTMLElement, tokenIndex);
	}

	function handleTokenKeydown(event: KeyboardEvent, tokenIndex: number) {
		const anchor = event.currentTarget as HTMLElement;

		switch (event.key) {
			case 'Enter':
			case ' ':
				// Space would otherwise page down out from under the word.
				event.preventDefault();
				openWordPanel(anchor, tokenIndex);
				return;
			case 'ArrowRight':
				event.preventDefault();
				moveFocus(tokenIndex, 1);
				return;
			case 'ArrowLeft':
				event.preventDefault();
				moveFocus(tokenIndex, -1);
				return;
			// Up/Down are deliberately left alone: they scroll the article, which is
			// what a reader parked on a word expects them to do.
			case 'Escape':
				clearHighlight();
				onWordClick(null, null);
				onSentenceMenu(null, null);
				return;
			case 'ContextMenu':
				event.preventDefault();
				showSentenceMenuForToken(tokenIndex, anchor, true);
				return;
			case 'F10':
				if (!event.shiftKey) return;
				event.preventDefault();
				showSentenceMenuForToken(tokenIndex, anchor, true);
				return;
		}
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
	// Right-click (desktop) — pointer counterpart of the touch long-press.
	// ---------------------------------------------------------------------------

	function handleContextMenu(event: MouseEvent, tokenIndex: number) {
		// Suppress the native context menu and open our sentence action menu,
		// anchored to the right-clicked token (mirrors long-press positioning).
		event.preventDefault();
		const anchor = event.currentTarget as HTMLElement;
		clearHighlight();
		onWordClick(null, null);
		showSentenceMenuForToken(tokenIndex, anchor);
	}

	// ---------------------------------------------------------------------------
	// Per-token CSS classes
	// ---------------------------------------------------------------------------

	function tokenClass(index: number): string {
		const base = baseTokenClass.get(index) ?? 'token rounded-sm px-[1px]';
		const isHighlightedPhrase =
			highlightedPhraseRange !== null &&
			index >= highlightedPhraseRange.start &&
			index <= highlightedPhraseRange.end;
		const isHighlightedWord = highlightedWordIndex === index;

		// Cheap string append in the hot path — no tailwind-merge per click.
		// token-active carries the accent as ink; see the note on --reader-accent.
		return isHighlightedPhrase || isHighlightedWord ? `${base} token-active rounded` : base;
	}
</script>

<!-- Interactive word token — shared by the plain and Markdown render paths.
	 An annotated word is exposed as a button (it opens a panel, and that is what
	 the underline promises); a plain word stays plain text, because it does
	 nothing when activated. -->
{#snippet wordSpan(index: number, text: string, extra: string)}
	{@const clickable = clickableSet.has(index)}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
	<!-- a11y_no_noninteractive_tabindex: role and tabindex are set from the SAME
		 `clickable` flag, so a token is never focusable without also being a
		 button — the compiler just cannot see that through the expression. -->
	<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
	<span
		use:track={index}
		data-index={index}
		class="{tokenClass(index)}{extra}"
		role={clickable ? 'button' : undefined}
		tabindex={clickable ? (index === rovingIndex ? 0 : -1) : undefined}
		aria-describedby={index === describedIndex ? WORD_POPOVER_ID : undefined}
		onclick={(e) => handleWordClick(e, index)}
		onkeydown={clickable ? (e) => handleTokenKeydown(e, index) : undefined}
		onfocus={clickable ? () => (activeIndex = index) : undefined}
		oncontextmenu={(e) => handleContextMenu(e, index)}
		ontouchstart={(e) => handleTouchStart(e, index)}
		ontouchend={(e) => handleTouchEnd(e, index)}
		ontouchmove={handleTouchMove}>{text}</span
	>
{/snippet}

<!-- An article image, wrapped in a real button rather than given role="button":
	 the wrapper is what makes zooming reachable by keyboard and by every screen
	 reader, and the title names the control when the image has no alt text. -->
{#snippet articleImage(url: string, alt: string)}
	<button
		type="button"
		class="reader-image-button"
		title="Open image full size"
		onclick={() => (lightboxImage = { url, alt })}
	>
		<img class="reader-image" src={url} {alt} loading="lazy" />
	</button>
{/snippet}

<!-- A run of Markdown inline segments (words keep their token interactivity). -->
{#snippet inline(segs: InlineSegment[])}
	{#each segs as seg, i (i)}
		{#if seg.kind === 'word'}
			{@render wordSpan(seg.index, seg.text, marksClass(seg.marks))}
		{:else if seg.kind === 'image'}
			{@render articleImage(seg.url, seg.alt)}
		{:else if seg.kind === 'link'}
			<a
				class="reader-link{marksClass(seg.marks)}"
				href={seg.url}
				target="_blank"
				rel="noopener noreferrer">{seg.text}</a
			>
		{:else if seg.marks.length > 0}
			<span class={marksClass(seg.marks).trim()}>{seg.text}</span>
		{:else}
			{seg.text}
		{/if}
	{/each}
{/snippet}

{#if isMarkdown}
	<!-- No role="document" here: that role only means anything inside a
		 role="application" subtree, and on a plain container some screen readers
		 announce a nesting the reader does not have. -->
	<div class="reader-content reader-markdown">
		{#each blocks as block, bi (bi)}
			{#if block.kind === 'heading'}
				<svelte:element this={`h${block.level}`} class="reader-heading"
					>{@render inline(block.inline)}</svelte:element
				>
			{:else if block.kind === 'paragraph'}
				<p>{@render inline(block.inline)}</p>
			{:else if block.kind === 'blockquote'}
				<blockquote class="reader-blockquote">{@render inline(block.inline)}</blockquote>
			{:else if block.kind === 'list'}
				{#if block.ordered}
					<ol class="reader-list reader-list-ordered">
						{#each block.items as item, ii (ii)}<li>{@render inline(item)}</li>{/each}
					</ol>
				{:else}
					<ul class="reader-list reader-list-unordered">
						{#each block.items as item, ii (ii)}<li>{@render inline(item)}</li>{/each}
					</ul>
				{/if}
			{:else if block.kind === 'code'}
				<pre class="reader-pre"><code>{block.text}</code></pre>
			{:else if block.kind === 'table'}
				<div class="reader-table-wrap">
					<table class="reader-table">
						<thead>
							<tr
								>{#each block.header as cell, ci (ci)}<th>{@render inline(cell)}</th>{/each}</tr
							>
						</thead>
						<tbody>
							{#each block.rows as row, ri (ri)}
								<tr
									>{#each row as cell, ci (ci)}<td>{@render inline(cell)}</td>{/each}</tr
								>
							{/each}
						</tbody>
					</table>
				</div>
			{:else if block.kind === 'hr'}
				<hr class="reader-hr" />
			{/if}
		{/each}
	</div>
{:else}
	<div class="reader-content">
		{#each segments as segment, i (i)}
			{#if segment.kind === 'word'}
				{@render wordSpan(segment.index, segment.text, '')}
			{:else if segment.kind === 'image'}
				{@render articleImage(segment.url, segment.alt)}
			{:else if segment.kind === 'link'}
				<a class="reader-link" href={segment.url} target="_blank" rel="noopener noreferrer"
					>{segment.text}</a
				>
			{:else}
				{segment.text}
			{/if}
		{/each}
	</div>
{/if}

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

	/* The word whose panel is open. --reader-accent rather than --primary: the
	   latter is tuned as a button fill and only reaches 3.4:1 as text on the dark
	   background, under the 4.5:1 body text needs. */
	:global(.token-active) {
		background-color: color-mix(in oklab, var(--reader-accent) 15%, transparent);
		color: var(--reader-accent);
	}

	/* Keyboard focus must be unmistakable inside a wall of text — an underline
	   shift would read as just another enrichment marker. Mouse clicks do not
	   trigger :focus-visible, so this never fires for pointer users. */
	:global(.token:focus-visible) {
		outline: 2px solid var(--reader-accent);
		outline-offset: 2px;
		border-radius: 3px;
	}

	@media (pointer: coarse) {
		:global(.token) {
			cursor: pointer;
		}
	}

	/* Markdown images rendered inline in the token stream. */
	.reader-image-button {
		display: block;
		width: 100%;
		padding: 0;
		border: 0;
		background: none;
		cursor: zoom-in;
	}

	.reader-image-button:focus-visible {
		outline: 2px solid var(--reader-accent);
		outline-offset: 3px;
		border-radius: 0.6rem;
	}

	.reader-image {
		display: block;
		max-width: 100%;
		height: auto;
		margin: 1.25rem auto;
		border-radius: 0.5rem;
	}

	/* Markdown links rendered inline in the token stream. */
	.reader-link {
		color: var(--reader-accent, #3b82f6);
		text-decoration: underline;
		text-decoration-thickness: 1px;
		text-underline-offset: 2px;
		cursor: pointer;
	}

	/* ── Markdown render mode ──────────────────────────────────────────────
	   When the article is Markdown the document is laid out as real block
	   elements, so the pre-wrap whitespace handling of the plain token stream is
	   replaced by normal block flow. Word tokens inside keep their interactivity
	   and styling (the .token rules above still apply). */
	.reader-markdown {
		white-space: normal;
	}

	.reader-heading {
		font-weight: 650;
		line-height: 1.3;
		margin: 1.6em 0 0.6em;
	}
	.reader-heading:first-child {
		margin-top: 0;
	}
	:global(h1.reader-heading) {
		font-size: 1.6em;
	}
	:global(h2.reader-heading) {
		font-size: 1.35em;
	}
	:global(h3.reader-heading) {
		font-size: 1.18em;
	}
	:global(h4.reader-heading),
	:global(h5.reader-heading),
	:global(h6.reader-heading) {
		font-size: 1.05em;
	}

	.reader-markdown p {
		margin: 0 0 1em;
	}

	.reader-list {
		margin: 0 0 1em;
		padding-left: 1.6em;
	}
	.reader-list-unordered {
		list-style: disc;
	}
	.reader-list-ordered {
		list-style: decimal;
	}
	.reader-list li {
		margin: 0.2em 0;
	}

	.reader-blockquote {
		margin: 0 0 1em;
		padding: 0.2em 0 0.2em 1em;
		border-left: 3px solid var(--color-border, #d4d4d8);
		color: var(--color-muted-foreground, #6b7280);
		font-style: italic;
	}

	.reader-pre {
		margin: 0 0 1em;
		padding: 0.9em 1em;
		overflow-x: auto;
		border-radius: 0.5rem;
		background: var(--color-muted, #f4f4f5);
		font-size: 0.9em;
		line-height: 1.5;
		white-space: pre;
	}
	.reader-pre code {
		font-family:
			ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;
	}

	/* Inline emphasis marks applied to interactive word tokens. */
	:global(.reader-inline-code) {
		font-family:
			ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, 'Liberation Mono', monospace;
		font-size: 0.9em;
		padding: 0.1em 0.3em;
		border-radius: 0.25rem;
		background: var(--color-muted, #f4f4f5);
	}

	.reader-table-wrap {
		margin: 0 0 1em;
		overflow-x: auto;
	}
	.reader-table {
		border-collapse: collapse;
		width: 100%;
		font-size: 0.95em;
	}
	.reader-table th,
	.reader-table td {
		border: 1px solid var(--color-border, #d4d4d8);
		padding: 0.4em 0.7em;
		text-align: left;
		vertical-align: top;
	}
	.reader-table th {
		font-weight: 650;
		background: var(--color-muted, #f4f4f5);
	}

	.reader-hr {
		margin: 1.6em 0;
		border: 0;
		border-top: 1px solid var(--color-border, #d4d4d8);
	}
</style>
