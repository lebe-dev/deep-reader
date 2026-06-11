<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { getArticleRaw, OfflineError } from '$lib/api';
	import type { ArticleRaw } from '$lib/types';
	import { toast } from 'svelte-sonner';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import CopyIcon from '@lucide/svelte/icons/copy';

	interface Props {
		open: boolean;
		articleId: string;
	}

	let { open = $bindable(), articleId }: Props = $props();

	type LoadState = 'loading' | 'ready' | 'error';
	let loadState = $state<LoadState>('loading');
	let data: ArticleRaw | undefined = $state();
	let errorMessage: string | undefined = $state();

	// Fetch the raw response each time the dialog opens (it is not synced offline).
	// An AbortController guards against stale results: rapidly closing and
	// re-opening (or switching articleId) aborts the prior request so a late
	// resolution can't clobber the current view. AbortErrors are ignored.
	$effect(() => {
		if (!open) return;
		const id = articleId;
		const controller = new AbortController();
		loadState = 'loading';
		data = undefined;
		errorMessage = undefined;
		getArticleRaw(id, controller.signal)
			.then((res) => {
				if (controller.signal.aborted) return;
				data = res;
				loadState = 'ready';
			})
			.catch((err) => {
				if (controller.signal.aborted) return;
				errorMessage =
					err instanceof OfflineError
						? 'You are offline — the raw response is only available online.'
						: err instanceof Error
							? err.message
							: String(err);
				loadState = 'error';
			});
		return () => controller.abort();
	});

	const hasRaw = $derived(loadState === 'ready' && !!data?.raw);

	async function handleCopy() {
		if (!data?.raw) return;
		try {
			await navigator.clipboard.writeText(data.raw);
			toast('Copied to clipboard.');
		} catch {
			toast.error('Failed to copy.');
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="flex max-h-[85vh] max-w-2xl flex-col">
		<Dialog.Header>
			<Dialog.Title>Raw LLM response</Dialog.Title>
			<Dialog.Description>
				The verbatim model output that could not be processed.
			</Dialog.Description>
		</Dialog.Header>

		{#if loadState === 'loading'}
			<div class="text-muted-foreground flex items-center gap-2 py-8 text-sm">
				<Loader2Icon class="size-4 animate-spin" />
				Loading…
			</div>
		{:else if loadState === 'error'}
			<p class="text-destructive py-8 text-sm">{errorMessage}</p>
		{:else if data}
			{#if data.error}
				<div class="bg-destructive/10 text-destructive shrink-0 rounded-md px-3 py-2 text-xs">
					{data.error}
				</div>
			{/if}
			{#if hasRaw}
				<pre
					class="bg-muted min-h-0 flex-1 overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre-wrap break-words">{data.raw}</pre>
			{:else}
				<p class="text-muted-foreground py-8 text-sm">
					No raw response was captured for this failure.
				</p>
			{/if}
		{/if}

		<Dialog.Footer class="mt-2 flex shrink-0 justify-end gap-2">
			{#if hasRaw}
				<Button variant="outline" onclick={handleCopy}>
					<CopyIcon class="size-4" />
					Copy
				</Button>
			{/if}
			<Button onclick={() => (open = false)}>Close</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
