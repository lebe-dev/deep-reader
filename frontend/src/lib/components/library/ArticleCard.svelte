<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import * as Dialog from '$lib/components/ui/dialog';
	import type { ArticleMeta } from '$lib/types';
	import DeleteDialog from '$lib/components/library/DeleteDialog.svelte';
	import RawResponseDialog from '$lib/components/library/RawResponseDialog.svelte';
	import CoverageBadge from '$lib/components/CoverageBadge.svelte';
	import { enqueueRetry, enqueuePin } from '$lib/sync/engine';
	import { toast } from 'svelte-sonner';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import PinIcon from '@lucide/svelte/icons/pin';
	import EllipsisVerticalIcon from '@lucide/svelte/icons/ellipsis-vertical';
	import AlignLeftIcon from '@lucide/svelte/icons/align-left';

	interface Props {
		article: ArticleMeta;
		articleHref: string;
		isRead?: boolean;
		/** Reading-progress percentage [0,100]; 0 hides the progress bar. */
		progressPercent?: number;
	}

	let { article, articleHref, isRead = false, progressPercent = 0 }: Props = $props();

	let deleteOpen = $state(false);
	let rawOpen = $state(false);
	let summaryOpen = $state(false);

	const isFailed = $derived(
		article.status === 'fetch_failed' ||
			article.status === 'enrich_failed' ||
			article.status === 'blocked'
	);

	// A raw LLM response is only captured for enrichment-stage decode failures.
	const canViewRaw = $derived(article.status === 'enrich_failed');

	const statusVariant = $derived.by(() => {
		if (article.status === 'enriched') return 'default' as const;
		if (isFailed) return 'destructive' as const;
		return 'secondary' as const;
	});

	const statusLabel = $derived.by(() => {
		switch (article.status) {
			case 'queued':
				return 'Queued';
			case 'fetching':
				return 'Fetching…';
			case 'fetched':
				return 'Content fetched';
			case 'enriching':
				return 'Processing…';
			case 'enriched':
				return 'Ready';
			case 'fetch_failed':
				return 'Fetch failed';
			case 'enrich_failed':
				return 'Process failed';
			case 'blocked':
				return 'Blocked';
		}
	});

	const failedMessage = $derived.by(() => {
		switch (article.status) {
			case 'fetch_failed':
				return 'Could not fetch the original content.';
			case 'blocked':
				return 'The site returned a bot-verification / captcha page instead of the article.';
			default:
				return 'Could not process the article.';
		}
	});

	const safeSourceUrl = $derived(
		article.source_url && /^https?:\/\//i.test(article.source_url) ? article.source_url : null
	);

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleDateString(undefined, {
			month: 'short',
			day: 'numeric',
			year: 'numeric'
		});
	}

	async function handleRetry(e: MouseEvent) {
		e.stopPropagation();
		try {
			await enqueueRetry(article.id);
			toast('Retry queued.');
		} catch {
			toast.error('Failed to queue retry.');
		}
	}

	async function handleTogglePin(e: MouseEvent) {
		e.stopPropagation();
		const next = !article.pinned;
		try {
			await enqueuePin(article.id, next);
			toast(next ? 'Pinned to top.' : 'Unpinned.');
		} catch {
			toast.error('Failed to update pin.');
		}
	}

	// Show the reading-progress bar only for partially-read, not-yet-finished
	// articles (an unread article the user has started). Read articles show none.
	const showProgress = $derived(!isRead && progressPercent > 0 && progressPercent < 100);
</script>

