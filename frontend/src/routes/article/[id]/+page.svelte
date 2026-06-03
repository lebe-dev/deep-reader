<script lang="ts">
	// Reader page — the core reading experience.
	// Spec §6 "Reader UI", §7 "Reading flow", §8 data model.
	//
	// Load strategy (spec §6):
	//   1. Check IndexedDB (articles_payload) — render immediately if cached.
	//   2. If missing and online → fetch from API, cache, render.
	//   3. If missing and offline → "unavailable offline" state.

	import { page } from '$app/state';
	import { browser } from '$app/environment';
	import { db } from '$lib/db';
	import { getArticle } from '$lib/api';
	import { enqueueProgress, enqueuePin } from '$lib/sync/engine';
	import { OfflineError } from '$lib/api';
	import type { ArticleMeta, ArticlePayload, Progress } from '$lib/types';
	import TokenRenderer from '$lib/components/reader/TokenRenderer.svelte';
	import WordPopover from '$lib/components/reader/WordPopover.svelte';
	import SentenceSheet from '$lib/components/reader/SentenceSheet.svelte';
	import type { PopoverContent, SentenceSheetContent } from '$lib/components/reader/reader-utils';
	import { debounce } from '$lib/components/reader/reader-utils';
	import { readerFont, getReaderFontCss } from '$lib/reader-font.svelte';
	import CoverageBadge from '$lib/components/CoverageBadge.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import BookOpenCheckIcon from '@lucide/svelte/icons/book-open-check';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import PinIcon from '@lucide/svelte/icons/pin';
	import PinOffIcon from '@lucide/svelte/icons/pin-off';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import WifiOffIcon from '@lucide/svelte/icons/wifi-off';
	import AlertCircleIcon from '@lucide/svelte/icons/circle-alert';

	// ---------------------------------------------------------------------------
	// Route param
	// ---------------------------------------------------------------------------

	const articleId = $derived(page.params.id);

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	type LoadState = 'loading' | 'ready' | 'offline' | 'error' | 'not-enriched';

	let loadState: LoadState = $state('loading');
	let errorMessage: string | undefined = $state();

	let meta: ArticleMeta | undefined = $state();
	let payload: ArticlePayload | undefined = $state();
	let progress: Progress | undefined = $state();

	// only allow http(s) source links to avoid javascript:/data: href injection
	const safeSourceUrl = $derived(
		meta?.source_url && /^https?:\/\//i.test(meta.source_url) ? meta.source_url : null
	);

	// Enrichment completeness for the header indicator. The payload is the
	// authoritative source on this page (meta may be absent on a cold network load).
	const coverage = $derived(payload?.enrichment_coverage ?? null);

	// Popover / sheet state.
	let wordContent: PopoverContent | null = $state(null);
	let wordAnchor: HTMLElement | null = $state(null);
	let sentenceContent: SentenceSheetContent | null = $state(null);

	// ---------------------------------------------------------------------------
	// Load article
	// ---------------------------------------------------------------------------

	async function loadArticle(id: string): Promise<void> {
		loadState = 'loading';
		errorMessage = undefined;
		payload = undefined;

		// Always try the local cache first.
		const cached = await db.articles_payload.get(id);
		const cachedMeta = await db.articles_meta.get(id);
		meta = cachedMeta;

		if (cached) {
			payload = cached;
			progress = await db.progress.get(id);
			loadState = 'ready';
			return;
		}

		// Cache miss — need the network.
		if (!navigator.onLine) {
			loadState = 'offline';
			return;
		}

		try {
			const fetched = await getArticle(id);

			if (fetched.status !== 'enriched') {
				loadState = 'not-enriched';
				return;
			}

			// Cache for offline use.
			await db.articles_payload.put(fetched);

			payload = fetched;
			progress = await db.progress.get(id);
			loadState = 'ready';
		} catch (err) {
			if (err instanceof OfflineError) {
				loadState = 'offline';
				return;
			}
			errorMessage = err instanceof Error ? err.message : String(err);
			loadState = 'error';
		}
	}

	// ---------------------------------------------------------------------------
	// Progress tracking
	// ---------------------------------------------------------------------------

	const PROGRESS_DEBOUNCE_MS = 2000;

	const debouncedPersistProgress = debounce((tokenIndex: number) => {
		if (!articleId) return;
		const now = new Date().toISOString();
		const updated: Progress = {
			article_id: articleId,
			position: tokenIndex,
			is_read: progress?.is_read ?? false,
			updated_at: now
		};
		progress = updated;
		enqueueProgress(updated).catch(console.warn);
	}, PROGRESS_DEBOUNCE_MS);

	function handleProgress(tokenIndex: number) {
		debouncedPersistProgress(tokenIndex);
	}

	// ---------------------------------------------------------------------------
	// Mark as read toggle
	// ---------------------------------------------------------------------------

	// ---------------------------------------------------------------------------
	// Pin toggle
	// ---------------------------------------------------------------------------

	async function togglePin() {
		if (!meta) return;
		const next = !meta.pinned;
		// Optimistic UI; enqueuePin persists + syncs the change.
		meta = { ...meta, pinned: next };
		try {
			await enqueuePin(meta.id, next);
		} catch {
			meta = { ...meta, pinned: !next };
		}
	}

	function toggleRead() {
		if (!articleId) return;
		const now = new Date().toISOString();
		const updated: Progress = {
			article_id: articleId,
			position: progress?.position ?? 0,
			is_read: !progress?.is_read,
			updated_at: now
		};
		progress = updated;
		enqueueProgress(updated).catch(console.warn);
	}

	// ---------------------------------------------------------------------------
	// Popover / sheet handlers
	// ---------------------------------------------------------------------------

	function handleWordClick(content: PopoverContent | null, anchor: HTMLElement | null) {
		// Close sentence sheet when opening word popover.
		if (content !== null) sentenceContent = null;
		wordContent = content;
		wordAnchor = anchor;
	}

	function handleSentenceSelect(content: SentenceSheetContent | null) {
		// Close word popover when opening sentence sheet.
		if (content !== null) {
			wordContent = null;
			wordAnchor = null;
		}
		sentenceContent = content;
	}

	function closeWordPopover() {
		wordContent = null;
		wordAnchor = null;
	}

	function closeSentenceSheet() {
		sentenceContent = null;
	}

	// ---------------------------------------------------------------------------
	// Lifecycle
	// ---------------------------------------------------------------------------

	let currentId: string | undefined;

	$effect(() => {
		if (!browser) return;
		const id = articleId;
		if (!id || id === currentId) return;
		currentId = id;
		loadArticle(id).catch(console.error);
	});
