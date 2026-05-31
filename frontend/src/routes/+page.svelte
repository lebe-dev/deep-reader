<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { liveQuery } from 'dexie';
	import { db } from '$lib/db';
	import type { ArticleMeta, Progress } from '$lib/types';
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
	let progressMap = $state<Map<string, Progress>>(new Map());
	let initialLoading = $state(true);

	onMount(() => {
		// Subscribe to live articles sorted by created_at descending.
		const articlesSub = liveQuery(async () => {
			const all = await db.articles_meta.orderBy('created_at').reverse().toArray();
			return all;
		}).subscribe({
			next(value) {
				articles = value;
				initialLoading = false;
			},
			error(err) {
				console.error('[library] articles liveQuery error', err);
				initialLoading = false;
			}
		});

		// Subscribe to progress rows.
		const progressSub = liveQuery(async () => {
			const rows = await db.progress.toArray();
			return rows;
		}).subscribe({
			next(rows) {
				const m = new Map<string, Progress>();
				for (const row of rows) {
					m.set(row.article_id, row);
				}
				progressMap = m;
			},
			error(err) {
				console.error('[library] progress liveQuery error', err);
			}
		});

		return () => {
			articlesSub.unsubscribe();
			progressSub.unsubscribe();
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

	// ---------------------------------------------------------------------------
	// Navigation
	// ---------------------------------------------------------------------------

	function openArticle(id: string) {
		goto(`/article/${id}`);
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

	<!-- Loading skeletons on first sync -->
	{#if initialLoading}
		<div class="space-y-3">
			{#each { length: 4 } as _, i (i)}
				<div class="bg-card rounded-xl border p-4 space-y-3">
					<div class="flex items-start justify-between gap-2">
						<div class="flex-1 space-y-2">
							<Skeleton class="h-4 w-3/4" />
							<Skeleton class="h-3 w-1/3" />
						</div>
						<Skeleton class="h-5 w-16 rounded-full" />
					</div>
					<Skeleton class="h-3 w-1/2" />
					<Skeleton class="h-1.5 w-full rounded-full" />
				</div>
			{/each}
		</div>

		<!-- Empty state -->
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

		<!-- Article list -->
	{:else}
		<div class="space-y-3">
			{#each articles as article (article.id)}
				<ArticleCard
					{article}
					progress={progressMap.get(article.id)}
					onclick={() => openArticle(article.id)}
				/>
			{/each}
		</div>
	{/if}
</div>
