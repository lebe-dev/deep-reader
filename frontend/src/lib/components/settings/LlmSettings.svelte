<!-- LLM settings card.
     Manages: chunk size and the per-stage system-prompt templates
     (normalization, summary, enrichment). All sync to the server via enqueueSettings
     (outbox → PATCH /api/settings). An empty value falls back to the server
     default (built-in prompt templates).

     The model is NOT configured here — it comes from the active LLM provider
     profile (see LlmProvidersSettings / Settings > "LLM providers").
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { liveQuery } from 'dexie';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Separator } from '$lib/components/ui/separator';
	import PromptEditor from '$lib/components/settings/PromptEditor.svelte';
	import { db, getSyncState, SYNC_STATE_ID } from '$lib/db';
	import { enqueueSettings } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { Settings, SettingsPatch, ServerInfo } from '$lib/types';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let settings = $state<Settings | undefined>(undefined);
	let serverInfo = $state<ServerInfo | undefined>(undefined);

	// ---------------------------------------------------------------------------
	// Lifecycle — subscribe to the sync_state singleton (settings + serverInfo).
	// ---------------------------------------------------------------------------

	onMount(() => {
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.serverInfo) serverInfo = state.serverInfo;
				if (state?.settings) settings = state.settings;
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

	async function patchField(patch: SettingsPatch) {
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

	function handleChunkTokensChange(raw: string) {
		if (!settings) return;
		const trimmed = raw.trim();
		// Empty field = revert to the server default (stored as 0).
		const next = trimmed === '' ? 0 : Math.trunc(Number(trimmed));
		if (Number.isNaN(next)) {
			toast.error('Chunk size must be a number');
			return;
		}
		// 0 (default) is allowed; any other value must be within the backend bounds.
		if (next !== 0 && (next < 50 || next > 2000)) {
			toast.error('Chunk size must be 0 (default) or between 50 and 2000');
			return;
		}
		if (next === settings.chunk_tokens) return;
		patchField({ chunk_tokens: next });
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>LLM</Card.Title>
		<Card.Description>
			Configure the chunk size and the per-stage prompts used to annotate articles. The model comes
			from the active provider profile above.
		</Card.Description>
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
			<!-- Chunk size (step-wise enrichment window) -->
			<div class="grid gap-1.5">
				<Label for="chunk-tokens-input">Chunk size (tokens)</Label>
				<Input
					id="chunk-tokens-input"
					type="number"
					min="50"
					max="2000"
					placeholder={String(serverInfo?.llm_chunk_tokens ?? 500)}
					value={settings.chunk_tokens === 0 ? '' : settings.chunk_tokens}
					onchange={(e) => handleChunkTokensChange(e.currentTarget.value)}
				/>
				<p class="text-muted-foreground text-xs">
					How many tokens each article is annotated in per LLM call. Smaller chunks make each
					request shorter and more reliable on weaker models, at the cost of more requests. Clear
					the field to keep the server default
					{#if serverInfo?.llm_chunk_tokens}(<code>{serverInfo.llm_chunk_tokens}</code>){/if}.
					Allowed: 50–2000.
				</p>
			</div>

			<Separator />

			<!-- Normalization prompt (fetch stage: strips nav / chrome / boilerplate) -->
			<PromptEditor
				id="normalize-prompt"
				label="Normalization prompt"
				rows={12}
				saved={settings.normalize_prompt}
				defaultValue={serverInfo?.normalize_prompt_default ?? ''}
				onSave={(value) => patchField({ normalize_prompt: value })}
				onReset={() => patchField({ normalize_prompt: '' })}
			>
				{#snippet help()}
					System prompt for the content-normalization step, which runs right after an article is
					fetched (before tokenization) to strip leftover navigation, reader chrome, subscribe
					banners, author bios, comment sections and prev/next-story links the extractor leaked into
					the body. Pre-filled with the default template — edit it to customise. Supported
					placeholder: <code>{'{{target_language}}'}</code>.
				{/snippet}
			</PromptEditor>

			<Separator />

			<!-- Summary prompt (step 1 of the step-wise enrichment) -->
			<PromptEditor
				id="summary-prompt"
				label="Summary prompt"
				rows={8}
				saved={settings.summary_prompt}
				defaultValue={serverInfo?.summary_prompt_default ?? ''}
				onSave={(value) => patchField({ summary_prompt: value })}
				onReset={() => patchField({ summary_prompt: '' })}
			>
				{#snippet help()}
					System prompt for the summary step. Pre-filled with the default template — edit it to
					customise. Supported placeholder: <code>{'{{target_language}}'}</code>.
				{/snippet}
			</PromptEditor>

			<Separator />

			<!-- Enrichment prompt (per-chunk translation) -->
			<PromptEditor
				id="enrichment-prompt"
				label="Enrichment prompt"
				rows={14}
				saved={settings.enrichment_prompt}
				defaultValue={serverInfo?.enrichment_prompt_default ?? ''}
				onSave={(value) => patchField({ enrichment_prompt: value })}
				onReset={() => patchField({ enrichment_prompt: '' })}
			>
				{#snippet help()}
					System prompt for article enrichment. Pre-filled with the default template — edit it to
					customise. Supported placeholders:
					<code>{'{{target_language}}'}</code>, <code>{'{{cefr_level}}'}</code>,
					<code>{'{{min_difficulty}}'}</code>, <code>{'{{enrichment_version}}'}</code>.
				{/snippet}
			</PromptEditor>

			<p class="text-muted-foreground text-xs">
				Changes apply to newly enriched articles. Use “Retry” on an article to re-enrich it with the
				new prompts.
			</p>
		{/if}
	</Card.Content>
</Card.Root>
