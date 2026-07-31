<script lang="ts">
	// One row of the /words list: a compact collapsed line that expands in place
	// (WORD-CACHE-ARCH.md §13.1).
	//
	// Accessibility: the row itself is a <button> inside an <li> and the kebab is
	// its SIBLING, not a child — nesting one interactive element inside another
	// is invalid and breaks keyboard navigation.

	import { cn } from '$lib/utils';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';
	import MoreVerticalIcon from '@lucide/svelte/icons/more-vertical';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import type { VocabEntry } from '$lib/types';
	import { displayableSurfaceForms, highlightSegments } from './words-utils';

	interface Props {
		entry: VocabEntry;
		/** Displayed count: server count plus un-flushed taps. */
		count: number;
		/** True when the count includes taps still sitting in the outbox. */
		pending: boolean;
		/** Current search query, for match highlighting. */
		query?: string;
		/** True when the source article still exists locally. */
		articleAvailable?: boolean;
		expanded: boolean;
		onToggle: () => void;
		onDelete: () => void;
	}

	let {
		entry,
		count,
		pending,
		query = '',
		articleAvailable = false,
		expanded,
		onToggle,
		onDelete
	}: Props = $props();

	const rowId = $derived(`word-row-${encodeURIComponent(entry.entry_key)}`);
	const forms = $derived(displayableSurfaceForms(entry));

	const phraseTypeLabel: Record<string, string> = {
		idiom: 'Idiom',
		phrasal_verb: 'Phrasal verb',
		term: 'Term'
	};

	/** CEFR level for a word, phrase type for a phrase — the §13.1 badge. */
	const badge = $derived(
		entry.kind === 'phrase'
			? (phraseTypeLabel[entry.latest_phrase_type ?? ''] ?? 'Phrase')
			: (entry.latest_cefr_level ?? '')
	);

	const capturedAt = $derived(formatDate(entry.last_seen));

	function formatDate(iso: string): string {
		if (!iso) return '';
		const date = new Date(iso);
		if (Number.isNaN(date.getTime())) return '';
		return date.toLocaleDateString('en-GB', { day: 'numeric', month: 'long' });
	}

	async function copy(text: string, label: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast(label);
		} catch {
			toast.error('Failed to copy.');
		}
	}
</script>

<li class="border-border/60 flex flex-col border-b last:border-b-0">
	<div class="flex items-center gap-1 pr-1">
		<button
			type="button"
			id={rowId}
			class={cn(
				'flex min-h-11 flex-1 items-center gap-3 px-3 py-2 text-left',
				'hover:bg-accent/50 focus-visible:ring-ring rounded-sm focus-visible:ring-2 focus-visible:outline-none'
			)}
			aria-expanded={expanded}
			aria-controls="{rowId}-details"
			onclick={onToggle}
		>
			<ChevronDownIcon
				class={cn(
					'text-muted-foreground size-3.5 shrink-0 transition-transform',
					expanded ? 'rotate-0' : '-rotate-90'
				)}
				aria-hidden="true"
			/>

			<!-- Below ~380px the translation wraps under the lemma; the badge and
				 count stay on the first line, right-aligned. -->
			<span class="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5">
				<span class="truncate font-medium">
					{#each highlightSegments(entry.lemma, query) as seg, i (i)}<span
							class={seg.match ? 'bg-primary/20 rounded-[2px]' : ''}>{seg.text}</span
						>{/each}
				</span>
				<span class="text-muted-foreground min-w-0 flex-1 truncate text-sm">
					{#each highlightSegments(entry.latest_translation, query) as seg, i (i)}<span
							class={seg.match ? 'bg-primary/20 rounded-[2px]' : ''}>{seg.text}</span
						>{/each}
				</span>
			</span>

			{#if badge}
				<span
					class="bg-secondary text-secondary-foreground shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium"
				>
					{badge}
				</span>
			{/if}
			<!-- A muted count means it includes taps the outbox has not flushed yet.
				 No spinner and no error state: the outbox handles it (§13.3). -->
			<span
				class={cn(
					'shrink-0 text-xs tabular-nums',
					pending ? 'text-muted-foreground/60' : 'text-muted-foreground'
				)}
				title={pending ? 'Waiting to sync' : undefined}
			>
				×{count}
			</span>
		</button>

		<DropdownMenu.Root>
			<DropdownMenu.Trigger>
				{#snippet child({ props })}
					<Button
						{...props}
						variant="ghost"
						size="icon"
						class="size-8 shrink-0"
						aria-label="Actions"
					>
						<MoreVerticalIcon class="size-4" />
					</Button>
				{/snippet}
			</DropdownMenu.Trigger>
			<DropdownMenu.Content align="end">
				<DropdownMenu.Item onclick={() => copy(entry.lemma, 'Word copied.')}>
					Copy word
				</DropdownMenu.Item>
				<DropdownMenu.Item onclick={() => copy(entry.latest_translation, 'Translation copied.')}>
					Copy translation
				</DropdownMenu.Item>
				<DropdownMenu.Separator />
				<DropdownMenu.Item variant="destructive" onclick={onDelete}>Delete</DropdownMenu.Item>
			</DropdownMenu.Content>
		</DropdownMenu.Root>
	</div>

	{#if expanded}
		<div
			id="{rowId}-details"
			class="text-muted-foreground flex flex-col gap-2 px-3 pb-3 pl-9 text-sm"
		>
			{#if entry.latest_context}
				<!-- The stored context is labelled with its source here, and ONLY
					 here: in the reader it would be a sentence from a different
					 article and would confuse more than help (§10.3). -->
				<p class="border-border border-l-2 pl-3 leading-relaxed italic">
					«{entry.latest_context}»
				</p>
			{/if}

			{#if forms.length > 0}
				<p class="text-xs">seen as: {forms.join(', ')}</p>
			{/if}

			<p class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
				{#if entry.latest_article_title}
					<span>{entry.latest_article_title}</span>
				{/if}
				{#if capturedAt}
					<span aria-hidden="true">·</span>
					<span>{capturedAt}</span>
				{/if}
				{#if articleAvailable && entry.latest_article_id}
					<a
						href="/article/{entry.latest_article_id}"
						class="text-foreground underline underline-offset-4"
					>
						Open
					</a>
				{/if}
			</p>

			<div>
				<Button variant="ghost" size="sm" class="text-destructive -ml-2" onclick={onDelete}>
					Delete word
				</Button>
			</div>
		</div>
	{/if}
</li>
