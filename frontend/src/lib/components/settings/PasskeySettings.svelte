<!-- Passkeys (Settings > Security, backend-only).
     Lists the WebAuthn credentials registered against the single built-in account
     and lets the user add, rename and revoke them. Like the LLM providers these do
     NOT go through the offline outbox: the ceremonies are live challenge/response
     with the server, so the whole section is gated on connectivity.

     The password is never disabled — a passkey is an additional way in, so losing
     every device can't lock the account out. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import * as Card from '$lib/components/ui/card';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { deletePasskey, listPasskeys, renamePasskey, ApiError, OfflineError } from '$lib/api';
	import { authState } from '$lib/auth/store.svelte';
	import { registerPasskey, PasskeyAbortedError } from '$lib/auth/passkey';
	import { initPasskeys, isPasskeyCancellation } from '$lib/platform/passkey';
	import { captureError } from '$lib/sentry';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { PasskeyView } from '$lib/types';
	import Loader2Icon from '@lucide/svelte/icons/loader-2';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';

	const MAX_NAME_LEN = 64;

	let passkeys = $state<PasskeyView[]>([]);
	let loading = $state(true);
	let loadError = $state<string | undefined>(undefined);
	let busyId = $state<string | undefined>(undefined);
	let deviceSupported = $state(false);

	// Register dialog
	let registerOpen = $state(false);
	let registerName = $state('');
	let registering = $state(false);

	// Rename dialog
	let renameTarget = $state<PasskeyView | undefined>(undefined);
	let renameValue = $state('');
	let renaming = $state(false);

	// Delete confirmation
	let deleteTarget = $state<PasskeyView | undefined>(undefined);
	let deleting = $state(false);

	const online = $derived(syncStatus.online);
	const serverSupported = $derived(authState.passkeyEnabled);

	onMount(() => {
		void initPasskeys().then((ok) => (deviceSupported = ok));
		void load();
	});

	async function load() {
		if (!authState.passkeyEnabled) {
			loading = false;
			return;
		}
		loading = true;
		loadError = undefined;
		try {
			passkeys = await listPasskeys();
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

	function openRegister() {
		// A sensible default so the common case is one tap: the user can still
		// rename afterwards, and an unnamed list of passkeys is useless.
		registerName = suggestName();
		registerOpen = true;
	}

	/** Suggest a device-flavoured label, falling back to a numbered passkey. */
	function suggestName(): string {
		const ua = typeof navigator === 'undefined' ? '' : navigator.userAgent;
		for (const [pattern, label] of [
			[/iPhone/i, 'iPhone'],
			[/iPad/i, 'iPad'],
			[/Android/i, 'Android phone'],
			[/Macintosh|Mac OS X/i, 'Mac'],
			[/Windows/i, 'Windows PC'],
			[/Linux/i, 'Linux PC']
		] as const) {
			if (pattern.test(ua)) return label;
		}
		return `Passkey ${passkeys.length + 1}`;
	}

	async function handleRegister() {
		const name = registerName.trim();
		if (!name) return;

		registering = true;
		try {
			const created = await registerPasskey(name);
			passkeys = [created, ...passkeys];
			registerOpen = false;
			toast.success(`“${created.name}” registered`);
		} catch (err) {
			// Dismissing the platform prompt is a decision, not a failure.
			if (err instanceof PasskeyAbortedError || isPasskeyCancellation(err)) return;

			if (err instanceof ApiError && err.status === 409) {
				toast.error('That authenticator already has a passkey for this account.');
				return;
			}
			toast.error('Could not register the passkey');
			captureError(err, { area: 'auth', extra: { action: 'passkey-register' } });
		} finally {
			registering = false;
		}
	}

	function openRename(p: PasskeyView) {
		renameTarget = p;
		renameValue = p.name;
	}

	async function handleRename() {
		const target = renameTarget;
		const name = renameValue.trim();
		if (!target || !name) return;

		renaming = true;
		try {
			await renamePasskey(target.id, name);
			passkeys = passkeys.map((p) => (p.id === target.id ? { ...p, name } : p));
			renameTarget = undefined;
		} catch (err) {
			toast.error('Could not rename the passkey');
			captureError(err, { area: 'auth', extra: { action: 'passkey-rename' } });
		} finally {
			renaming = false;
		}
	}

	async function handleDelete() {
		const target = deleteTarget;
		if (!target) return;

		deleting = true;
		busyId = target.id;
		try {
			await deletePasskey(target.id);
			passkeys = passkeys.filter((p) => p.id !== target.id);
			deleteTarget = undefined;
			toast(`“${target.name}” revoked`);
		} catch (err) {
			toast.error('Could not revoke the passkey');
			captureError(err, { area: 'auth', extra: { action: 'passkey-delete' } });
		} finally {
			deleting = false;
			busyId = undefined;
		}
	}

	/** Format an RFC3339 timestamp as a short local date, or a dash when absent. */
	function formatDate(value: string | null): string {
		if (!value) return '—';
		const date = new Date(value);
		return Number.isNaN(date.getTime()) ? '—' : date.toLocaleDateString();
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Passkeys</Card.Title>
		<Card.Description>
			Sign in with Face ID, Touch ID, a device PIN or a security key instead of typing your
			password. Your password keeps working either way, so losing a device can't lock you out.
		</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-4">
		{#if !serverSupported}
			<p class="text-muted-foreground text-sm">
				This server has no passkey relying party configured. Set <code>PASSKEY_RP_ID</code>
				(or <code>PUBLIC_BASE_URL</code>) and restart it.
			</p>
		{:else if !online}
			<p class="text-muted-foreground text-sm">
				You're offline. Connect to the server to manage passkeys.
			</p>
		{:else if loading}
			<p class="text-muted-foreground text-sm">Loading passkeys…</p>
		{:else if loadError === 'offline'}
			<p class="text-muted-foreground text-sm">
				Couldn't reach the server. Check your connection and try again.
			</p>
			<Button variant="outline" size="sm" onclick={load}>Retry</Button>
		{:else if loadError}
			<p class="text-destructive text-sm">Couldn't load passkeys: {loadError}.</p>
			<Button variant="outline" size="sm" onclick={load}>Retry</Button>
		{:else}
			{#if passkeys.length === 0}
				<p class="text-muted-foreground text-sm">
					No passkeys yet. Register one to sign in without your password.
				</p>
			{:else}
				<ul class="space-y-2">
					{#each passkeys as p (p.id)}
						<li class="rounded-lg border p-3">
							<div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
								<div class="flex min-w-0 items-start gap-2.5">
									<KeyRoundIcon class="text-muted-foreground mt-0.5 size-4 shrink-0" />
									<div class="min-w-0">
										<p class="truncate font-medium">{p.name}</p>
										<p class="text-muted-foreground text-xs">
											Added {formatDate(p.created_at)} · Last used {formatDate(p.last_used_at)}
										</p>
									</div>
								</div>
								<div class="flex shrink-0 flex-wrap items-center gap-1.5">
									<Button variant="outline" size="sm" onclick={() => openRename(p)}>Rename</Button>
									<Button
										variant="ghost"
										size="sm"
										disabled={busyId === p.id}
										onclick={() => (deleteTarget = p)}
									>
										{#if busyId === p.id}
											<Loader2Icon class="size-4 animate-spin" />
										{/if}
										Revoke
									</Button>
								</div>
							</div>
						</li>
					{/each}
				</ul>
			{/if}

			{#if deviceSupported}
				<Button variant="outline" size="sm" onclick={openRegister}>
					<PlusIcon class="size-4" />
					Register a passkey
				</Button>
			{:else}
				<p class="text-muted-foreground text-sm">
					This device can't create passkeys, but any already registered here still work.
				</p>
			{/if}
		{/if}
	</Card.Content>
</Card.Root>

<!-- Register dialog -->
<Dialog.Root bind:open={registerOpen}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Register a passkey</Dialog.Title>
			<Dialog.Description>
				Name this device, then confirm with your fingerprint, face or device PIN.
			</Dialog.Description>
		</Dialog.Header>

		<div class="grid gap-1.5 py-2">
			<Label for="passkey-name">Name</Label>
			<Input
				id="passkey-name"
				bind:value={registerName}
				maxlength={MAX_NAME_LEN}
				placeholder="MacBook"
			/>
		</div>

		<Dialog.Footer class="mt-2 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (registerOpen = false)} disabled={registering}>
				Cancel
			</Button>
			<Button onclick={handleRegister} disabled={registering || registerName.trim().length === 0}>
				{#if registering}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				Continue
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<!-- Rename dialog -->
<Dialog.Root
	open={renameTarget !== undefined}
	onOpenChange={(v) => !v && (renameTarget = undefined)}
>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Rename passkey</Dialog.Title>
		</Dialog.Header>

		<div class="grid gap-1.5 py-2">
			<Label for="passkey-rename">Name</Label>
			<Input id="passkey-rename" bind:value={renameValue} maxlength={MAX_NAME_LEN} />
		</div>

		<Dialog.Footer class="mt-2 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (renameTarget = undefined)} disabled={renaming}>
				Cancel
			</Button>
			<Button onclick={handleRename} disabled={renaming || renameValue.trim().length === 0}>
				{#if renaming}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				Save
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<!-- Revoke confirmation -->
<Dialog.Root
	open={deleteTarget !== undefined}
	onOpenChange={(v) => !v && (deleteTarget = undefined)}
>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>Revoke “{deleteTarget?.name}”?</Dialog.Title>
			<Dialog.Description>
				That device will no longer be able to sign in with its passkey. You can still sign in with
				your password, and register the device again later.
			</Dialog.Description>
		</Dialog.Header>

		<Dialog.Footer class="mt-2 flex justify-end gap-2">
			<Button variant="outline" onclick={() => (deleteTarget = undefined)} disabled={deleting}>
				Cancel
			</Button>
			<Button variant="destructive" onclick={handleDelete} disabled={deleting}>
				{#if deleting}
					<Loader2Icon class="size-4 animate-spin" />
				{/if}
				Revoke
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
