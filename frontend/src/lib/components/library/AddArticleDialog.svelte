<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { enqueueAddArticle } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import { toast } from 'svelte-sonner';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	// open is bindable so parent can control it, or the internal trigger button opens it.
	let open = $state(false);

	// markdown.new daily budget (undefined until first sync, or when disabled).
	const budget = $derived(syncStatus.markdownBudget);
	const budgetActive = $derived(!!budget?.enabled && budget.daily_limit > 0);
	const articlesLeft = $derived(budget?.articles_remaining ?? 0);
	const budgetExhausted = $derived(budgetActive && articlesLeft <= 0);

	let url = $state('');
	let submitting = $state(false);
	let validationError = $state('');

	function validateUrl(value: string): string {
		const trimmed = value.trim();
		if (!trimmed) return 'URL is required.';
		try {
			const parsed = new URL(trimmed);
			if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
				return 'Only http:// and https:// URLs are allowed.';
			}
		} catch {
			return 'Please enter a valid URL.';
		}
		return '';
	}

	function handleInput() {
		if (validationError) {
			validationError = validateUrl(url);
		}
	}

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		const trimmed = url.trim();
		validationError = validateUrl(trimmed);
		if (validationError) return;

		submitting = true;
		try {
			await enqueueAddArticle(trimmed);
			toast('Article added — syncing in the background.');
			url = '';
			open = false;
		} catch {
			toast.error('Failed to queue article. Try again.');
		} finally {
			submitting = false;
		}
	}

	function handleOpenChange(value: boolean) {
		if (!value) {
			url = '';
			validationError = '';
		}
		open = value;
	}
</script>

<!-- Trigger button -->
<Button onclick={() => handleOpenChange(true)} class="gap-2">
	<PlusIcon class="size-4" />
	Add article
</Button>

<Dialog.Root {open} onOpenChange={handleOpenChange}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Add article</Dialog.Title>
			<Dialog.Description>
				Paste a URL to add an article to your library. It will be processed in the background.
			</Dialog.Description>
		</Dialog.Header>

		<form onsubmit={handleSubmit} class="mt-2 space-y-3">
			<div class="space-y-1.5">
				<Input
					type="url"
					placeholder="https://example.com/article"
					bind:value={url}
					oninput={handleInput}
					disabled={submitting}
					aria-invalid={!!validationError || undefined}
					aria-describedby={validationError ? 'url-error' : undefined}
					autofocus
				/>
				{#if validationError}
					<p id="url-error" class="text-destructive text-xs">{validationError}</p>
				{/if}
			</div>

			{#if budgetActive}
				{#if budgetExhausted}
					<div
						class="bg-muted text-muted-foreground flex items-start gap-2 rounded-md p-2.5 text-xs"
						role="status"
					>
						<TriangleAlertIcon class="text-amber-500 mt-0.5 size-3.5 shrink-0" />
						<span>
							markdown.new daily limit reached ({budget!.daily_limit} units). New articles will use the
							built-in extractor until it resets (UTC midnight).
						</span>
					</div>
				{:else}
					<div class="text-muted-foreground flex items-center gap-1.5 text-xs">
						<SparklesIcon class="size-3.5 shrink-0" />
						<span>
							~{articlesLeft}
							{articlesLeft === 1 ? 'conversion' : 'conversions'} left today via markdown.new
							<span class="text-muted-foreground/60"
								>({budget!.units_remaining}/{budget!.daily_limit} units)</span
							>
						</span>
					</div>
				{/if}
			{/if}

			<Dialog.Footer class="flex justify-end gap-2">
				<Button
					type="button"
					variant="outline"
					onclick={() => handleOpenChange(false)}
					disabled={submitting}
				>
					Cancel
				</Button>
				<Button type="submit" disabled={submitting || !url.trim()}>
					{#if submitting}
						<Loader2Icon class="size-4 animate-spin" />
					{/if}
					Add
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
