<!-- Device setup card.
     Manages: Server URL and Auth Token (stored in db.sync_state, not settings table).
     "Test connection / Sync now" triggers sync() and shows a toast.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import * as Card from '$lib/components/ui/card';
	import * as Alert from '$lib/components/ui/alert';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import { getSyncState, updateSyncState } from '$lib/db';
	import { sync } from '$lib/sync/engine';
	import MonitorSmartphoneIcon from '@lucide/svelte/icons/monitor-smartphone';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let serverUrl = $state('');
	let authToken = $state('');
	let isSyncing = $state(false);

	// Validation
	let serverUrlError = $state('');
	let authTokenError = $state('');

	// ---------------------------------------------------------------------------
	// Lifecycle
	// ---------------------------------------------------------------------------

	onMount(async () => {
		const state = await getSyncState();
		serverUrl = state.serverUrl ?? '';
		authToken = state.authToken ?? '';
	});

	// ---------------------------------------------------------------------------
	// Helpers
	// ---------------------------------------------------------------------------

	function validateServerUrl(v: string): string {
		if (!v.trim()) return '';
		try {
			const url = new URL(v.trim());
			if (url.protocol !== 'http:' && url.protocol !== 'https:') {
				return 'URL must start with http:// or https://';
			}
			return '';
		} catch {
			return 'Enter a valid URL (e.g. https://reader.example.com)';
		}
	}

	function validateAuthToken(v: string): string {
		if (!v.trim()) return 'Auth token is required to connect';
		return '';
	}

	async function saveDevice() {
		const urlErr = validateServerUrl(serverUrl);
		const tokenErr = validateAuthToken(authToken);
		serverUrlError = urlErr;
		authTokenError = tokenErr;

		if (urlErr || tokenErr) return;

		await updateSyncState({
			serverUrl: serverUrl.trim() || undefined,
			authToken: authToken.trim()
		});
	}

	async function handleTestSync() {
		await saveDevice();
		if (serverUrlError || authTokenError) return;

		isSyncing = true;
		try {
			await sync();
			toast.success('Sync successful');
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			toast.error(`Sync failed: ${msg}`);
		} finally {
			isSyncing = false;
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Device Setup</Card.Title>
		<Card.Description>Connect this device to your Deep Reader server.</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-5">
		<!-- Server URL -->
		<div class="grid gap-1.5">
			<Label for="server-url-input">Server URL</Label>
			<Input
				id="server-url-input"
				type="url"
				placeholder="https://reader.example.com"
				bind:value={serverUrl}
				aria-invalid={!!serverUrlError}
			/>
			{#if serverUrlError}
				<p class="text-destructive text-xs">{serverUrlError}</p>
			{:else}
				<p class="text-muted-foreground text-xs">
					Leave blank if this PWA is served by the backend (same origin).
				</p>
			{/if}
		</div>

		<!-- Auth Token -->
		<div class="grid gap-1.5">
			<Label for="auth-token-input">Auth Token</Label>
			<Input
				id="auth-token-input"
				type="password"
				placeholder="Your AUTH_TOKEN from the server .env"
				bind:value={authToken}
				aria-invalid={!!authTokenError}
				autocomplete="current-password"
			/>
			{#if authTokenError}
				<p class="text-destructive text-xs">{authTokenError}</p>
			{:else}
				<p class="text-muted-foreground text-xs">
					The token is stored only in this browser's IndexedDB.
				</p>
			{/if}
		</div>

		<Button onclick={handleTestSync} disabled={isSyncing} class="w-full gap-2">
			<RefreshCwIcon class={['size-4', isSyncing && 'animate-spin'].filter(Boolean).join(' ')} />
			{isSyncing ? 'Syncing…' : 'Test Connection / Sync Now'}
		</Button>

		<Separator />

		<!-- Multi-device hint -->
		<Alert.Root>
			<MonitorSmartphoneIcon class="size-4" />
			<Alert.Title>Setting up a second device</Alert.Title>
			<Alert.Description>
				Copy the <strong>AUTH_TOKEN</strong> from your server's <code>.env</code> file and paste it above
				on each device. All devices share the same token — there is only one user.
			</Alert.Description>
		</Alert.Root>
	</Card.Content>
</Card.Root>
