<script lang="ts">
	// SentenceMenu — small in-place action menu shown on a long-press over a
	// sentence (touch) or a right-click (desktop). Anchored just below the
	// pressed/clicked token, mirroring WordPopover's positioning. Offers two
	// actions:
	//   Copy sentence  → copies the sentence text to the clipboard.
	//   Translate      → opens the sentence sheet (only when a translation exists).
	//
	// Positioning logic intentionally duplicates WordPopover rather than sharing
	// a helper: the two panels have different sizes and the math is trivial.

	import { cn } from '$lib/utils';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import LanguagesIcon from '@lucide/svelte/icons/languages';
	import type { SentenceMenuContent } from './reader-utils';

	interface Props {
		content: SentenceMenuContent | null;
		anchorEl: HTMLElement | null;
		oncopy: (text: string) => void;
		ontranslate: (content: SentenceMenuContent) => void;
		onclose: () => void;
	}

	let { content, anchorEl, oncopy, ontranslate, onclose }: Props = $props();

	const hasTranslation = $derived((content?.translation.trim().length ?? 0) > 0);

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

	function handleCopy() {
		if (content) oncopy(content.original);
	}

	function handleTranslate() {
		if (content) ontranslate(content);
	}
</script>

{#if content && anchorEl}
	<div
		class={cn(
			'bg-popover text-popover-foreground ring-foreground/10 fixed z-50 flex w-56 flex-col gap-0.5 rounded-md p-1 shadow-md ring-1',
			'animate-in fade-in-0 zoom-in-95 duration-100'
		)}
		{style}
		role="menu"
		aria-label="Sentence actions"
	>
		<button
			type="button"
			role="menuitem"
			class="hover:bg-accent flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors"
			onclick={handleCopy}
		>
			<CopyIcon class="size-4 shrink-0" />
			<span>Copy sentence</span>
		</button>

		{#if hasTranslation}
			<button
				type="button"
				role="menuitem"
				class="hover:bg-accent flex w-full items-center gap-2 rounded-sm px-3 py-2 text-left text-sm transition-colors"
				onclick={handleTranslate}
			>
				<LanguagesIcon class="size-4 shrink-0" />
				<span>Translate sentence</span>
			</button>
		{/if}
	</div>
{/if}
