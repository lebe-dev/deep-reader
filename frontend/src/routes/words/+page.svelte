<script lang="ts">
	// /words — the collected vocabulary (WORD-CACHE-ARCH.md §13).
	//
	// Reads straight from Dexie (vocab_entries plus the pending lookup outbox
	// count), so it works fully offline. Filter/sort state lives in the URL query
	// so a view is shareable and survives a reload, matching the library page.

	import { browser } from '$app/environment';
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { liveQuery } from 'dexie';
	import { db, SYNC_STATE_ID } from '$lib/db';
	import { toast } from 'svelte-sonner';
	import { captureError } from '$lib/sentry';
	import {
		displayedCount,
		hasPendingLookups,
		refreshVocab,
		removeVocabEntry,
		vocabStore
	} from '$lib/vocab/store.svelte';
	import WordRow from '$lib/components/vocabulary/WordRow.svelte';
	import WordsToolbar from '$lib/components/vocabulary/WordsToolbar.svelte';
	import {
		parseFilter,
		parseSort,
		selectWords,
		type WordsFilter,
		type WordsSort
	} from '$lib/components/vocabulary/words-utils';
	import { Button } from '$lib/components/ui/button';
	import type { VocabEntry } from '$lib/types';

	// How many rows render before "Show more". Virtualisation is deliberately
	// deferred until a real vocabulary proves this insufficient (§13.3).
	const PAGE_SIZE = 100;

	/** Debounce for the search box, per §13.2. */
	const SEARCH_DEBOUNCE_MS = 150;

	// URL-backed view state. `query` is the live input; `debouncedQuery` is what
	// actually filters, so typing does not re-sort the list on every keystroke.
	let query = $state(page.url.searchParams.get('q') ?? '');
	let debouncedQuery = $state(page.url.searchParams.get('q') ?? '');
	let filter = $state<WordsFilter>(parseFilter(page.url.searchParams.get('kind')));
	let sort = $state<WordsSort>(parseSort(page.url.searchParams.get('sort')));

	let expandedKey = $state<string | null>(null);
	let visibleCount = $state(PAGE_SIZE);

	/** Ids of articles still present locally, so a dead link renders as text. */
	let availableArticleIds = $state(new Set<string>());

	/** BCP 47 tag of the stored translations; the seeded backend default is 'ru'. */
	let targetLang = $state('ru');

	let searchTimer: ReturnType<typeof setTimeout> | undefined;

	function syncUrl() {
		if (!browser) return;
		const params = new URLSearchParams();
		if (query.trim() !== '') params.set('q', query.trim());
		if (filter !== 'all') params.set('kind', filter);
		if (sort !== 'frequency') params.set('sort', sort);
		const search = params.toString();
		replaceState(search === '' ? page.url.pathname : `${page.url.pathname}?${search}`, {});
	}

	function handleQueryChange(value: string) {
		query = value;
		clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			debouncedQuery = value;
			visibleCount = PAGE_SIZE;
			syncUrl();
		}, SEARCH_DEBOUNCE_MS);
	}

	function handleFilterChange(value: WordsFilter) {
		filter = value;
		visibleCount = PAGE_SIZE;
		syncUrl();
	}

	function handleSortChange(value: WordsSort) {
		sort = value;
		visibleCount = PAGE_SIZE;
		syncUrl();
	}

	function resetFilters() {
		query = '';
		debouncedQuery = '';
		filter = 'all';
		sort = 'frequency';
		visibleCount = PAGE_SIZE;
		syncUrl();
	}

	async function handleDelete(entry: VocabEntry) {
		try {
			await removeVocabEntry(entry.entry_key);
			// No confirmation dialog and no undo: the deletion is soft server-side
			// and the entry revives on the next lookup, so a misclick costs one tap
			// in an article (§13.4). Deliberately lighter than the library's
			// DeleteDialog, where deletion is destructive.
			toast(`Removed: ${entry.lemma}`);
		} catch (err) {
			captureError(err, { area: 'vocab', extra: { op: 'removeVocabEntry' } });
			toast.error('Failed to remove the word.');
		}
	}

	// The rendered list. vocabStore is the shared snapshot the reader overlay
	// uses too, so a word collected while reading appears here without a reload.
	const words = $derived(
		selectWords(vocabStore.entries, {
			query: debouncedQuery,
			filter,
			sort,
			countOf: displayedCount
		})
	);
	const visible = $derived(words.slice(0, visibleCount));
	const hasFilters = $derived(debouncedQuery.trim() !== '' || filter !== 'all');

	$effect(() => {
		void refreshVocab();
	});

	// Keep the "Open" links honest: an article deleted from the library no
	// longer resolves, and the vocabulary built from it deliberately outlives it.
	$effect(() => {
		if (!browser) return;
		const sub = liveQuery(() => db.articles_meta.toArray()).subscribe({
			next(metas) {
				availableArticleIds = new Set(metas.map((m) => m.id));
			},
			error(err) {
				captureError(err, { area: 'vocab', extra: { query: 'articles_meta' } });
			}
		});
		return () => sub.unsubscribe();
	});

	// The language every stored translation on this page is written in, so the
	// rows can tag it and a screen reader reads it with the right voice.
	$effect(() => {
		if (!browser) return;
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.settings?.target_language) targetLang = state.settings.target_language;
			},
			error(err) {
				captureError(err, { area: 'vocab', extra: { query: 'sync_state' } });
			}
		});
		return () => sub.unsubscribe();
	});
