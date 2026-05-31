<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { enqueueDelete } from '$lib/sync/engine';
	import { toast } from 'svelte-sonner';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';

	interface Props {
		open: boolean;
		articleId: string;
		articleTitle: string;
	}

	let { open = $bindable(), articleId, articleTitle }: Props = $props();

	let deleting = $state(false);

	async function handleConfirm() {
		deleting = true;
		try {
			await enqueueDelete(articleId);
			toast('Article deleted.');
			open = false;
		} catch {
			toast.error('Failed to delete article.');
		} finally {
			deleting = false;
		}
	}

	function handleCancel() {
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>Delete article?</Dialog.Title>
			<Dialog.Description>
				<span class="line-clamp-2 font-medium">"{articleTitle || 'Untitled'}"</span>
				will be removed from your library. This action cannot be undone.
			</Dialog.Description>
		</Dialog.Header>
		<Dialog.Footer class="mt-4 flex justify-end gap-2">
			<Button variant="outline" onclick={handleCancel} disabled={deleting}>Cancel</Button>
			<Button variant="destructive" onclick={handleConfirm} disabled={deleting}>
				{#if deleting}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				Delete
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
