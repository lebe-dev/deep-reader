<!-- Reading / language settings card.
     Manages: CEFR level, min difficulty to highlight, reader font.
     Writes optimistically via enqueueSettings (outbox → PATCH /api/settings).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { liveQuery } from 'dexie';
	import { Select as SelectPrimitive } from 'bits-ui';
	import * as Card from '$lib/components/ui/card';
	import * as Select from '$lib/components/ui/select';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { db, getSyncState, SYNC_STATE_ID } from '$lib/db';
	import { enqueueSettings } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { CefrLevel, Settings } from '$lib/types';
	import {
		readerFont,
		setReaderFont,
		READER_FONT_OPTIONS,
		type ReaderFont
	} from '$lib/reader-font.svelte';

	// ---------------------------------------------------------------------------
	// Constants
	// ---------------------------------------------------------------------------

	const CEFR_LEVELS: { value: CefrLevel; label: string }[] = [
		{ value: 'A2', label: 'A2 — Elementary' },
		{ value: 'B1', label: 'B1 — Intermediate' },
		{ value: 'B2', label: 'B2 — Upper-Intermediate' },
		{ value: 'C1', label: 'C1 — Advanced' },
		{ value: 'C2', label: 'C2 — Proficient' }
	];

	function cefrIndex(level: CefrLevel | undefined): number {
		return CEFR_LEVELS.findIndex((l) => l.value === level);
	}

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let settings = $state<Settings | undefined>(undefined);

	// ---------------------------------------------------------------------------
	// Lifecycle — subscribe to the sync_state singleton so settings appear as
	// soon as the boot sync populates them (no race on first load).
	// ---------------------------------------------------------------------------

	onMount(() => {
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (!state?.settings) return;
				settings = state.settings;
			},
			error(err) {
				console.error('[settings] sync_state liveQuery error', err);
			}
		});

		return () => sub.unsubscribe();
	});

	// ---------------------------------------------------------------------------
	// Helpers
	// ---------------------------------------------------------------------------

	type PatchableField = Pick<
		Settings,
		'cefr_level' | 'min_difficulty_to_highlight' | 'markdown_warn_threshold'
	>;

	async function patchField(patch: Partial<PatchableField>) {
		try {
			await enqueueSettings(patch);
			const state = await getSyncState();
			if (state.settings) settings = state.settings;
			toast('Settings saved');
		} catch (err) {
			toast.error('Failed to save settings');
			console.error('[settings] patch failed', err);
		}
	}

	function handleCefrChange(value: string | undefined) {
		if (!value || !settings) return;
		patchField({ cefr_level: value as CefrLevel });
	}

	function handleFontChange(value: string | undefined) {
		if (!value) return;
		setReaderFont(value as ReaderFont);
	}

	function handleMinDifficultyChange(value: string | undefined) {
		if (!value || !settings) return;
		patchField({ min_difficulty_to_highlight: value as CefrLevel });
	}

	function handleWarnThresholdChange(raw: string) {
		if (!settings) return;
		const n = Number.parseInt(raw, 10);
		if (Number.isNaN(n) || n < 0 || n > 100) return;
		if (n === settings.markdown_warn_threshold) return;
		patchField({ markdown_warn_threshold: n });
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Reading</Card.Title>
		<Card.Description>Configure your language level and enrichment preferences.</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-5">
		{#if !settings && syncStatus.error}
			<p class="text-destructive text-sm">
				Couldn't load settings: {syncStatus.error}.
			</p>
			<p class="text-muted-foreground text-sm">
				Check your server URL and auth token on the <strong>Device</strong> tab, then sync.
			</p>
		{:else if !settings}
			<p class="text-muted-foreground text-sm">Loading settings…</p>
		{:else}
			<!-- Reader font -->
			<div class="grid gap-1.5">
				<Label for="font-select">Reader font</Label>
				<Select.Root type="single" value={readerFont.value} onValueChange={handleFontChange}>
					<Select.Trigger id="font-select" class="w-full">
						<SelectPrimitive.Value placeholder="Select font" />
					</Select.Trigger>
					<Select.Content>
						{#each READER_FONT_OPTIONS as opt (opt.value)}
							<Select.Item value={opt.value} label={opt.label} />
						{/each}
					</Select.Content>
				</Select.Root>
				<p class="text-muted-foreground text-xs">
					Font used in the article reader. Stored locally on this device.
				</p>
			</div>

			<!-- CEFR level -->
			<div class="grid gap-1.5">
				<Label for="cefr-select">Your English level (CEFR)</Label>
				<Select.Root type="single" value={settings.cefr_level} onValueChange={handleCefrChange}>
					<Select.Trigger id="cefr-select" class="w-full">
						<SelectPrimitive.Value placeholder="Select level" />
					</Select.Trigger>
					<Select.Content>
						{#each CEFR_LEVELS as lvl (lvl.value)}
							<Select.Item value={lvl.value} label={lvl.label} />
						{/each}
					</Select.Content>
				</Select.Root>
				<p class="text-muted-foreground text-xs">
					Words above this level will be highlighted for translation.
				</p>
			</div>

			<!-- Min difficulty to highlight -->
			<div class="grid gap-1.5">
				<Label for="min-diff-select">Minimum difficulty to highlight</Label>
				<Select.Root
					type="single"
					value={settings.min_difficulty_to_highlight}
					onValueChange={handleMinDifficultyChange}
				>
					<Select.Trigger id="min-diff-select" class="w-full">
						<SelectPrimitive.Value placeholder="Select level" />
					</Select.Trigger>
					<Select.Content>
						{#each CEFR_LEVELS as lvl (lvl.value)}
							<Select.Item
								value={lvl.value}
								label={lvl.label}
								disabled={cefrIndex(lvl.value) < cefrIndex(settings.cefr_level)}
							/>
						{/each}
					</Select.Content>
				</Select.Root>
				<p class="text-muted-foreground text-xs">
					Usually your CEFR level + 1. Words at or below this level are shown without markup.
				</p>
			</div>

			<!-- markdown.new low-budget warning threshold -->
			<div class="grid gap-1.5">
				<Label for="warn-threshold-input">markdown.new warning threshold</Label>
				<Input
					id="warn-threshold-input"
					type="number"
					min="0"
					max="100"
					value={settings.markdown_warn_threshold}
					onchange={(e) => handleWarnThresholdChange(e.currentTarget.value)}
				/>
				<p class="text-muted-foreground text-xs">
					Show a banner when ≤ this many markdown.new conversions remain today. 0 turns the warning
					off.
				</p>
			</div>
		{/if}
	</Card.Content>
</Card.Root>
