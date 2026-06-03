<script lang="ts">
	import { onMount } from 'svelte';
	import { liveQuery } from 'dexie';
	import { db } from '$lib/db';
	import type { ArticleMeta } from '$lib/types';
	import { sync } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import { ArticleCard, AddArticleDialog } from '$lib/components/library';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { toast } from 'svelte-sonner';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import WifiOffIcon from '@lucide/svelte/icons/wifi-off';

	// ---------------------------------------------------------------------------
	// Reactive data from IndexedDB via Dexie liveQuery
	// ---------------------------------------------------------------------------

	let articles = $state<ArticleMeta[]>([]);
	let readSet = $state<Set<string>>(new Set());
	let initialLoading = $state(true);

	onMount(() => {
		const articlesSub = liveQuery(async () => {
			const [all, progress] = await Promise.all([
				db.articles_meta.orderBy('created_at').reverse().toArray(),
				db.progress.toArray()
			]);
			const read = new Set(progress.filter((p) => p.is_read).map((p) => p.article_id));
			const sorted = [...all].sort((a, b) => {
				const aRead = read.has(a.id) ? 1 : 0;
				const bRead = read.has(b.id) ? 1 : 0;
				return aRead - bRead;
			});
			return { sorted, read };
		}).subscribe({
			next({ sorted, read }) {
				articles = sorted;
				readSet = read;
				initialLoading = false;
			},
			error(err) {
				console.error('[library] articles liveQuery error', err);
				initialLoading = false;
			}
		});

		return () => {
			articlesSub.unsubscribe();
		};
	});

	// ---------------------------------------------------------------------------
	// Sync
	// ---------------------------------------------------------------------------

	let syncing = $state(false);

	async function handleSync() {
		if (syncing) return;
		syncing = true;
		try {
			await sync();
			toast('Library synced.');
		} catch {
			toast.error('Sync failed — check your connection.');
		} finally {
			syncing = false;
		}
	}
</script>

<div class="space-y-4">
	<!-- Header row: title + actions -->
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div class="flex items-center gap-2">
			<h1 class="text-xl font-semibold">Library</h1>
			<!-- Sync status indicator -->
			<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
				{#if !syncStatus.online}
					<WifiOffIcon class="size-3.5" />
					<span>Offline</span>
				{:else if syncStatus.pending > 0}
					<span class="bg-primary size-1.5 rounded-full"></span>
					<span>{syncStatus.pending} pending</span>
				{:else if syncStatus.lastSyncedAt}
					<span class="text-muted-foreground/70">Synced</span>
				{/if}
			</div>
		</div>

		<div class="flex items-center gap-2">
			<Button
				variant="ghost"
				size="icon-sm"
				onclick={handleSync}
				disabled={syncing || !syncStatus.online}
				aria-label="Sync now"
				title="Sync now"
			>
				<RefreshCwIcon class="size-4 {syncing ? 'animate-spin' : ''}" />
			</Button>
			<AddArticleDialog />
		</div>
	</div>

	{#if initialLoading}
		<div class="space-y-2">
			{#each { length: 4 } as _, i (i)}
				<div class="bg-card rounded-xl border px-4 py-3 space-y-2">
					<div class="flex items-start justify-between gap-2">
						<Skeleton class="h-4 w-3/4" />
						<Skeleton class="h-5 w-16 rounded-full" />
					</div>
					<Skeleton class="h-3 w-1/3" />
				</div>
			{/each}
		</div>
	{:else if articles.length === 0}
		<div class="flex flex-col items-center justify-center gap-4 py-20 text-center">
			<div class="bg-muted rounded-full p-4">
				<BookOpenIcon class="text-muted-foreground size-8" />
			</div>
			<div class="space-y-1">
				<p class="font-medium">Your library is empty</p>
				<p class="text-muted-foreground text-sm">Add an article URL to get started.</p>
			</div>
			<AddArticleDialog />
		</div>
	{:else}
		<div class="space-y-2">
			{#each articles as article (article.id)}
				<ArticleCard {article} articleHref="/article/{article.id}" isRead={readSet.has(article.id)} />
			{/each}
		</div>
	{/if}
</div>
