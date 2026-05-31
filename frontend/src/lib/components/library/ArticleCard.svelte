<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Progress } from '$lib/components/ui/progress';
	import type { ArticleMeta, Progress as ProgressType } from '$lib/types';
	import DeleteDialog from '$lib/components/library/DeleteDialog.svelte';
	import { enqueueReenrich } from '$lib/sync/engine';
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

	const statusVariant = $derived.by(() => {
		if (article.status === 'enriched') return 'default' as const;
		if (article.status === 'failed') return 'destructive' as const;
		return 'secondary' as const;
	});

	const statusLabel = $derived.by(() => {
		if (article.status === 'enriched') return 'Ready';
		if (article.status === 'failed') return 'Failed';
		return 'Processing';
	});

	// position is a token index; convert to 0–100 percentage using the article's token count.
	const progressPct = $derived.by(() => {
		const pos = progress?.position ?? 0;
		const total = article.token_count;
		if (!total) return 0;
		return Math.min(100, Math.round((pos / total) * 100));
	});

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
		if (article.status === 'pending') {
			toast.info('Still processing — check back in a moment.');
			return;
		}
		// failed — do nothing on card click; user uses the Re-enrich button
	}

	async function handleReenrich(e: MouseEvent) {
		e.stopPropagation();
		try {
			await enqueueReenrich(article.id);
			toast.success('Re-enrichment queued.');
		} catch {
			toast.error('Failed to queue re-enrichment.');
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
				<Badge variant={statusVariant} class="rounded-md">{statusLabel}</Badge>
			</div>
		</div>
	</Card.Header>

	<Card.Content class="pb-3">
		<div class="text-muted-foreground mb-3 flex items-center gap-2 text-xs">
			{#if article.source_domain}
				<span class="flex items-center gap-1 truncate">
					<ExternalLinkIcon class="size-3 shrink-0" />
					{article.source_domain}
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

		{#if article.status === 'failed'}
			<div class="bg-destructive/10 text-destructive mt-2 rounded-md px-3 py-2 text-xs">
				{#if article.error}
					<span class="line-clamp-2">{article.error}</span>
				{:else}
					Enrichment failed.
				{/if}
			</div>
		{/if}
	</Card.Content>

	<Card.Footer class="flex justify-end gap-2 pt-0">
		{#if article.status === 'failed'}
			<Button size="sm" variant="secondary" onclick={handleReenrich} class="h-7 text-xs">
				<RefreshCwIcon class="size-3" />
				Re-enrich
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
