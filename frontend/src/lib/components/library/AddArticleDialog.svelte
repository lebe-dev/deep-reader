<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { enqueueAddArticle, enqueueAddArticleText } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import { toast } from 'svelte-sonner';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import LinkIcon from '@lucide/svelte/icons/link';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	// open is bindable so parent can control it, or the internal trigger button opens it.
	let open = $state(false);
	// Which input mode the dialog is showing: a URL to fetch, or raw pasted text.
	let mode = $state<'url' | 'text'>('url');

	// markdown.new daily budget (undefined until first sync, or when disabled).
	const budget = $derived(syncStatus.markdownBudget);
	const budgetActive = $derived(!!budget?.enabled && budget.daily_limit > 0);
	const articlesLeft = $derived(budget?.articles_remaining ?? 0);
	const budgetExhausted = $derived(budgetActive && articlesLeft <= 0);

	let url = $state('');
	let text = $state('');
	let title = $state('');
	// Optional link back to the original article, stored as metadata (text mode).
	let sourceUrl = $state('');
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

	// validateTextMode returns the first problem with the text-mode fields: the
	// pasted text is required, the source URL is optional but must be valid when
	// present.
	function validateTextMode(): string {
		if (!text.trim()) return 'Text is required.';
		if (sourceUrl.trim()) return validateUrl(sourceUrl);
		return '';
	}

	function handleInput() {
		if (validationError) {
			validationError = mode === 'url' ? validateUrl(url) : validateTextMode();
		}
	}

	function openWith(next: 'url' | 'text') {
		mode = next;
		validationError = '';
		open = true;
	}

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();

		if (mode === 'url') {
			const trimmed = url.trim();
			validationError = validateUrl(trimmed);
			if (validationError) return;

			submitting = true;
			try {
				await enqueueAddArticle(trimmed);
				toast('Article added — syncing in the background.');
				resetFields();
				open = false;
			} catch {
				toast.error('Failed to queue article. Try again.');
			} finally {
				submitting = false;
			}
			return;
		}

		validationError = validateTextMode();
		if (validationError) return;
		const trimmedText = text.trim();

		submitting = true;
		try {
			await enqueueAddArticleText(trimmedText, title.trim(), sourceUrl.trim());
			toast('Text added — syncing in the background.');
			resetFields();
			open = false;
		} catch {
			toast.error('Failed to queue text. Try again.');
		} finally {
			submitting = false;
		}
	}

	function resetFields() {
		url = '';
		text = '';
		title = '';
		sourceUrl = '';
	}

	function handleOpenChange(value: boolean) {
		if (!value) {
			resetFields();
			validationError = '';
		}
		open = value;
	}

	const canSubmit = $derived(mode === 'url' ? !!url.trim() : !!text.trim());
</script>

<!-- Split trigger: primary action adds by URL, the dropdown offers "Add text". -->
<div class="flex">
	<Button onclick={() => openWith('url')} class="gap-2 rounded-r-none">
		<PlusIcon class="size-4" />
		Add article
	</Button>
	<DropdownMenu.Root>
		<DropdownMenu.Trigger>
			{#snippet child({ props })}
				<Button
					{...props}
					class="rounded-l-none border-l border-l-primary-foreground/20 px-2"
					aria-label="More add options"
				>
					<ChevronDownIcon class="size-4" />
				</Button>
			{/snippet}
		</DropdownMenu.Trigger>
		<DropdownMenu.Content align="end">
			<DropdownMenu.Item onSelect={() => openWith('url')}>
				<LinkIcon class="size-4" />
				Add article
			</DropdownMenu.Item>
			<DropdownMenu.Item onSelect={() => openWith('text')}>
				<FileTextIcon class="size-4" />
				Add text
			</DropdownMenu.Item>
		</DropdownMenu.Content>
	</DropdownMenu.Root>
</div>

<Dialog.Root {open} onOpenChange={handleOpenChange}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{mode === 'url' ? 'Add article' : 'Add text'}</Dialog.Title>
			<Dialog.Description>
				{#if mode === 'url'}
					Paste a URL to add an article to your library. It will be processed in the background.
				{:else}
					Paste the raw text of an article. It will be processed in the background — no fetching.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<form onsubmit={handleSubmit} class="flex flex-col gap-4">
			{#if mode === 'url'}
				<div class="space-y-1.5">
					<Input
						type="url"
						placeholder="https://example.com/article"
						bind:value={url}
						oninput={handleInput}
						disabled={submitting}
						aria-invalid={!!validationError || undefined}
						aria-describedby={validationError ? 'add-error' : undefined}
						autofocus
					/>
					{#if validationError}
						<p id="add-error" class="text-destructive text-xs">{validationError}</p>
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
								markdown.new daily limit reached ({budget!.daily_limit} units). New articles will use
								the built-in extractor until it resets (UTC midnight).
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
			{:else}
				<div class="space-y-3">
					<Input
						type="text"
						placeholder="Title (optional)"
						bind:value={title}
						disabled={submitting}
						autofocus
					/>
					<Input
						type="url"
						placeholder="Original article URL (optional)"
						bind:value={sourceUrl}
						oninput={handleInput}
						disabled={submitting}
					/>
					<Textarea
						placeholder="Paste the article text here…"
						bind:value={text}
						oninput={handleInput}
						disabled={submitting}
						aria-invalid={!!validationError || undefined}
						aria-describedby={validationError ? 'add-error' : undefined}
						class="max-h-[50vh] min-h-40 resize-none"
					/>
					{#if validationError}
						<p id="add-error" class="text-destructive text-xs">{validationError}</p>
					{/if}
				</div>
			{/if}

			<Dialog.Footer class="flex shrink-0 justify-end gap-2">
				<Button
					type="button"
					variant="outline"
					onclick={() => handleOpenChange(false)}
					disabled={submitting}
				>
					Cancel
				</Button>
				<Button type="submit" disabled={submitting || !canSubmit}>
					{#if submitting}
						<Loader2Icon class="size-4 animate-spin" />
					{/if}
					Add
				</Button>
			</Dialog.Footer>
		</form>
	</Dialog.Content>
</Dialog.Root>
