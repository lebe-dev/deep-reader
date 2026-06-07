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
	import { onDestroy } from 'svelte';
	import { liveQuery } from 'dexie';
	import { db, SYNC_STATE_ID } from '$lib/db';
	import { getArticle, ApiError } from '$lib/api';
	import {
		enqueueProgress,
		enqueueReEnrich,
		enqueueSetRead,
		enqueueResetProgress
	} from '$lib/sync/engine';
	import { OfflineError } from '$lib/api';
	import type {
		ArticleMeta,
		ArticlePayload,
		Progress,
		ReEnrichMode,
		FontSize,
		LineHeight
	} from '$lib/types';
	import TokenRenderer from '$lib/components/reader/TokenRenderer.svelte';
	import WordPopover from '$lib/components/reader/WordPopover.svelte';
	import SentenceSheet from '$lib/components/reader/SentenceSheet.svelte';
	import SentenceMenu from '$lib/components/reader/SentenceMenu.svelte';
	import type {
		PopoverContent,
		SentenceMenuContent,
		SentenceSheetContent
	} from '$lib/components/reader/reader-utils';
	import { debounce } from '$lib/components/reader/reader-utils';
	import { readerFont, getReaderFontCss } from '$lib/reader-font.svelte';
	import {
		readerFullscreen,
		enterReaderFullscreen,
		exitReaderFullscreen
	} from '$lib/reader-fullscreen.svelte';
	import {
		fontSizeRem,
		lineHeightMultiplier,
		DEFAULT_FONT_SIZE,
		DEFAULT_LINE_HEIGHT
	} from '$lib/reader-typography';
	import CoverageBadge from '$lib/components/CoverageBadge.svelte';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { toast } from 'svelte-sonner';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import WifiOffIcon from '@lucide/svelte/icons/wifi-off';
	import AlertCircleIcon from '@lucide/svelte/icons/circle-alert';
	import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
	import Maximize2Icon from '@lucide/svelte/icons/maximize-2';
	import Minimize2Icon from '@lucide/svelte/icons/minimize-2';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import LanguagesIcon from '@lucide/svelte/icons/languages';
	import ListPlusIcon from '@lucide/svelte/icons/list-plus';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCheckIcon from '@lucide/svelte/icons/check-check';
	import CircleIcon from '@lucide/svelte/icons/circle';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';

	// ---------------------------------------------------------------------------
	// Route param
	// ---------------------------------------------------------------------------

	const articleId = $derived(page.params.id);

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	type LoadState = 'loading' | 'ready' | 'offline' | 'error' | 'not-enriched' | 'reenriching';

	let loadState: LoadState = $state('loading');
	let errorMessage: string | undefined = $state();
	// The mode of an in-flight re-enrichment, for the processing-state copy.
	let reEnrichMode: ReEnrichMode | undefined = $state();

	let meta: ArticleMeta | undefined = $state();
	let payload: ArticlePayload | undefined = $state();
	let progress: Progress | undefined = $state();

	// Live processing info for the "not ready yet" screen, refreshed by the poll
	// loop while an article is still being processed: the current pipeline stage
	// label and the sentence-coverage fraction (0→1) reached so far.
	let processingStage: string | undefined = $state();
	let processingCoverage = $state(0);
	const processingPercent = $derived(Math.round(processingCoverage * 100));

	// only allow http(s) source links to avoid javascript:/data: href injection
	const safeSourceUrl = $derived(
		meta?.source_url && /^https?:\/\//i.test(meta.source_url) ? meta.source_url : null
	);

	// Enrichment completeness for the header indicator. The payload is the
	// authoritative source on this page (meta may be absent on a cold network load).
	const coverage = $derived(payload?.enrichment_coverage ?? null);

	// Model that produced the enrichment, shown in the header so the reader can
	// tell which model they're reading. Empty until enriched.
	const llmModel = $derived(payload?.llm_model || meta?.llm_model || null);

	// Reader typography, driven by the synced Appearance settings. Applied to the
	// reader via CSS custom properties so font size / line spacing react live.
	let fontSize = $state<FontSize>(DEFAULT_FONT_SIZE);
	let lineHeight = $state<LineHeight>(DEFAULT_LINE_HEIGHT);
	const readerStyle = $derived(
		`--reader-font-size: ${fontSizeRem(fontSize)};` +
			` --reader-line-height: ${lineHeightMultiplier(lineHeight)};` +
			` font-family: ${getReaderFontCss(readerFont.value)}`
	);

	// Summary disclosure — collapsed by default; the user expands on demand.
	let summaryOpen = $state(false);

	// Popover / sheet state.
	let wordContent: PopoverContent | null = $state(null);
	let wordAnchor: HTMLElement | null = $state(null);
	let sentenceContent: SentenceSheetContent | null = $state(null);
	let sentenceMenu: SentenceMenuContent | null = $state(null);
	let sentenceMenuAnchor: HTMLElement | null = $state(null);

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
			// Cache-bust by updated_at so a re-enrich (same enrichment_version) is
			// fetched fresh rather than served from the immutable HTTP cache.
			const fetched = await getArticle(id, undefined, { version: cachedMeta?.updated_at });

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
			// A still-processing article comes back as 409 with the payload as its
			// body. Surface the live stage/coverage and poll until it's ready.
			const proc = processingPayloadFrom(err);
			if (proc) {
				processingStage = proc.progress_stage;
				processingCoverage = proc.enrichment_coverage ?? 0;
				loadState = 'not-enriched';
				startPolling(id);
				return;
			}
			errorMessage = err instanceof Error ? err.message : String(err);
			loadState = 'error';
		}
	}

	// processingPayloadFrom extracts the article payload carried by a 409
	// "still processing" response (the getArticle endpoint returns the payload as
	// the 409 body). Returns null for any other error.
	function processingPayloadFrom(err: unknown): ArticlePayload | null {
		if (!(err instanceof ApiError) || err.status !== 409) return null;
		try {
			return JSON.parse(err.body) as ArticlePayload;
		} catch {
			return null;
		}
	}

	// ---------------------------------------------------------------------------
	// Re-enrich (improve translation) + auto-polling
	// ---------------------------------------------------------------------------

	// "Top up" only makes sense while some text is still uncovered.
	const canTopUp = $derived(coverage !== null && coverage < 1);

	const POLL_INTERVAL_MS = 4000;
	const POLL_TIMEOUT_MS = 5 * 60 * 1000;
	let pollTimer: ReturnType<typeof setTimeout> | undefined;

	function stopPolling() {
		if (pollTimer) {
			clearTimeout(pollTimer);
			pollTimer = undefined;
		}
	}

	// Poll the article until it is enriched again, then swap in the fresh payload.
	// Uses no-store to bypass the immutable HTTP cache on the enriched payload.
	function startPolling(id: string) {
		stopPolling();
		const startedAt = Date.now();

		const tick = async () => {
			pollTimer = undefined;
			if (id !== currentId) return; // navigated away
			if (!navigator.onLine) {
				pollTimer = setTimeout(tick, POLL_INTERVAL_MS);
				return;
			}
			try {
				const fetched = await getArticle(id, undefined, { noStore: true });
				if (fetched.status === 'enriched') {
					await db.articles_payload.put(fetched);
					payload = fetched;
					progress = await db.progress.get(id);
					meta = (await db.articles_meta.get(id)) ?? meta;
					loadState = 'ready';
					reEnrichMode = undefined;
					return;
				}
				// Still processing (200 with a non-enriched status) — refresh the
				// live stage/coverage so the waiting screen advances.
				processingStage = fetched.progress_stage;
				processingCoverage = fetched.enrichment_coverage ?? 0;
			} catch (err) {
				// 409 still carries the payload — refresh the live stage/coverage from
				// it. Offline / other errors are expected here; keep polling.
				const proc = processingPayloadFrom(err);
				if (proc) {
					processingStage = proc.progress_stage;
					processingCoverage = proc.enrichment_coverage ?? 0;
				}
			}
			if (id !== currentId) return;
			if (Date.now() - startedAt > POLL_TIMEOUT_MS) {
				errorMessage = 'Re-enrichment is taking longer than expected. Check back later.';
				loadState = 'error';
				reEnrichMode = undefined;
				return;
			}
			pollTimer = setTimeout(tick, POLL_INTERVAL_MS);
		};

		pollTimer = setTimeout(tick, POLL_INTERVAL_MS);
	}

	async function handleReEnrich(mode: ReEnrichMode) {
		if (!articleId) return;
		const id = articleId;
		loadState = 'reenriching';
		reEnrichMode = mode;
		try {
			await enqueueReEnrich(id, mode);
		} catch {
			toast.error('Failed to start re-enrichment.');
		}
		startPolling(id);
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

	async function handleToggleRead() {
		if (!articleId) return;
		const next = !progress?.is_read;
		try {
			await enqueueSetRead(articleId, next);
			progress = await db.progress.get(articleId);
			toast(next ? 'Marked as read.' : 'Marked as unread.');
		} catch {
			toast.error('Failed to update read status.');
		}
	}

	async function handleResetProgress() {
		if (!articleId) return;
		try {
			await enqueueResetProgress(articleId);
			progress = await db.progress.get(articleId);
			toast('Reading progress reset.');
		} catch {
			toast.error('Failed to reset progress.');
		}
	}

	// ---------------------------------------------------------------------------
	// Fullscreen toggle
	// ---------------------------------------------------------------------------

	function toggleFullscreen() {
		if (readerFullscreen.active) {
			exitReaderFullscreen();
		} else {
			enterReaderFullscreen();
		}
	}

	// ---------------------------------------------------------------------------
	// Popover / sheet handlers
	// ---------------------------------------------------------------------------

	function handleWordClick(content: PopoverContent | null, anchor: HTMLElement | null) {
		// Close the sentence sheet / menu when opening a word popover.
		if (content !== null) {
			sentenceContent = null;
			closeSentenceMenu();
		}
		wordContent = content;
		wordAnchor = anchor;
	}

	function handleSentenceSelect(content: SentenceSheetContent | null) {
		// Close word popover when opening sentence sheet.
		if (content !== null) {
			wordContent = null;
			wordAnchor = null;
			closeSentenceMenu();
		}
		sentenceContent = content;
	}

	function handleSentenceMenu(content: SentenceMenuContent | null, anchor: HTMLElement | null) {
		// Opening the menu supersedes any open word popover / sentence sheet.
		if (content !== null) {
			wordContent = null;
			wordAnchor = null;
			sentenceContent = null;
		}
		sentenceMenu = content;
		sentenceMenuAnchor = anchor;
	}

	async function handleSentenceCopy(text: string) {
		closeSentenceMenu();
		try {
			await navigator.clipboard.writeText(text);
			toast('Sentence copied to clipboard.');
		} catch {
			toast.error('Could not copy the sentence.');
		}
	}

	function handleSentenceTranslate(content: SentenceMenuContent) {
		closeSentenceMenu();
		handleSentenceSelect({
			kind: 'sentence',
			original: content.original,
			translation: content.translation
		});
	}

	function closeWordPopover() {
		wordContent = null;
		wordAnchor = null;
	}

	function closeSentenceSheet() {
		sentenceContent = null;
	}

	function closeSentenceMenu() {
		sentenceMenu = null;
		sentenceMenuAnchor = null;
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
		stopPolling();
		reEnrichMode = undefined;
		loadArticle(id).catch(console.error);
	});

	// Track the synced reader typography so size/spacing changes apply live.
	$effect(() => {
		if (!browser) return;
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.settings?.font_size) fontSize = state.settings.font_size;
				if (state?.settings?.line_height) lineHeight = state.settings.line_height;
			},
			error(err) {
				console.error('[reader] sync_state liveQuery error', err);
			}
		});
		return () => sub.unsubscribe();
	});

	onDestroy(stopPolling);
	// Immersive mode is reader-only; leave it when navigating away.
	onDestroy(exitReaderFullscreen);
