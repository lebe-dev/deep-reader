<script lang="ts">
	// SentenceSheet — bottom Sheet for sentence translation.
	// Triggered by long-press, shift-click, or text selection.

	import * as Sheet from '$lib/components/ui/sheet';
	import type { SentenceSheetContent } from './reader-utils';

	interface Props {
		content: SentenceSheetContent | null;
		onclose: () => void;
	}

	let { content, onclose }: Props = $props();

	let open = $derived(content !== null);
</script>

<Sheet.Root
	{open}
	onOpenChange={(v) => {
		if (!v) onclose();
	}}
>
	<Sheet.Content side="bottom" class="max-h-[60vh] px-0 pb-safe">
		<Sheet.Header class="px-6 pb-2">
			<Sheet.Title class="text-base">Sentence translation</Sheet.Title>
		</Sheet.Header>

		{#if content}
			<div class="flex flex-col gap-4 overflow-y-auto px-6 pb-6">
				<blockquote
					class="border-muted text-muted-foreground border-l-2 pl-4 text-sm leading-relaxed italic"
				>
					{content.original}
				</blockquote>
				<p class="text-sm leading-relaxed">{content.translation}</p>
			</div>
		{/if}
	</Sheet.Content>
</Sheet.Root>
