<!-- Appearance settings card.
     Manages the reader typography presets — font size and line spacing — with a
     live preview rendered in the actual reader font. Writes optimistically via
     enqueueSettings (outbox → PATCH /api/settings), so the choice syncs across
     the user's devices like other settings.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { liveQuery } from 'dexie';
	import * as Card from '$lib/components/ui/card';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { db, getSyncState, SYNC_STATE_ID } from '$lib/db';
	import { enqueueSettings } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { Settings, FontSize, LineHeight } from '$lib/types';
	import {
		FONT_SIZE_OPTIONS,
		LINE_HEIGHT_OPTIONS,
		DEFAULT_FONT_SIZE,
		DEFAULT_LINE_HEIGHT,
		fontSizeRem,
		lineHeightMultiplier
	} from '$lib/reader-typography';
	import { readerFont, getReaderFontCss } from '$lib/reader-font.svelte';

	// ---------------------------------------------------------------------------
	// State — subscribe to the synced settings singleton.
	// ---------------------------------------------------------------------------

	let settings = $state<Settings | undefined>(undefined);

	onMount(() => {
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.settings) settings = state.settings;
			},
			error(err) {
				console.error('[settings] sync_state liveQuery error', err);
			}
		});
		return () => sub.unsubscribe();
	});

	// Effective values for the preview (fall back to defaults until settings load).
	const fontSize = $derived<FontSize>(settings?.font_size ?? DEFAULT_FONT_SIZE);
	const lineHeight = $derived<LineHeight>(settings?.line_height ?? DEFAULT_LINE_HEIGHT);

	// ---------------------------------------------------------------------------
	// Helpers
	// ---------------------------------------------------------------------------

	async function patchField(patch: Partial<Pick<Settings, 'font_size' | 'line_height'>>) {
		try {
			await enqueueSettings(patch);
			const state = await getSyncState();
			if (state.settings) settings = state.settings;
		} catch (err) {
			toast.error('Failed to save settings');
			console.error('[settings] appearance patch failed', err);
		}
	}

	function selectFontSize(value: FontSize) {
		if (!settings || value === settings.font_size) return;
		patchField({ font_size: value });
	}

	function selectLineHeight(value: LineHeight) {
		if (!settings || value === settings.line_height) return;
		patchField({ line_height: value });
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Appearance</Card.Title>
		<Card.Description
			>Tune how the article reader looks. Synced across your devices.</Card.Description
		>
	</Card.Header>

	<Card.Content class="space-y-5">
		{#if !settings && syncStatus.error}
			<p class="text-destructive text-sm">Couldn't load settings: {syncStatus.error}.</p>
			<p class="text-muted-foreground text-sm">
				Check your server URL and auth token on the <strong>Device</strong> tab, then sync.
			</p>
		{:else if !settings}
			<p class="text-muted-foreground text-sm">Loading settings…</p>
		{:else}
			<!-- Font size -->
			<div class="grid gap-1.5">
				<Label>Font size</Label>
				<div class="flex gap-2">
					{#each FONT_SIZE_OPTIONS as opt (opt.value)}
						<Button
							variant={fontSize === opt.value ? 'default' : 'outline'}
							class="flex-1"
							aria-pressed={fontSize === opt.value}
							onclick={() => selectFontSize(opt.value)}
						>
							{opt.label}
						</Button>
					{/each}
				</div>
				<p class="text-muted-foreground text-xs">Text size used in the article reader.</p>
			</div>

			<!-- Line spacing -->
			<div class="grid gap-1.5">
				<Label>Line spacing</Label>
				<div class="flex gap-2">
					{#each LINE_HEIGHT_OPTIONS as opt (opt.value)}
						<Button
							variant={lineHeight === opt.value ? 'default' : 'outline'}
							class="flex-1"
							aria-pressed={lineHeight === opt.value}
							onclick={() => selectLineHeight(opt.value)}
						>
							{opt.label}
						</Button>
					{/each}
				</div>
				<p class="text-muted-foreground text-xs">Vertical space between lines of text.</p>
			</div>

			<!-- Live preview -->
			<div class="grid gap-1.5">
				<Label>Preview</Label>
				<div
					class="bg-muted/40 rounded-lg border p-4"
					style="font-family: {getReaderFontCss(readerFont.value)}; font-size: {fontSizeRem(
						fontSize
					)}; line-height: {lineHeightMultiplier(lineHeight)};"
				>
					The quick brown fox jumps over the lazy dog. Reading should feel comfortable — neither
					cramped nor sprawling — so your eyes glide from line to line without effort.
				</div>
			</div>
		{/if}
	</Card.Content>
</Card.Root>