</script>

<svelte:head>
	<title>{meta?.title ?? 'Article'} — Deep Reader</title>
</svelte:head>

<!-- Word/phrase popover -->
<WordPopover content={wordContent} anchorEl={wordAnchor} onclose={closeWordPopover} />

<!-- Sentence sheet -->
<SentenceSheet content={sentenceContent} onclose={closeSentenceSheet} />

<!-- Sentence long-press action menu -->
<SentenceMenu
	content={sentenceMenu}
	anchorEl={sentenceMenuAnchor}
	oncopy={handleSentenceCopy}
	ontranslate={handleSentenceTranslate}
	onclose={closeSentenceMenu}
/>

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
		<LoaderCircleIcon class="text-primary size-12 animate-spin" />
		<h2 class="text-lg font-semibold">{processingStage?.trim() || 'Processing…'}</h2>
		<p class="text-muted-foreground max-w-xs text-sm">
			This article is still being processed. The page updates automatically when it's ready.
		</p>
		{#if processingPercent > 0}
			<div class="w-full max-w-xs">
				<div class="text-muted-foreground mb-1.5 text-right text-xs tabular-nums">
					{processingPercent}%
				</div>
				<div
					class="bg-muted h-1.5 w-full overflow-hidden rounded-full"
					role="progressbar"
					aria-valuenow={processingPercent}
					aria-valuemin={0}
					aria-valuemax={100}
					aria-label="Processing progress"
				>
					<div
						class="bg-primary h-full rounded-full transition-[width] duration-500"
						style="width: {processingPercent}%"
					></div>
				</div>
			</div>
		{/if}
		<Button variant="outline" href="/">Back to library</Button>
	</div>
{:else if loadState === 'reenriching'}
	<div class="flex flex-col items-center gap-4 py-16 text-center">
		<LoaderCircleIcon class="text-primary size-12 animate-spin" />
		<h2 class="text-lg font-semibold">
			{reEnrichMode === 'topup' ? 'Filling in missing translation…' : 'Re-translating…'}
		</h2>
		<p class="text-muted-foreground max-w-xs text-sm">
			This usually takes a moment. The page updates automatically when it's ready.
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
	<div class="relative mb-6 flex flex-col gap-3">
		<div class="flex items-start justify-between gap-3">
			<h1
				class="text-2xl font-semibold leading-snug sm:text-3xl"
				style="font-family: {getReaderFontCss(readerFont.value)}"
			>
				{meta?.title ?? 'Article'}
			</h1>
			<Button
				variant="ghost"
				size="icon"
				class="shrink-0"
				onclick={toggleFullscreen}
				aria-label={readerFullscreen.active ? 'Exit fullscreen reading' : 'Fullscreen reading'}
				title={readerFullscreen.active ? 'Exit fullscreen reading' : 'Fullscreen reading'}
			>
				{#if readerFullscreen.active}
					<Minimize2Icon class="text-muted-foreground size-5" />
				{:else}
					<Maximize2Icon class="text-muted-foreground size-5" />
				{/if}
			</Button>
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
			{#if llmModel}
				<span aria-hidden="true">·</span>
				<span class="font-mono text-xs" title="Model used for translation">{llmModel}</span>
			{/if}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger
					class={buttonVariants({ variant: 'ghost', size: 'icon-sm' })}
					title="Article options"
					aria-label="Article options"
				>
					<EllipsisIcon class="size-4" />
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="start" class="w-60">
					<DropdownMenu.Label>Improve translation</DropdownMenu.Label>
					<DropdownMenu.Separator />
					<DropdownMenu.Item onSelect={() => handleReEnrich('full')}>
						<LanguagesIcon class="size-4" />
						Re-translate from scratch
					</DropdownMenu.Item>
					<DropdownMenu.Item onSelect={() => handleReEnrich('topup')} disabled={!canTopUp}>
						<ListPlusIcon class="size-4" />
						Fill in missing parts
					</DropdownMenu.Item>
					<DropdownMenu.Separator />
					<DropdownMenu.Label>Reading</DropdownMenu.Label>
					<DropdownMenu.Item onSelect={handleToggleRead}>
						{#if progress?.is_read}
							<CircleIcon class="size-4" />
							Mark as unread
						{:else}
							<CheckCheckIcon class="size-4" />
							Mark as read
						{/if}
					</DropdownMenu.Item>
					{#if (progress?.position ?? 0) > 0}
						<DropdownMenu.Item onSelect={handleResetProgress}>
							<RotateCcwIcon class="size-4" />
							Reset reading progress
						</DropdownMenu.Item>
					{/if}
				</DropdownMenu.Content>
			</DropdownMenu.Root>
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
			> = phrase · Tap a word to translate · Long-press or right-click for sentence
		</p>
	</div>

	<!-- Summary (if any) — collapsed by default, toggled by the header. -->
	{#if payload.summary}
		<div class="bg-muted/40 mb-6 rounded-lg border">
			<button
				type="button"
				onclick={() => (summaryOpen = !summaryOpen)}
				aria-expanded={summaryOpen}
				class="text-muted-foreground flex w-full items-center justify-between gap-2 p-4 text-xs font-semibold tracking-wide uppercase opacity-60 transition-opacity hover:opacity-100"
			>
				Summary
				<ChevronDownIcon class="size-4 transition-transform {summaryOpen ? 'rotate-180' : ''}" />
			</button>
			{#if summaryOpen}
				<p class="px-4 pb-4 text-sm leading-relaxed">{payload.summary}</p>
			{/if}
		</div>
	{/if}

	<!-- Token renderer -->
	<div style={readerStyle}>
		<TokenRenderer
			tokens={payload.tokens}
			originalText={payload.original_text}
			enrichment={payload.enrichment}
			initialPosition={progress?.position ?? 0}
			onProgress={handleProgress}
			onWordClick={handleWordClick}
			onSentenceMenu={handleSentenceMenu}
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
{/if}
