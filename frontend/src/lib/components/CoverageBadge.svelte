<script lang="ts">
	import { coverageDisplay } from '$lib/utils';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	interface Props {
		/** Enrichment coverage fraction in [0,1]. */
		coverage: number;
		/** Append the word "enriched" after the percentage (used in the article header). */
		showLabel?: boolean;
	}

	let { coverage, showLabel = false }: Props = $props();

	const display = $derived(coverageDisplay(coverage));
</script>

<span
	class="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-xs font-medium tabular-nums {display.low
		? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
		: 'text-muted-foreground'}"
	title={display.low
		? `Only ${display.pct}% of the text was enriched — the rest may be untranslated.`
		: 'Text fully enriched.'}
>
	{#if display.low}
		<TriangleAlertIcon class="size-3" />
	{:else}
		<SparklesIcon class="size-3" />
	{/if}
	{display.pct}%{showLabel ? ' enriched' : ''}
</span>
