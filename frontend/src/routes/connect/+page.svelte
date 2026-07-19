<!-- Native onboarding: point the app at a Deep Reader server (MOBILE-ARCH.md §7).
     Reachable only on native before login, when no serverUrl is configured yet.
     The layout guard routes here; on success we verify the URL with GET /api/config
     (reachable without auth), persist it, and hand off to the normal auth flow. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { getConfig, OfflineError } from '$lib/api';
	import { getSyncState, updateSyncState } from '$lib/db';
	import { markServerConfigured } from '$lib/platform/connect.svelte';
	import { refreshAuth } from '$lib/auth/store.svelte';
	import { toggleMode, mode } from 'mode-watcher';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';

	let serverUrl = $state('');
	let error = $state('');
	let submitting = $state(false);

	const canSubmit = $derived(serverUrl.trim().length > 0);

	onMount(async () => {
		// Pre-fill if a URL was stored before (e.g. re-onboarding after a reset).
		const state = await getSyncState();
		serverUrl = state.serverUrl ?? '';
	});

	function normalize(v: string): string | null {
		const trimmed = v.trim().replace(/\/$/, '');
		try {
			const url = new URL(trimmed);
			if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
			return trimmed;
		} catch {
			return null;
		}
	}

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';

		const url = normalize(serverUrl);
		if (!url) {
			error = 'Enter a valid URL, e.g. https://reader.example.com';
			return;
		}

		submitting = true;
		try {
			// Persist first so getConfig() resolves the base URL against it, then
			// verify the server actually answers before committing to onboarding.
			await updateSyncState({ serverUrl: url });
			await getConfig();
			markServerConfigured();
			// Let the auth store learn initialized/authenticated from the same server,
			// then hand control to the layout guard (routes to /setup or /login).
			await refreshAuth();
			await goto('/');
		} catch (err) {
			// Roll back the stored URL so a bad address does not strand the app with a
			// serverUrl that points nowhere.
			await updateSyncState({ serverUrl: undefined });
			if (err instanceof OfflineError) {
				error = "Couldn't reach that server. Check the URL and your connection.";
			} else {
				error = 'That server did not respond as expected. Check the URL.';
			}
		} finally {
			submitting = false;
		}
	}
</script>

<div class="flex min-h-svh items-center justify-center px-4 py-10">
	<Card.Root class="relative w-full max-w-sm">
		<div class="absolute top-3 right-3">
			<Button
				variant="ghost"
				size="icon"
				onclick={toggleMode}
				aria-label="Toggle light / dark mode"
			>
				{#if mode.current === 'dark'}
					<MoonIcon class="size-4" />
				{:else}
					<SunIcon class="size-4" />
				{/if}
			</Button>
		</div>

		<Card.Header class="text-center">
			<div class="mb-2 flex justify-center">
				<div class="bg-muted rounded-full p-3">
					<BookOpenIcon class="size-6" />
				</div>
			</div>
			<Card.Title>Connect to your server</Card.Title>
			<Card.Description>
				Enter the address of your Deep Reader server to get started.
			</Card.Description>
		</Card.Header>

		<Card.Content>
			<form class="space-y-4" onsubmit={handleSubmit}>
				<div class="grid gap-1.5">
					<Label for="server-url">Server URL</Label>
					<Input
						id="server-url"
						type="url"
						inputmode="url"
						autocapitalize="none"
						autocorrect="off"
						spellcheck={false}
						placeholder="https://reader.example.com"
						bind:value={serverUrl}
						disabled={submitting}
						aria-invalid={!!error}
						required
					/>
				</div>

				{#if error}
					<p class="text-destructive text-sm">{error}</p>
				{/if}

				<Button type="submit" class="w-full" disabled={!canSubmit || submitting}>
					{#if submitting}
						<LoaderCircleIcon class="mr-2 size-4 animate-spin" />
						Connecting…
					{:else}
						Connect
					{/if}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
