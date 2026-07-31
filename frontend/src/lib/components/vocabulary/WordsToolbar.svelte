<script lang="ts">
	// Search / filter / sort controls for the /words screen (§13.2).
	//
	// It owns no state: every control reports upward, and the page keeps the
	// canonical values in the URL query so a view is shareable and survives a
	// reload — matching the library page.

	import { Input } from '$lib/components/ui/input';
	import { Button } from '$lib/components/ui/button';
	import { cn } from '$lib/utils';
	import SearchIcon from '@lucide/svelte/icons/search';
	import ArrowUpDownIcon from '@lucide/svelte/icons/arrow-up-down';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import type { WordsFilter, WordsSort } from './words-utils';

	interface Props {
		/** The live (un-debounced) input value. */
		query: string;
		filter: WordsFilter;
		sort: WordsSort;
		onQueryChange: (value: string) => void;
		onFilterChange: (value: WordsFilter) => void;
		onSortChange: (value: WordsSort) => void;
	}

	let { query, filter, sort, onQueryChange, onFilterChange, onSortChange }: Props = $props();

	const filters: { value: WordsFilter; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'word', label: 'Words' },
		{ value: 'phrase', label: 'Phrases' }
	];

	const sorts: { value: WordsSort; label: string }[] = [
		{ value: 'frequency', label: 'Frequency' },
		{ value: 'recent', label: 'Recent' },
		{ value: 'oldest', label: 'Oldest first' },
		{ value: 'alpha', label: 'A–Z' }
	];

	const sortLabel = $derived(sorts.find((s) => s.value === sort)?.label ?? 'Frequency');
</script>

<!-- Sticky so the filters stay reachable in a long list. -->
<div
	class="bg-background/95 supports-[backdrop-filter]:bg-background/80 sticky top-0 z-10 flex flex-wrap items-center gap-2 py-3 backdrop-blur"
>
	<div class="relative min-w-[12rem] flex-1">
		<SearchIcon
			class="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2"
			aria-hidden="true"
		/>
		<Input
			type="search"
			enterkeyhint="search"
			placeholder="search…"
			aria-label="Search my words"
			class="pl-8"
			value={query}
			oninput={(e) => onQueryChange((e.currentTarget as HTMLInputElement).value)}
		/>
	</div>

	<div class="flex items-center gap-0.5 rounded-md border p-0.5" role="group" aria-label="Filter">
		{#each filters as option (option.value)}
			<button
				type="button"
				class={cn(
					'rounded-sm px-2.5 py-1 text-sm transition-colors',
					filter === option.value
						? 'bg-accent text-foreground'
						: 'text-muted-foreground hover:text-foreground'
				)}
				aria-pressed={filter === option.value}
				onclick={() => onFilterChange(option.value)}
			>
				{option.label}
			</button>
		{/each}
	</div>

	<DropdownMenu.Root>
		<DropdownMenu.Trigger>
			{#snippet child({ props })}
				<Button {...props} variant="outline" size="sm" class="gap-2">
					<ArrowUpDownIcon class="size-4" />
					<span>{sortLabel}</span>
				</Button>
			{/snippet}
		</DropdownMenu.Trigger>
		<DropdownMenu.Content align="end">
			{#each sorts as option (option.value)}
				<DropdownMenu.Item onclick={() => onSortChange(option.value)}>
					{option.label}
				</DropdownMenu.Item>
			{/each}
		</DropdownMenu.Content>
	</DropdownMenu.Root>
</div>
