<!-- Device setup card.
     Manages: the Server URL (stored in db.sync_state, used when the PWA is not
     served by the backend) and the signed-in account (sign out).
     "Test connection / Sync now" triggers sync() and shows a toast.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import * as Card from '$lib/components/ui/card';
	import * as Alert from '$lib/components/ui/alert';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import { getSyncState, updateSyncState } from '$lib/db';
	import { sync } from '$lib/sync/engine';
	import { authState, logout } from '$lib/auth/store.svelte';
	import { checkForUpdate } from '$lib/pwa/bootstrap';
	import MonitorSmartphoneIcon from '@lucide/svelte/icons/monitor-smartphone';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import LogOutIcon from '@lucide/svelte/icons/log-out';
	import ArrowUpCircleIcon from '@lucide/svelte/icons/arrow-up-circle';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let serverUrl = $state('');
	let isSyncing = $state(false);
	let isCheckingUpdate = $state(false);
	let serverUrlError = $state('');

	// ---------------------------------------------------------------------------
	// Lifecycle
	// ---------------------------------------------------------------------------

	onMount(async () => {
		const state = await getSyncState();
		serverUrl = state.serverUrl ?? '';
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

	async function saveServerUrl(): Promise<boolean> {
		const urlErr = validateServerUrl(serverUrl);
		serverUrlError = urlErr;
		if (urlErr) return false;

		await updateSyncState({ serverUrl: serverUrl.trim() || undefined });
		return true;
	}

	async function handleTestSync() {
		if (!(await saveServerUrl())) return;

		isSyncing = true;
		try {
			await sync();
			toast('Sync successful');
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			toast.error(`Sync failed: ${msg}`);
		} finally {
			isSyncing = false;
		}
	}

	async function handleCheckUpdate() {
		isCheckingUpdate = true;
		try {
			const found = await checkForUpdate();
			if (!found) toast('Already on the latest version.');
		} finally {
			isCheckingUpdate = false;
		}
	}

	async function handleLogout() {
		await logout();
		await goto('/login');
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Device Setup</Card.Title>
		<Card.Description>Connect this device to your Deep Reader server.</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-5">
		<!-- Account -->
		<div class="flex items-center justify-between gap-3">
			<div class="grid gap-0.5">
				<span class="text-sm font-medium">Signed in</span>
				<span class="text-muted-foreground text-xs">
					{authState.username ?? 'Your account'}
				</span>
			</div>
			<Button variant="outline" size="sm" class="gap-2" onclick={handleLogout}>
				<LogOutIcon class="size-4" />
				Sign out
			</Button>
		</div>

		<Separator />

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

		<Button onclick={handleTestSync} disabled={isSyncing} class="w-full gap-2">
			<RefreshCwIcon class={['size-4', isSyncing && 'animate-spin'].filter(Boolean).join(' ')} />
			{isSyncing ? 'Syncing…' : 'Test Connection / Sync Now'}
		</Button>

		<Button variant="outline" onclick={handleCheckUpdate} disabled={isCheckingUpdate} class="w-full gap-2">
			<ArrowUpCircleIcon class={['size-4', isCheckingUpdate && 'animate-pulse'].filter(Boolean).join(' ')} />
			{isCheckingUpdate ? 'Checking…' : 'Check for Updates'}
		</Button>

		<Separator />

		<!-- Multi-device hint -->
		<Alert.Root>
			<MonitorSmartphoneIcon class="size-4" />
			<Alert.Title>Setting up a second device</Alert.Title>
			<Alert.Description>
				Open this server's address on the other device and sign in with the same username and
				password. There is only one account — all devices share it.
			</Alert.Description>
		</Alert.Root>
	</Card.Content>
</Card.Root>
