<!-- Publish dialog — share an article's translated text under a public link.
     Pre-fills the page title and description from the article (title, summary)
     and lets the user edit them before publishing; they become the page's
     <title> and its Open Graph metadata for link previews.

     The dialog is also the management surface for an existing link: it shows
     when the link expires, copies it, and revokes it.
-->
<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { toast } from 'svelte-sonner';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import GlobeIcon from '@lucide/svelte/icons/globe';
	import { getPublication, publishArticle, unpublishArticle } from '$lib/api';
	import { formatExpiry } from '$lib/publish-utils';
	import type { Publication } from '$lib/types';

	interface Props {
		open: boolean;
		articleId: string;
		articleTitle: string;
		articleSummary?: string;
		/** Called after publish/unpublish so the caller can refresh its own state. */
		onChange?: (publication: Publication | null) => void;
	}

	let {
		open = $bindable(),
		articleId,
		articleTitle,
		articleSummary = '',
		onChange
	}: Props = $props();

	let publication = $state<Publication | null>(null);
	let title = $state('');
	let description = $state('');
	let loading = $state(false);
	let busy = $state(false);

	const expiry = $derived(formatExpiry(publication?.expires_at));

	// Load the article's current link each time the dialog opens, so a link that
	// expired or was revoked on another device is not shown as live here.
	$effect(() => {
		if (!open) return;

		title = articleTitle;
		description = articleSummary;
		loading = true;

		let cancelled = false;
		getPublication(articleId)
			.then((found) => {
				if (cancelled) return;
				publication = found;
				if (found) {
					title = found.title;
					description = found.description;
				}
			})
			.catch((err) => {
				if (cancelled) return;
				console.error('[publish] could not load the current link', err);
				toast.error("Couldn't check whether this article is published.");
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});

		return () => {
			cancelled = true;
		};
	});

	async function handlePublish() {
		if (!title.trim()) {
			toast.error('A title is required.');
			return;
		}

		busy = true;
		try {
			publication = await publishArticle(articleId, {
				title: title.trim(),
				description: description.trim()
			});
			onChange?.(publication);
			await copyLink();
		} catch (err) {
			console.error('[publish] failed', err);
			toast.error('Failed to publish the article.');
		} finally {
			busy = false;
		}
	}

	async function handleUnpublish() {
		busy = true;
		try {
			await unpublishArticle(articleId);
			publication = null;
			onChange?.(null);
			toast('The link no longer works.');
		} catch (err) {
			console.error('[publish] unpublish failed', err);
			toast.error('Failed to revoke the link.');
		} finally {
			busy = false;
		}
	}

	async function copyLink() {
		const url = publication?.url;
		if (!url) return;

		try {
			await navigator.clipboard.writeText(url);
			toast('Link copied to the clipboard.');
		} catch {
			// Clipboard access can be denied or unavailable (insecure origin, some
			// in-app webviews). The link is on screen and selectable either way.
			toast('Published. Copy the link below.');
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{publication ? 'Public link' : 'Publish article'}</Dialog.Title>
			<Dialog.Description>
				{#if publication}
					Anyone with this link can read the translated text.
				{:else}
					Publishes the translated text under an unguessable link. The title and description below
					are what link previews show.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		{#if loading}
			<p class="text-muted-foreground py-4 text-sm">Loading…</p>
		{:else if publication}
			<div class="space-y-3 py-2">
				<div class="grid gap-1.5">
					<Label for="publish-url">Link</Label>
					<div class="flex gap-2">
						<Input id="publish-url" readonly value={publication.url} class="font-mono text-xs" />
						<Button variant="outline" size="icon" onclick={copyLink} title="Copy link">
							<CopyIcon class="size-4" />
						</Button>
					</div>
				</div>
				<p class="text-muted-foreground text-xs">
					{expiry ?? 'This link never expires.'}
					{#if expiry}
						Publishing again mints a new link with a fresh lifetime.
					{/if}
				</p>
			</div>

			<Dialog.Footer class="mt-2 flex flex-wrap justify-end gap-2">
				<Button variant="outline" onclick={() => (open = false)} disabled={busy}>Close</Button>
				<Button variant="destructive" onclick={handleUnpublish} disabled={busy}>
					{#if busy}
						<Loader2Icon class="size-4 animate-spin" />
					{/if}
					Unpublish
				</Button>
				<Button onclick={handlePublish} disabled={busy}>Republish</Button>
			</Dialog.Footer>
		{:else}
			<div class="space-y-3 py-2">
				<div class="grid gap-1.5">
					<Label for="publish-title">Title</Label>
					<Input id="publish-title" bind:value={title} maxlength={300} />
				</div>
				<div class="grid gap-1.5">
					<Label for="publish-description">Description</Label>
					<Textarea id="publish-description" bind:value={description} rows={3} maxlength={1000} />
					<p class="text-muted-foreground text-xs">
						Shown under the title in link previews (Open Graph).
					</p>
				</div>
			</div>

			<Dialog.Footer class="mt-2 flex justify-end gap-2">
				<Button variant="outline" onclick={() => (open = false)} disabled={busy}>Cancel</Button>
				<Button onclick={handlePublish} disabled={busy}>
					{#if busy}
						<Loader2Icon class="size-4 animate-spin" />
					{:else}
						<GlobeIcon class="size-4" />
					{/if}
					Publish
				</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
