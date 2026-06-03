<script lang="ts">
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import type { ArticleMeta } from '$lib/types';
	import DeleteDialog from '$lib/components/library/DeleteDialog.svelte';
	import CoverageBadge from '$lib/components/CoverageBadge.svelte';
	import { enqueueRetry } from '$lib/sync/engine';
	import { toast } from 'svelte-sonner';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

	interface Props {
		article: ArticleMeta;
		articleHref: string;
		isRead?: boolean;
	}

	let { article, articleHref, isRead = false }: Props = $props();

	let deleteOpen = $state(false);

	const isFailed = $derived(
		article.status === 'fetch_failed' || article.status === 'enrich_failed'
	);

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
		}
	});

	const failedMessage = $derived(
		article.status === 'fetch_failed'
			? 'Could not fetch the original content.'
			: 'Could not process the article.'
	);

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
</script>

<div class="border-border bg-card rounded-xl border px-4 py-3">
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

		<div class="ml-auto flex items-center gap-1">
			{#if isFailed}
				<Button size="sm" variant="secondary" onclick={handleRetry} class="h-6 text-xs">
					<RefreshCwIcon class="size-3" />
					Retry
				</Button>
			{/if}
			<Button
				size="icon-sm"
				variant="ghost"
				onclick={() => (deleteOpen = true)}
				aria-label="Delete article"
				class="text-muted-foreground hover:text-destructive"
			>
				<TrashIcon class="size-4" />
			</Button>
		</div>
	</div>
</div>

<DeleteDialog bind:open={deleteOpen} articleId={article.id} articleTitle={article.title} />