</script>

<svelte:head>
	<title>My Words — Deep Reader</title>
</svelte:head>

<div class="mx-auto flex w-full max-w-3xl flex-col px-4 pb-16">
	<h1 class="pt-6 text-2xl font-semibold">My Words</h1>

	<WordsToolbar
		{query}
		{filter}
		{sort}
		onQueryChange={handleQueryChange}
		onFilterChange={handleFilterChange}
		onSortChange={handleSortChange}
	/>

	{#if !vocabStore.loaded}
		<p class="text-muted-foreground py-10 text-center text-sm">Loading…</p>
	{:else if vocabStore.entries.length === 0}
		<div class="flex flex-col items-center gap-3 py-16 text-center">
			<p class="text-muted-foreground max-w-sm text-sm leading-relaxed">
				Words collect themselves here — just read, and open the translation of anything you do not
				know.
			</p>
			<Button href="/" variant="outline" size="sm">Go to library</Button>
		</div>
	{:else if words.length === 0}
		<div class="flex flex-col items-center gap-3 py-16 text-center">
			<p class="text-muted-foreground text-sm">Nothing found</p>
			<Button variant="outline" size="sm" onclick={resetFilters}>Reset filters</Button>
		</div>
	{:else}
		<ul class="border-border/60 mt-1 flex flex-col rounded-md border">
			{#each visible as entry (entry.entry_key)}
				<WordRow
					{entry}
					count={displayedCount(entry)}
					pending={hasPendingLookups(entry)}
					query={debouncedQuery}
					lang={targetLang}
					articleAvailable={availableArticleIds.has(entry.latest_article_id)}
					expanded={expandedKey === entry.entry_key}
					onToggle={() => (expandedKey = expandedKey === entry.entry_key ? null : entry.entry_key)}
					onDelete={() => handleDelete(entry)}
				/>
			{/each}
		</ul>

		{#if words.length > visible.length}
			<div class="flex justify-center pt-4">
				<Button variant="outline" size="sm" onclick={() => (visibleCount += PAGE_SIZE)}>
					Show more
				</Button>
			</div>
		{/if}

		<p class="text-muted-foreground pt-4 text-center text-xs">
			{#if hasFilters}
				{words.length} of {vocabStore.entries.length}
			{:else}
				{vocabStore.entries.length} total
			{/if}
		</p>
	{/if}
</div>