</script>

<svelte:head>
	<title>{meta?.title ?? 'Article'} — Deep Reader</title>
</svelte:head>

<!-- Word/phrase popover -->
<WordPopover content={wordContent} anchorEl={wordAnchor} onclose={closeWordPopover} />

<!-- Sentence sheet -->
<SentenceSheet content={sentenceContent} onclose={closeSentenceSheet} />

{#if loadState === 'loading'}
	<!-- Skeleton loader -->
	<div class="flex flex-col gap-4 py-4">
		<Skeleton class="h-8 w-3/4 rounded" />
		<Skeleton class="h-4 w-1/3 rounded" />
		<div class="mt-4 flex flex-col gap-3">
			{#each { length: 8 } as _}
				<Skeleton class="h-4 w-full rounded" />
			{/each}
			<Skeleton class="h-4 w-2/3 rounded" />
		</div>
	</div>
{:else if loadState === 'offline'}
	<div class="flex flex-col items-center gap-4 py-16 text-center">
		<WifiOffIcon class="text-muted-foreground size-12" />
		<h2 class="text-lg font-semibold">Unavailable offline</h2>
		<p class="text-muted-foreground max-w-xs text-sm">
			This article hasn't been cached yet. Connect to the internet to load it.
		</p>
		<Button variant="outline" href="/">Back to library</Button>
	</div>
{:else if loadState === 'not-enriched'}
	<div class="flex flex-col items-center gap-4 py-16 text-center">
		<AlertCircleIcon class="text-muted-foreground size-12" />
		<h2 class="text-lg font-semibold">Not ready yet</h2>
		<p class="text-muted-foreground max-w-xs text-sm">
			This article is still being processed. Check back in a moment.
		</p>
		<Button variant="outline" href="/">Back to library</Button>
	</div>
{:else if loadState === 'error'}
	<div class="flex flex-col items-center gap-4 py-16 text-center">
		<AlertCircleIcon class="text-destructive size-12" />
		<h2 class="text-lg font-semibold">Failed to load</h2>
		{#if errorMessage}
			<p class="text-muted-foreground max-w-xs text-sm font-mono">{errorMessage}</p>
		{/if}
		<Button variant="outline" href="/">Back to library</Button>
	</div>
{:else if loadState === 'ready' && payload}
	<!-- Article header -->
	<div class="mb-6 flex flex-col gap-3">
		<div class="flex items-start justify-between gap-3">
			<h1
				class="text-xl font-semibold leading-snug sm:text-2xl"
				style="font-family: {getReaderFontCss(readerFont.value)}"
			>
				{meta?.title ?? 'Article'}
			</h1>
			<div class="mt-0.5 flex shrink-0 items-center">
				<Button
					variant="ghost"
					size="icon"
					onclick={togglePin}
					aria-label={meta?.pinned ? 'Unpin article' : 'Pin article'}
					title={meta?.pinned ? 'Unpin' : 'Pin to top'}
				>
					{#if meta?.pinned}
						<PinOffIcon class="text-primary size-5" />
					{:else}
						<PinIcon class="text-muted-foreground size-5" />
					{/if}
				</Button>
				<Button
					variant="ghost"
					size="icon"
					onclick={toggleRead}
					aria-label={progress?.is_read ? 'Mark as unread' : 'Mark as read'}
					title={progress?.is_read ? 'Mark as unread' : 'Mark as read'}
				>
					{#if progress?.is_read}
						<BookOpenCheckIcon class="text-primary size-5" />
					{:else}
						<BookOpenIcon class="text-muted-foreground size-5" />
					{/if}
				</Button>
			</div>
		</div>

		<div class="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
			{#if meta?.author}
				<span>{meta.author}</span>
				<span aria-hidden="true">·</span>
			{/if}
			{#if meta?.source_domain && safeSourceUrl}
				<a
					href={safeSourceUrl}
					target="_blank"
					rel="noopener noreferrer"
					class="hover:text-foreground inline-flex items-center gap-1 transition-colors"
				>
					{meta.source_domain}
					<ExternalLinkIcon class="size-3" />
				</a>
			{/if}
			{#if coverage !== null}
				<CoverageBadge {coverage} showLabel />
			{/if}
			{#if progress?.is_read}
				<Badge variant="secondary" class="ml-auto text-xs">Read</Badge>
			{/if}
		</div>

		<!-- Enrichment legend hint (show on first visit feeling) -->
		<p class="text-muted-foreground text-xs">
			<span class="underline decoration-dotted decoration-1 underline-offset-3"
				>Dotted underline</span
			>
			= difficult word ·
			<span class="underline decoration-solid decoration-1 underline-offset-3">Solid underline</span
			> = phrase · Tap to translate · Shift-click or long-press for sentence
		</p>
	</div>

	<!-- Token renderer -->
	<div style="font-family: {getReaderFontCss(readerFont.value)}">
		<TokenRenderer
			tokens={payload.tokens}
			originalText={payload.original_text}
			enrichment={payload.enrichment}
			initialPosition={progress?.position ?? 0}
			onProgress={handleProgress}
			onWordClick={handleWordClick}
			onSentenceSelect={handleSentenceSelect}
		/>
	</div>

	<!-- Glossary (if any) -->
	{#if payload.enrichment.glossary.length > 0}
		<div class="mt-10 border-t pt-6">
			<h2 class="mb-4 text-sm font-semibold tracking-wide uppercase opacity-60">Glossary</h2>
			<dl class="flex flex-col gap-4">
				{#each payload.enrichment.glossary as item (item.term)}
					<div class="flex flex-col gap-0.5">
						<dt class="text-sm font-semibold">{item.term}</dt>
						<dd class="text-muted-foreground text-sm leading-relaxed">{item.definition}</dd>
					</div>
				{/each}
			</dl>
		</div>
	{/if}

	<!-- End-of-article mark-read CTA -->
	{#if !progress?.is_read}
		<div class="mt-10 flex justify-center pb-6">
			<Button variant="outline" onclick={toggleRead} class="gap-2">
				<BookOpenCheckIcon class="size-4" />
				Mark as read
			</Button>
		</div>
	{/if}
{/if}
