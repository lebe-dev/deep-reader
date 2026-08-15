<script lang="ts">
	// SentenceMenu — small in-place action menu shown on a long-press over a word
	// (touch) or a right-click (desktop). Anchored just below the pressed/clicked
	// token, mirroring WordPopover's positioning. Offers:
	//   Save "<word>"  → adds the word to the vocabulary (hidden once it is there).
	//   Save phrase…   → starts picking the phrase's other end.
	//   Copy sentence  → copies the sentence text to the clipboard.
	//   Translate      → opens the sentence sheet (only when a translation exists).
	//
	// The two save items are why the menu opens on plain, unannotated words at
	// all — those are precisely the words the reader cannot otherwise collect
	// (WORD-CACHE-ARCH.md §18). The sentence items drop out when no sentence
	// covers the token, which can leave the menu holding only the save actions.
	//
	// Positioning logic intentionally duplicates WordPopover rather than sharing
	// a helper: the two panels have different sizes and the math is trivial.

	import { cn } from '$lib/utils';
	import BookmarkPlusIcon from '@lucide/svelte/icons/bookmark-plus';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import LanguagesIcon from '@lucide/svelte/icons/languages';
	import HighlighterIcon from '@lucide/svelte/icons/highlighter';
	import type { SentenceMenuContent } from './reader-utils';

	interface Props {
		content: SentenceMenuContent | null;
		anchorEl: HTMLElement | null;
		oncopy: (text: string) => void;
		ontranslate: (content: SentenceMenuContent) => void;
		/** Save the pressed word to the vocabulary. */
		onsaveword: (content: SentenceMenuContent) => void;
		/** Start picking a phrase that begins at the pressed word. */
		onsavephrase: (content: SentenceMenuContent) => void;
		onclose: () => void;
	}

	let { content, anchorEl, oncopy, ontranslate, onsaveword, onsavephrase, onclose }: Props =
		$props();

	const hasSentence = $derived((content?.original.trim().length ?? 0) > 0);
	const hasTranslation = $derived((content?.translation.trim().length ?? 0) > 0);
	/** Already collected words offer nothing to save, so the item is hidden. */
	const canSaveWord = $derived(content !== null && !content.alreadySaved);

	/** Set when the menu was opened from the keyboard — see SentenceMenuContent. */
	const viaKeyboard = $derived(content?.viaKeyboard === true);

	let menuEl = $state<HTMLElement | null>(null);

	function menuItems(): HTMLButtonElement[] {
		return Array.from(menuEl?.querySelectorAll('button') ?? []);
	}

	// Bumped on scroll/resize so the panel stays glued to its token. See WordPopover.
	let reflowTick = $state(0);

	$effect(() => {
		if (!content || !anchorEl) return;
		function reflow() {
			reflowTick++;
		}
		window.addEventListener('scroll', reflow, true);
		window.addEventListener('resize', reflow);
		return () => {
			window.removeEventListener('scroll', reflow, true);
			window.removeEventListener('resize', reflow);
		};
	});

	let rect = $derived.by(() => {
		reflowTick; // track for recomputation on scroll/resize
		if (!anchorEl || !content) return null;
		return anchorEl.getBoundingClientRect();
	});

	// Position the panel just below the token (above when near the bottom edge).
	let style = $derived.by(() => {
		if (!rect) return '';
		const vw = window.innerWidth;
		const vh = window.innerHeight;
		const panelW = Math.min(224, vw - 24); // w-56 = 224px, respect viewport

		let left = rect.left;
		if (left + panelW > vw - 8) left = vw - panelW - 8;
		if (left < 8) left = 8;

		const spaceBelow = vh - rect.bottom;
		const isAbove = spaceBelow < 120;

		return isAbove
			? `left:${left}px;top:${rect.top - 6}px;transform:translateY(-100%)`
			: `left:${left}px;top:${rect.bottom + 6}px`;
	});

	// Close when interacting outside the menu. A token tap re-targets, so let the
	// renderer handle those (matching WordPopover).
	$effect(() => {
		if (!content || !anchorEl) return;
		function onPointerDown(e: PointerEvent) {
			const t = e.target as HTMLElement | null;
			if (t?.closest('.token') || t?.closest('[role="menu"]')) return;
			onclose();
		}
		window.addEventListener('pointerdown', onPointerDown, true);
		return () => window.removeEventListener('pointerdown', onPointerDown, true);
	});

	// Keyboard menu behaviour, per the WAI-ARIA menu pattern: focus moves into the
	// menu on open, arrows walk it, Escape closes it and hands focus back to the
	// word it was opened from — a keyboard user who is dropped back at the top of
	// the document has effectively lost their place in the article.
	$effect(() => {
		if (!content || !anchorEl) return;
		const anchor = anchorEl;
		const takesFocus = viaKeyboard;

		if (takesFocus) queueMicrotask(() => menuItems()[0]?.focus());

		function onKeydown(e: KeyboardEvent) {
			if (e.key === 'Escape') {
				e.stopPropagation();
				onclose();
				return;
			}
			if (!takesFocus) return;
			if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
			const items = menuItems();
			if (items.length === 0) return;
			e.preventDefault();
			const at = items.indexOf(document.activeElement as HTMLButtonElement);
			const step = e.key === 'ArrowDown' ? 1 : -1;
			const next = (at + step + items.length) % items.length;
			items[next].focus();
		}

		window.addEventListener('keydown', onKeydown);
		return () => {
			window.removeEventListener('keydown', onKeydown);
			// Only when we took focus in the first place: a long-press must not make
			// the page jump back to the pressed word.
			if (takesFocus) anchor.focus();
		};
	});

	function handleCopy() {
		if (content) oncopy(content.original);
	}

	function handleTranslate() {
		if (content) ontranslate(content);
	}

	function handleSaveWord() {
		if (content) onsaveword(content);
	}

	function handleSavePhrase() {
		if (content) onsavephrase(content);
	}
</script>

{#if content && anchorEl}
	<div
		class={cn(
			'bg-popover text-popover-foreground ring-foreground/10 fixed z-50 flex w-56 flex-col gap-0.5 rounded-md p-1 shadow-md ring-1',
			'animate-in fade-in-0 zoom-in-95 duration-100'
		)}
		{style}
		bind:this={menuEl}
		role="menu"
		aria-label="Word actions"
	>
		{#if canSaveWord}
			<button
				type="button"
				role="menuitem"
				class="hover:bg-accent focus-visible:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none"
				onclick={handleSaveWord}
			>
				<BookmarkPlusIcon class="size-4 shrink-0" />
				<span class="truncate">Save “{content.word}”</span>
			</button>
		{/if}

		<button
			type="button"
			role="menuitem"
			class="hover:bg-accent focus-visible:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none"
			onclick={handleSavePhrase}
		>
			<HighlighterIcon class="size-4 shrink-0" />
			<span>Save phrase…</span>
		</button>

		{#if hasSentence}
			<button
				type="button"
				role="menuitem"
				class="hover:bg-accent focus-visible:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none"
				onclick={handleCopy}
			>
				<CopyIcon class="size-4 shrink-0" />
				<span>Copy sentence</span>
			</button>
		{/if}

		{#if hasTranslation}
			<button
				type="button"
				role="menuitem"
				class="hover:bg-accent focus-visible:bg-accent focus-visible:ring-ring flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors focus-visible:ring-2 focus-visible:outline-none"
				onclick={handleTranslate}
			>
				<LanguagesIcon class="size-4 shrink-0" />
				<span>Translate sentence</span>
			</button>
		{/if}
	</div>
{/if}
