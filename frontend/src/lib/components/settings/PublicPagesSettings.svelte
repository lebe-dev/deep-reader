<!-- Public pages settings card.
     Manages: how long a published article link stays reachable (TTL, in hours).
     The TTL is read when an article is published and stamped into that link, so
     editing it here never shortens or extends links already shared.
     Writes optimistically via enqueueSettings (outbox → PATCH /api/settings).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { liveQuery } from 'dexie';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { db, getSyncState, SYNC_STATE_ID } from '$lib/db';
	import { enqueueSettings } from '$lib/sync/engine';
	import { syncStatus } from '$lib/sync/store.svelte';
	import { formatTtl, MAX_TTL_HOURS, MIN_TTL_HOURS } from '$lib/publish-utils';
	import type { Settings } from '$lib/types';

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

	async function handleTtlChange(raw: string) {
		if (!settings) return;

		const hours = Number.parseInt(raw, 10);
		if (Number.isNaN(hours) || hours < 0 || hours > MAX_TTL_HOURS) {
			toast.error(
				`Lifetime must be 0 (never expires) or between ${MIN_TTL_HOURS} and ${MAX_TTL_HOURS} hours`
			);
			return;
		}
		if (hours === settings.public_page_ttl_hours) return;

		try {
			await enqueueSettings({ public_page_ttl_hours: hours });
			const state = await getSyncState();
			if (state.settings) settings = state.settings;
			toast('Settings saved');
		} catch (err) {
			toast.error('Failed to save settings');
			console.error('[settings] public page TTL patch failed', err);
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Public Pages</Card.Title>
		<Card.Description>
			Share an article's translated text under a link that anyone can open — and that stops working
			on its own.
		</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-5">
		{#if !settings && syncStatus.error}
			<p class="text-destructive text-sm">Couldn't load settings: {syncStatus.error}.</p>
		{:else if !settings}
			<p class="text-muted-foreground text-sm">Loading settings…</p>
		{:else}
			<div class="grid gap-1.5">
				<Label for="public-ttl-input">Link lifetime (hours)</Label>
				<Input
					id="public-ttl-input"
					type="number"
					min="0"
					max={MAX_TTL_HOURS}
					value={settings.public_page_ttl_hours}
					onchange={(e) => handleTtlChange(e.currentTarget.value)}
				/>
				<p class="text-muted-foreground text-xs">
					{#if settings.public_page_ttl_hours > 0}
						New links expire after {formatTtl(settings.public_page_ttl_hours)} and then answer “page not
						found”.
					{:else}
						New links never expire — they stay reachable until you unpublish the article.
					{/if}
					Changing this only affects articles you publish from now on; links you have already shared keep
					the lifetime they were created with.
				</p>
			</div>

			<p class="text-muted-foreground text-xs">
				A published page carries only the translated text, its title and description — no reading
				progress, vocabulary or account data. The link is unguessable and never indexed, but anyone
				who has it can open the page, so treat it as public.
			</p>
		{/if}
	</Card.Content>
</Card.Root>
