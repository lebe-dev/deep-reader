<!-- Reusable editor for a single LLM stage prompt (summary, enrichment, …).
     Encapsulates the draft/dirty/reset logic so each stage prompt behaves the
     same: pre-filled with the saved value or the server default, "Save" persists
     the override (an exact match of the default is stored as "" so the server
     keeps tracking its built-in default), and "Reset to default" clears it.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Button } from '$lib/components/ui/button';

	type Props = {
		id: string;
		label: string;
		/** The currently saved override ("" means the server uses its default). */
		saved: string;
		/** The built-in default template the editor pre-fills / resets to. */
		defaultValue: string;
		/** Persist the override value ("" = clear the override). */
		onSave: (value: string) => void;
		/** Clear the override (revert to the server default). */
		onReset: () => void;
		rows?: number;
		/** Help text rendered under the textarea (placeholders, hints). */
		help?: Snippet;
	};

	let { id, label, saved, defaultValue, onSave, onReset, rows = 12, help }: Props = $props();

	// Local draft so typing isn't clobbered by upstream (liveQuery) echoes.
	let draft = $state('');
	let touched = $state(false);

	// Seed the draft with the saved override, or the default template when none is
	// set — so the textarea is always populated. Re-seeds on upstream changes
	// until the user starts editing.
	$effect(() => {
		if (!touched) draft = saved || defaultValue;
	});

	// An exact match of the default is stored as "" so the server keeps tracking
	// its built-in default rather than a frozen copy.
	const valueToStore = $derived(draft === defaultValue ? '' : draft);
	const dirty = $derived(saved !== valueToStore);
	// Already on the default (nothing to reset) when no override is saved and the
	// draft still equals the default template.
	const isDefault = $derived(saved === '' && draft === defaultValue);

	function handleInput(raw: string) {
		touched = true;
		draft = raw;
	}

	function save() {
		if (!dirty) return;
		onSave(valueToStore);
		touched = false;
	}

	function reset() {
		draft = defaultValue;
		touched = false;
		if (saved !== '') onReset();
	}
</script>

<div class="grid gap-1.5">
	<Label for={id}>{label}</Label>
	<Textarea
		{id}
		{rows}
		class="font-mono text-xs"
		value={draft}
		oninput={(e) => handleInput(e.currentTarget.value)}
	/>
	{#if help}
		<p class="text-muted-foreground text-xs">{@render help()}</p>
	{/if}
	<div class="flex gap-2 pt-1">
		<Button size="sm" onclick={save} disabled={!dirty}>Save</Button>
		<Button size="sm" variant="outline" onclick={reset} disabled={isDefault}
			>Reset to default</Button
		>
	</div>
</div>
