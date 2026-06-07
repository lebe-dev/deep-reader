<script lang="ts">
	// Full-screen image viewer for reader images. Opens over the article with a
	// dimmed backdrop; closes on Escape, backdrop click, or the close button.

	import { onDestroy } from 'svelte';
	import { fade } from 'svelte/transition';
	import XIcon from '@lucide/svelte/icons/x';

	interface Props {
		/** Currently zoomed image, or null when the lightbox is closed. */
		image: { url: string; alt: string } | null;
		onclose: () => void;
	}

	let { image, onclose }: Props = $props();

	function handleKeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') onclose();
	}

	$effect(() => {
		if (!image) return;
		window.addEventListener('keydown', handleKeydown);
		// Prevent the page behind the lightbox from scrolling.
		const previousOverflow = document.body.style.overflow;
		document.body.style.overflow = 'hidden';
		return () => {
			window.removeEventListener('keydown', handleKeydown);
			document.body.style.overflow = previousOverflow;
		};
	});

	onDestroy(() => {
		document.body.style.overflow = '';
	});
</script>

{#if image}
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4 backdrop-blur-sm"
		onclick={onclose}
		role="dialog"
		aria-modal="true"
		aria-label={image.alt || 'Image'}
		tabindex="-1"
		transition:fade={{ duration: 120 }}
	>
		<button
			type="button"
			class="absolute top-4 right-4 rounded-full bg-white/10 p-2 text-white transition-colors hover:bg-white/20"
			onclick={onclose}
			aria-label="Close image"
		>
			<XIcon class="size-5" />
		</button>
		<!-- Stop propagation so clicking the image itself doesn't close. -->
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<img
			src={image.url}
			alt={image.alt}
			class="max-h-[90vh] max-w-[90vw] rounded object-contain shadow-2xl"
			onclick={(e) => e.stopPropagation()}
		/>
	</div>
{/if}
