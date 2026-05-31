<script lang="ts">
	// WordPopover — displays word or phrase translation in a floating card
	// anchored just below the clicked token element.
	//
	// We implement a custom floating panel rather than using bits-ui Popover
	// because the bits-ui Popover requires its Trigger to be the reference
	// element, which conflicts with our "click any span" interaction model.

	import { cn } from '$lib/utils';
	import type { PopoverContent } from './reader-utils';

	interface Props {
		content: PopoverContent | null;
		anchorEl: HTMLElement | null;
		onclose: () => void;
	}

	let { content, anchorEl, onclose }: Props = $props();

	// Recompute position whenever anchor or content changes.
	let rect = $derived.by(() => {
		if (!anchorEl || !content) return null;
		return anchorEl.getBoundingClientRect();
	});

	// Position the panel just below (or above if close to viewport bottom).
	let style = $derived.by(() => {
		if (!rect) return '';
		const vw = window.innerWidth;
		const vh = window.innerHeight;
		const panelW = Math.min(288, vw - 24); // w-72 = 288px, respect viewport
		const scrollY = window.scrollY;

		// Horizontal: align left edge with token, clamp to viewport.
		let left = rect.left;
		if (left + panelW > vw - 8) left = vw - panelW - 8;
		if (left < 8) left = 8;

		// Vertical: below token by default; above if insufficient space.
		const spaceBelow = vh - rect.bottom;
		const topBelow = rect.bottom + scrollY + 6;
		const topAbove = rect.top + scrollY - 6; // we'll translate up in CSS

		const isAbove = spaceBelow < 160;

		return isAbove
			? `left:${left}px;top:${topAbove}px;transform:translateY(-100%)`
			: `left:${left}px;top:${topBelow}px`;
	});

	const phraseTypeLabel: Record<string, string> = {
		idiom: 'Idiom',
		phrasal_verb: 'Phrasal verb',
		term: 'Term'
	};

	// Close when clicking outside.
	function handleBackdropClick(e: MouseEvent) {
		e.stopPropagation();
		onclose();
	}
</script>

{#if content && anchorEl}
	<!-- Transparent full-screen backdrop to catch outside clicks -->
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="fixed inset-0 z-40" onclick={handleBackdropClick} aria-hidden="true"></div>

	<!-- Floating panel -->
	<div
		class={cn(
			'bg-popover text-popover-foreground ring-foreground/10 fixed z-50 w-72 rounded-md p-4 text-sm shadow-md ring-1',
			'animate-in fade-in-0 zoom-in-95 duration-100'
		)}
		{style}
		role="tooltip"
		aria-live="polite"
	>
		{#if content.kind === 'phrase'}
			<div class="flex flex-col gap-2">
				<span
					class={cn(
						'self-start rounded px-1.5 py-0.5 text-xs font-medium',
						content.phraseType === 'idiom'
							? 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300'
							: content.phraseType === 'phrasal_verb'
								? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300'
								: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
					)}
				>
					{phraseTypeLabel[content.phraseType] ?? content.phraseType}
				</span>
				<p class="font-semibold leading-snug">{content.original}</p>
				<p class="text-muted-foreground leading-relaxed">
					{content.translationOrDefinition}
				</p>
			</div>
		{:else}
			<div class="flex flex-col gap-2">
				<div class="flex items-center justify-between gap-2">
					<p class="font-semibold leading-snug">{content.original}</p>
					{#if content.cefrLevel}
						<span
							class="bg-secondary text-secondary-foreground rounded px-1.5 py-0.5 text-[10px] font-medium tabular-nums"
						>
							{content.cefrLevel}
						</span>
					{/if}
				</div>
				{#if content.lemma && content.lemma !== content.original}
					<p class="text-muted-foreground text-xs italic">{content.lemma}</p>
				{/if}
				<p class="leading-relaxed">{content.translation}</p>
			</div>
		{/if}
	</div>
{/if}
