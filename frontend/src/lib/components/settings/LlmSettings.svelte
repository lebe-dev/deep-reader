<!-- LLM settings card.
     Manages: LLM model override and the enrichment system-prompt template.
     Both sync to the server via enqueueSettings (outbox → PATCH /api/settings).
     An empty value falls back to the server default (.env LLM_MODEL / built-in
     enrichment prompt template).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { liveQuery } from 'dexie';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { db, getSyncState, SYNC_STATE_ID } from '$lib/db';
	import { enqueueSettings } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { Settings, ServerInfo } from '$lib/types';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let settings = $state<Settings | undefined>(undefined);
	let serverInfo = $state<ServerInfo | undefined>(undefined);

	// Local draft for the prompt textarea so typing isn't clobbered by liveQuery
	// echoes. Initialised from settings / server default on first load.
	let promptDraft = $state('');
	let promptTouched = $state(false);

	// ---------------------------------------------------------------------------
	// Derived
	// ---------------------------------------------------------------------------

	// The built-in default template, shown as the textarea value when the user
	// has no custom prompt of their own.
	const defaultPrompt = $derived(serverInfo?.enrichment_prompt_default ?? '');

	// The value to persist: an exact match of the default is stored as "" so the
	// server keeps tracking its built-in default rather than a frozen copy.
	const promptToStore = $derived(promptDraft === defaultPrompt ? '' : promptDraft);

	// There are unsaved changes when the value-to-store differs from what's saved.
	const promptDirty = $derived((settings?.enrichment_prompt ?? '') !== promptToStore);

	// Already on the default (nothing to reset) when the saved value is empty and
	// the draft still equals the default template.
	const isDefaultPrompt = $derived(
		(settings?.enrichment_prompt ?? '') === '' && promptDraft === defaultPrompt
	);

	// ---------------------------------------------------------------------------
	// Lifecycle — subscribe to the sync_state singleton (settings + serverInfo).
	// ---------------------------------------------------------------------------

	onMount(() => {
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.serverInfo) serverInfo = state.serverInfo;
				if (!state?.settings) return;
				settings = state.settings;
				// Seed the draft with the saved prompt, or the default template when
				// none is set — so the textarea is always populated, never blank.
				// Re-seeds on later syncs until the user starts editing.
				if (!promptTouched) {
					promptDraft =
						state.settings.enrichment_prompt || (state.serverInfo?.enrichment_prompt_default ?? '');
				}
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

	async function patchField(patch: Partial<Settings>) {
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

	function handleModelChange(raw: string) {
		if (!settings) return;
		const next = raw.trim();
		// The backend rejects an empty llm_model; only patch when non-empty and
		// actually changed. To revert to the server default the user clears the
		// field — we simply don't send anything in that case.
		if (next === '' || next === settings.llm_model) return;
		patchField({ llm_model: next });
	}

	function handlePromptInput(raw: string) {
		promptTouched = true;
		promptDraft = raw;
	}

	function savePrompt() {
		if (!settings || !promptDirty) return;
		patchField({ enrichment_prompt: promptToStore });
		promptTouched = false;
	}

	function resetPrompt() {
		// Restore the built-in default into the editor and clear the saved
		// override (empty = the server keeps using its default template).
		promptDraft = defaultPrompt;
		promptTouched = false;
		if (settings && settings.enrichment_prompt !== '') {
			patchField({ enrichment_prompt: '' });
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>LLM</Card.Title>
		<Card.Description
			>Configure the model and enrichment prompt used to annotate articles.</Card.Description
		>
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
			<!-- LLM model override -->
			<div class="grid gap-1.5">
				<Label for="llm-model-input">LLM model</Label>
				<Input
					id="llm-model-input"
					type="text"
					placeholder={serverInfo?.llm_model || 'gpt-4o-mini'}
					value={settings.llm_model}
					onchange={(e) => handleModelChange(e.currentTarget.value)}
				/>
				<p class="text-muted-foreground text-xs">
					Model name as expected by your provider (OpenAI, OpenRouter, Ollama…). Clear the field to
					keep the server default
					{#if serverInfo?.llm_model}(<code>{serverInfo.llm_model}</code>){/if}.
				</p>
			</div>

			<!-- Enrichment prompt template -->
			<div class="grid gap-1.5">
				<Label for="enrichment-prompt">Enrichment prompt</Label>
				<Textarea
					id="enrichment-prompt"
					rows={14}
					class="font-mono text-xs"
					value={promptDraft}
					oninput={(e) => handlePromptInput(e.currentTarget.value)}
				/>
				<p class="text-muted-foreground text-xs">
					System prompt for article enrichment. Pre-filled with the default template — edit it to
					customise. Supported placeholders:
					<code>{'{{target_language}}'}</code>, <code>{'{{cefr_level}}'}</code>,
					<code>{'{{min_difficulty}}'}</code>, <code>{'{{enrichment_version}}'}</code>.
				</p>
				<div class="flex gap-2 pt-1">
					<Button size="sm" onclick={savePrompt} disabled={!promptDirty}>Save prompt</Button>
					<Button size="sm" variant="outline" onclick={resetPrompt} disabled={isDefaultPrompt}>
						Reset to default
					</Button>
				</div>
				<p class="text-muted-foreground text-xs">
					Changes apply to newly enriched articles. Use “Retry” on an article to re-enrich it with
					the new prompt.
				</p>
			</div>
		{/if}
	</Card.Content>
</Card.Root>