<div class="border-border bg-card group relative rounded-xl border px-4 py-3">
	<div class="flex items-start justify-between gap-3">
		<div class="min-w-0 flex-1">
			{#if article.status === 'enriched'}
				<a
					href={articleHref}
					class="line-clamp-2 text-base font-medium leading-snug transition-colors
						{isRead ? 'text-muted-foreground hover:text-foreground' : 'hover:text-primary'}"
				>
					{article.title || 'Untitled'}
				</a>
			{:else}
				<span class="text-muted-foreground line-clamp-2 text-base font-medium leading-snug">
					{article.title || 'Untitled'}
				</span>
			{/if}
			{#if article.author}
				<p class="text-muted-foreground mt-0.5 truncate text-xs">{article.author}</p>
			{/if}
		</div>
		<div class="flex shrink-0 items-center gap-1">
			{#if article.status === 'enriched' && article.enrichment_coverage < 1}
				<CoverageBadge coverage={article.enrichment_coverage} />
			{/if}
			{#if article.status !== 'enriched'}
				<Badge variant={statusVariant} class="rounded-md">{statusLabel}</Badge>
			{/if}
			<Button
				size="icon-sm"
				variant="ghost"
				onclick={handleTogglePin}
				aria-label={article.pinned ? 'Unpin article' : 'Pin article'}
				title={article.pinned ? 'Unpin' : 'Pin to top'}
				class="transition-opacity
					{article.pinned
					? 'text-primary opacity-100'
					: 'text-muted-foreground opacity-0 group-hover:opacity-100'}"
			>
				<PinIcon class="size-3.5" fill={article.pinned ? 'currentColor' : 'none'} />
			</Button>
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button
							{...props}
							size="icon-sm"
							variant="ghost"
							onclick={(e: MouseEvent) => e.stopPropagation()}
							class="text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100"
							aria-label="Article actions"
						>
							<EllipsisVerticalIcon class="size-4" />
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end">
					{#if article.summary}
						<DropdownMenu.Item onclick={() => (summaryOpen = true)} class="gap-2">
							<AlignLeftIcon class="size-4" />
							Summary
						</DropdownMenu.Item>
						<DropdownMenu.Separator />
					{/if}
					<DropdownMenu.Item
						onclick={() => (deleteOpen = true)}
						class="text-destructive focus:text-destructive gap-2"
					>
						<TrashIcon class="size-4" />
						Delete
					</DropdownMenu.Item>
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		</div>
	</div>

	{#if isFailed}
		<div class="bg-destructive/10 text-destructive mt-2 rounded-md px-3 py-2 text-xs">
			{#if article.error}
				<span class="line-clamp-2">{article.error}</span>
			{:else}
				{failedMessage}
			{/if}
		</div>
	{/if}

	<div class="text-muted-foreground mt-1.5 flex items-center gap-2 text-xs">
		{#if article.source_domain && safeSourceUrl}
			<a
				href={safeSourceUrl}
				target="_blank"
				rel="noopener noreferrer"
				class="hover:text-foreground flex items-center gap-1 truncate transition-colors"
			>
				{article.source_domain}
				<ExternalLinkIcon class="size-3 shrink-0" />
			</a>
		{:else if article.source_domain}
			<span class="truncate">{article.source_domain}</span>
		{/if}
		<span class="shrink-0">{formatDate(article.created_at)}</span>

		{#if canViewRaw || isFailed}
			<div class="ml-auto flex items-center gap-1">
				{#if canViewRaw}
					<Button
						size="sm"
						variant="ghost"
						onclick={(e: MouseEvent) => {
							e.stopPropagation();
							rawOpen = true;
						}}
						class="h-6 text-xs"
						title="View the raw LLM response"
					>
						<FileTextIcon class="size-3" />
						Raw
					</Button>
				{/if}
				{#if isFailed}
					<Button size="sm" variant="secondary" onclick={handleRetry} class="h-6 text-xs">
						<RefreshCwIcon class="size-3" />
						Retry
					</Button>
				{/if}
			</div>
		{/if}
	</div>

	{#if showProgress}
		<!-- Reading-progress bar: how far the reader got before stopping. -->
		<div
			class="bg-muted mt-2.5 h-1 w-full overflow-hidden rounded-full"
			role="progressbar"
			aria-valuenow={progressPercent}
			aria-valuemin={0}
			aria-valuemax={100}
			aria-label="Reading progress"
		>
			<div class="bg-primary/60 h-full rounded-full" style="width: {progressPercent}%"></div>
		</div>
	{/if}
</div>

<DeleteDialog bind:open={deleteOpen} articleId={article.id} articleTitle={article.title} />

{#if canViewRaw}
	<RawResponseDialog bind:open={rawOpen} articleId={article.id} />
{/if}

{#if article.summary}
	<Dialog.Root bind:open={summaryOpen}>
		<Dialog.Content class="max-w-sm">
			<Dialog.Header>
				<Dialog.Title>Summary</Dialog.Title>
			</Dialog.Header>
			<p class="text-muted-foreground text-sm leading-relaxed">{article.summary}</p>
		</Dialog.Content>
	</Dialog.Root>
{/if}
