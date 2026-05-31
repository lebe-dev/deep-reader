<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Progress } from '$lib/components/ui/progress';
	import type { ArticleMeta, Progress as ProgressType } from '$lib/types';
	import DeleteDialog from '$lib/components/library/DeleteDialog.svelte';
	import CoverageBadge from '$lib/components/CoverageBadge.svelte';
	import { enqueueRetry } from '$lib/sync/engine';
	import { toast } from 'svelte-sonner';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';

	interface Props {
		article: ArticleMeta;
		progress?: ProgressType;
		onclick?: () => void;
	}

	let { article, progress, onclick }: Props = $props();

	let deleteOpen = $state(false);

	const isFailed = $derived(
		article.status === 'fetch_failed' || article.status === 'enrich_failed'
	);

	// "New": ready but never opened (no progress row yet).
	const isNew = $derived(article.status === 'enriched' && progress === undefined);

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

	// position is a token index; convert to 0–100 percentage using the article's token count.
	const progressPct = $derived.by(() => {
		const pos = progress?.position ?? 0;
		const total = article.token_count;
		if (!total) return 0;
		return Math.min(100, Math.round((pos / total) * 100));
	});

	// only allow http(s) source links to avoid javascript:/data: href injection
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

	function handleCardClick() {
		if (article.status === 'enriched') {
			onclick?.();
			return;
		}
		if (isFailed) {
			// do nothing on card click; user uses the Retry button
			return;
		}
		toast('Still processing — check back in a moment.');
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

	function handleDeleteClick(e: MouseEvent) {
		e.stopPropagation();
		deleteOpen = true;
	}
</script>

<Card.Root
	class="group relative cursor-pointer transition-shadow hover:shadow-md {article.status !==
	'enriched'
		? 'cursor-default'
		: ''}"
	onclick={handleCardClick}
	role="button"
	tabindex={0}
	onkeydown={(e) => {
		if (e.key === 'Enter' || e.key === ' ') handleCardClick();
	}}
>
	<Card.Header class="pb-2">
		<div class="flex items-start justify-between gap-2">
			<div class="min-w-0 flex-1">
				<Card.Title class="line-clamp-2 text-base leading-snug">
					{article.title || 'Untitled'}
				</Card.Title>
				{#if article.author}
					<Card.Description class="mt-0.5 truncate text-xs">
						{article.author}
					</Card.Description>
				{/if}
			</div>
			<div class="flex shrink-0 items-center gap-1">
				{#if isNew}
					<Badge variant="outline" class="border-primary text-primary rounded-md">New</Badge>
				{/if}
				{#if article.status === 'enriched'}
					<CoverageBadge coverage={article.enrichment_coverage} />
				{/if}
				<Badge variant={statusVariant} class="rounded-md">{statusLabel}</Badge>
			</div>
		</div>
	</Card.Header>

	<Card.Content class="pb-3">
		<div class="text-muted-foreground mb-3 flex items-center gap-2 text-xs">
			{#if article.source_domain && safeSourceUrl}
				<a
					href={safeSourceUrl}
					target="_blank"
					rel="noopener noreferrer"
					onclick={(e) => e.stopPropagation()}
					class="hover:text-foreground flex items-center gap-1 truncate transition-colors"
				>
					{article.source_domain}
					<ExternalLinkIcon class="size-3 shrink-0" />
				</a>
			{:else if article.source_domain}
				<span class="flex items-center gap-1 truncate">
					{article.source_domain}
					<ExternalLinkIcon class="size-3 shrink-0" />
				</span>
			{/if}
			<span class="shrink-0">{formatDate(article.created_at)}</span>
			{#if progress?.is_read}
				<span class="text-primary shrink-0 font-medium">Read</span>
			{/if}
		</div>

		{#if article.status === 'enriched'}
			<div class="space-y-1">
				<div class="flex justify-between text-xs">
					<span class="text-muted-foreground">Progress</span>
					<span class="text-muted-foreground">{Math.round(progressPct)}%</span>
				</div>
				<Progress value={progressPct} max={100} class="h-1" />
			</div>
		{/if}

		{#if isFailed}
			<div class="bg-destructive/10 text-destructive mt-2 rounded-md px-3 py-2 text-xs">
				{#if article.error}
					<span class="line-clamp-2">{article.error}</span>
				{:else}
					{failedMessage}
				{/if}
			</div>
		{/if}
	</Card.Content>

	<Card.Footer class="flex justify-end gap-2 pt-0">
		{#if isFailed}
			<Button size="sm" variant="secondary" onclick={handleRetry} class="h-7 text-xs">
				<RefreshCwIcon class="size-3" />
				Retry
			</Button>
		{/if}
		<Button
			size="icon-sm"
			variant="ghost"
			onclick={handleDeleteClick}
			aria-label="Delete article"
			class="text-muted-foreground hover:text-destructive"
		>
			<TrashIcon class="size-4" />
		</Button>
	</Card.Footer>
</Card.Root>

<DeleteDialog bind:open={deleteOpen} articleId={article.id} articleTitle={article.title} />
