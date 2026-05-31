<script lang="ts">
	import { pwaState, applyUpdate, dismissUpdate } from '$lib/pwa/state.svelte';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import XIcon from '@lucide/svelte/icons/x';
</script>

{#if pwaState.updateAvailable}
	<div
		class="bg-primary text-primary-foreground flex items-center justify-between gap-3 px-4 py-2 text-sm"
		role="status"
		aria-live="polite"
	>
		<span class="flex items-center gap-2">
			<RefreshCwIcon class="size-4 shrink-0" />
			Доступно обновление приложения
		</span>
		<div class="flex items-center gap-2">
			<button
				onclick={applyUpdate}
				class="bg-primary-foreground text-primary hover:bg-primary-foreground/90 rounded-md px-3 py-1 font-medium transition-colors"
			>
				Обновить
			</button>
			<button
				onclick={dismissUpdate}
				aria-label="Закрыть"
				class="hover:bg-primary-foreground/15 rounded-md p-1 transition-colors"
			>
				<XIcon class="size-4" />
			</button>
		</div>
	</div>
{/if}
