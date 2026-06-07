<!-- LLM connection profiles (Settings > LLM, backend-only).
     Manages the named provider profiles {name, base_url, api_key, model} and which
     one is active. Unlike the rest of settings these do NOT go through the offline
     outbox: they call the backend directly (GET/POST/PATCH/DELETE /api/llm-providers)
     and the section is gated on connectivity, so the secret API key never lands in
     the client IndexedDB. The API key is write-only: the server returns only a masked
     preview, and leaving the key field blank on edit keeps the stored secret. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import * as Card from '$lib/components/ui/card';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import {
		listLLMProviders,
		createLLMProvider,
		updateLLMProvider,
		deleteLLMProvider,
		activateLLMProvider,
		OfflineError
	} from '$lib/api';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { LLMProviderView, LLMProviderInput } from '$lib/types';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import PlusIcon from '@lucide/svelte/icons/plus';

	// State -------------------------------------------------------------------
	let providers = $state<LLMProviderView[]>([]);
	let loading = $state(true);
	let loadError = $state<string | undefined>(undefined);
	let busyId = $state<string | undefined>(undefined);

	// Edit/create dialog
	let formOpen = $state(false);
	let editing = $state<LLMProviderView | undefined>(undefined);
	let form = $state<{
		name: string;
		base_url: string;
		model: string;
		api_key: string;
		force_json_object: boolean;
	}>({
		name: '',
		base_url: '',
		model: '',
		api_key: '',
		force_json_object: false
	});
	let saving = $state(false);

	// Delete confirmation
	let deleteTarget = $state<LLMProviderView | undefined>(undefined);
	let deleting = $state(false);

	const online = $derived(syncStatus.online);

	onMount(load);

	async function load() {
		loading = true;
		loadError = undefined;
		try {
			providers = await listLLMProviders();
		} catch (err) {
			if (err instanceof OfflineError) {
				loadError = 'offline';
			} else {
				loadError = err instanceof Error ? err.message : String(err);
			}
		} finally {
			loading = false;
		}
	}

	function openCreate() {
		editing = undefined;
		form = { name: '', base_url: '', model: '', api_key: '', force_json_object: false };
		formOpen = true;
	}

	function openEdit(p: LLMProviderView) {
		editing = p;
		// api_key starts blank: blank = keep the stored secret.
		form = {
			name: p.name,
			base_url: p.base_url,
			model: p.model,
			api_key: '',
			force_json_object: p.force_json_object
		};
		formOpen = true;
	}

	async function handleSave() {
		const name = form.name.trim();
		const base_url = form.base_url.trim();
		const model = form.model.trim();
		if (!name || !base_url || !model) {
			toast.error('Name, base URL and model are required');
			return;
		}
		if (!/^https?:\/\//.test(base_url)) {
			toast.error('Base URL must start with http:// or https://');
			return;
		}

		const input: LLMProviderInput = {
			name,
			base_url,
			model,
			force_json_object: form.force_json_object
		};
		const key = form.api_key.trim();
		if (editing) {
			// Blank key on edit = keep stored secret (omit the field).
			if (key) input.api_key = key;
		} else {
			input.api_key = key; // may be empty for keyless local providers
		}

		saving = true;
		try {
			if (editing) {
				await updateLLMProvider(editing.id, input);
				toast('Provider updated');
			} else {
				await createLLMProvider(input);
				toast('Provider added');
			}
			formOpen = false;
			await load();
		} catch (err) {
			toast.error('Failed to save provider');
			console.error('[llm-providers] save failed', err);
		} finally {
			saving = false;
		}
	}

	async function handleActivate(p: LLMProviderView) {
		if (p.is_active) return;
		busyId = p.id;
		try {
			await activateLLMProvider(p.id);
			await load();
			toast(`“${p.name}” is now active`);
		} catch (err) {
			toast.error('Failed to activate provider');
			console.error('[llm-providers] activate failed', err);
		} finally {
			busyId = undefined;
		}
	}

	async function handleDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await deleteLLMProvider(deleteTarget.id);
			deleteTarget = undefined;
			await load();
			toast('Provider deleted');
		} catch (err) {
			toast.error('Failed to delete provider');
			console.error('[llm-providers] delete failed', err);
		} finally {
			deleting = false;
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>LLM providers</Card.Title>
		<Card.Description>
			Manage connection profiles (base URL, API key and model) and pick the active one. Available
			only when connected to the server — these settings are stored on the backend, not on this
			device.
		</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-4">
		{#if !online}
			<p class="text-muted-foreground text-sm">
				You're offline. Connect to the server to manage LLM providers.
			</p>
		{:else if loading}
			<p class="text-muted-foreground text-sm">Loading providers…</p>
		{:else if loadError === 'offline'}
			<p class="text-muted-foreground text-sm">
				Couldn't reach the server. Check your connection and try again.
			</p>
			<Button variant="outline" size="sm" onclick={load}>Retry</Button>
		{:else if loadError}
			<p class="text-destructive text-sm">Couldn't load providers: {loadError}.</p>
			<Button variant="outline" size="sm" onclick={load}>Retry</Button>
		{:else}
			{#if providers.length === 0}
				<p class="text-muted-foreground text-sm">
					No providers configured yet. Add one to start enriching articles.
				</p>
			{:else}
				<ul class="space-y-2">
					{#each providers as p (p.id)}
						<li class="rounded-lg border p-3">
							<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
								<div class="min-w-0">
									<div class="flex items-center gap-2">
										<span class="truncate font-medium">{p.name}</span>
										{#if p.is_active}
											<Badge variant="default" class="rounded-sm">Active</Badge>
										{/if}
									</div>
									<p class="text-muted-foreground truncate text-xs">{p.base_url}</p>
									<p class="text-muted-foreground truncate text-xs">
										model: <code>{p.model}</code>
										{#if p.has_key}
											· key: <code>{p.key_preview}</code>
										{:else}
											· <span class="italic">no key</span>
										{/if}
										{#if p.force_json_object}
											· <code>json_object</code>
										{/if}
									</p>
								</div>
								<div class="flex shrink-0 flex-wrap items-center gap-1.5">
									{#if !p.is_active}
										<Button
											variant="outline"
											size="sm"
											disabled={busyId === p.id}
											onclick={() => handleActivate(p)}
										>
											{#if busyId === p.id}
												<Loader2Icon class="size-4 animate-spin" />
											{/if}
											Activate
										</Button>
									{/if}
									<Button variant="outline" size="sm" onclick={() => openEdit(p)}>Edit</Button>
									<Button variant="ghost" size="sm" onclick={() => (deleteTarget = p)}>
										Delete
									</Button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			{/if}

			<Button variant="outline" size="sm" onclick={openCreate}>
				<PlusIcon class="size-4" />
				Add provider
			</Button>
		{/if}
	</Card.Content>
</Card.Root>

<!-- Create / edit dialog -->
<Dialog.Root bind:open={formOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{editing ? 'Edit provider' : 'Add provider'}</Dialog.Title>
			<Dialog.Description>
				An OpenAI-compatible endpoint (OpenAI, OpenRouter, Ollama, …).
			</Dialog.Description>
		</Dialog.Header>

		<div class="grid gap-3 py-2">
			<div class="grid gap-1.5">
				<Label for="prov-name">Name</Label>
				<Input id="prov-name" bind:value={form.name} placeholder="OpenRouter" />
			</div>
			<div class="grid gap-1.5">
				<div class="flex items-center gap-2">
					<Label for="prov-base">Base URL</Label>
					<button
						type="button"
						class="text-muted-foreground hover:text-foreground cursor-pointer text-xs underline transition-colors"
						onclick={() => (form.base_url = 'https://openrouter.ai/api/v1')}
					>
						OpenRouter
					</button>
				</div>
				<Input
					id="prov-base"
					bind:value={form.base_url}
					placeholder="https://openrouter.ai/api/v1"
				/>
			</div>
			<div class="grid gap-1.5">
				<Label for="prov-model">Model</Label>
				<Input id="prov-model" bind:value={form.model} placeholder="openai/gpt-4o-mini" />
			</div>
			<div class="grid gap-1.5">
				<Label for="prov-key">API key</Label>
				<Input
					id="prov-key"
					type="password"
					bind:value={form.api_key}
					placeholder={editing
						? editing.has_key
							? `${editing.key_preview} — leave blank to keep`
							: 'no key set'
						: 'sk-…'}
				/>
				{#if editing}
					<p class="text-muted-foreground text-xs">
						Leave blank to keep the stored key. The current key is never shown.
					</p>
				{/if}
			</div>
			<div class="flex items-start justify-between gap-3 rounded-lg border p-3">
				<div class="grid gap-0.5">
					<Label for="prov-force-json">Force <code>json_object</code></Label>
					<p class="text-muted-foreground text-xs">
						Skip <code>json_schema</code> and request <code>json_object</code> directly. Enable for providers
						that reject structured outputs (e.g. an OpenRouter model whose data policy leaves no matching
						endpoint).
					</p>
				</div>
				<Switch id="prov-force-json" bind:checked={form.force_json_object} />
			</div>
		</div>

		<Dialog.Footer class="mt-2 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (formOpen = false)} disabled={saving}>Cancel</Button>
			<Button onclick={handleSave} disabled={saving}>
				{#if saving}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				{editing ? 'Save' : 'Add'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<!-- Delete confirmation -->
<Dialog.Root open={!!deleteTarget} onOpenChange={(v) => !v && (deleteTarget = undefined)}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title>Delete provider?</Dialog.Title>
			<Dialog.Description>
				<span class="font-medium">“{deleteTarget?.name}”</span> will be removed. If it's the active profile,
				another one is promoted automatically.
			</Dialog.Description>
		</Dialog.Header>
		<Dialog.Footer class="mt-4 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (deleteTarget = undefined)} disabled={deleting}>
				Cancel
			</Button>
			<Button variant="destructive" onclick={handleDelete} disabled={deleting}>
				{#if deleting}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				Delete
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
