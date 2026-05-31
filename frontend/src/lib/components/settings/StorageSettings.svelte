<!-- Storage settings card.
     - "Enable persistent storage" button → navigator.storage.persist().
     - Shows current grant status.
     - iOS "Add to Home Screen" hint when running on iOS Safari (not standalone).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { browser } from '$app/environment';
	import { toast } from 'svelte-sonner';
	import * as Card from '$lib/components/ui/card';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import { requestPersistentStorage } from '$lib/sync/persist';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';
	import CheckCircle2Icon from '@lucide/svelte/icons/circle-check';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	/** null = API not available; true = granted; false = denied/undecided */
	let persistGranted = $state<boolean | null>(null);
	let isPersistApiAvailable = $state(false);
	let isRequesting = $state(false);

	/** True when running on iOS Safari in normal browser mode (not standalone). */
	let showIosHint = $state(false);

	// ---------------------------------------------------------------------------
	// Lifecycle
	// ---------------------------------------------------------------------------

	onMount(async () => {
		if (!browser) return;

		isPersistApiAvailable = !!navigator.storage?.persist;

		if (isPersistApiAvailable) {
			// Check current status without prompting.
			try {
				persistGranted = await navigator.storage.persisted();
			} catch {
				persistGranted = false;
			}
		}

		// Detect iOS Safari in non-standalone mode.
		const ua = navigator.userAgent;
		const isIos = /iphone|ipad|ipod/i.test(ua);
		// @ts-ignore — navigator.standalone is non-standard iOS API
		const isStandalone =
			navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
		showIosHint = isIos && !isStandalone;
	});

	// ---------------------------------------------------------------------------
	// Handlers
	// ---------------------------------------------------------------------------

	async function handleRequestPersist() {
		isRequesting = true;
		try {
			const granted = await requestPersistentStorage();
			persistGranted = granted;
			if (granted) {
				toast.success('Persistent storage granted');
			} else {
				toast.info(
					'Persistent storage not granted by the browser. Try adding the app to your Home Screen.'
				);
			}
		} finally {
			isRequesting = false;
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Storage</Card.Title>
		<Card.Description>Protect your offline data from browser eviction.</Card.Description>
	</Card.Header>

	<Card.Content class="space-y-5">
		<!-- Persistent storage section -->
		<div class="space-y-3">
			<div class="flex items-center justify-between gap-4">
				<div class="min-w-0">
					<p class="text-sm font-medium leading-none">Persistent Storage</p>
					<p class="text-muted-foreground mt-1 text-xs">
						Prevents the browser from clearing cached articles when storage is low.
					</p>
				</div>

				{#if persistGranted === true}
					<div class="text-primary flex shrink-0 items-center gap-1.5 text-sm font-medium">
						<CheckCircle2Icon class="size-4" />
						<span>Enabled</span>
					</div>
				{:else if isPersistApiAvailable}
					<Button
						size="sm"
						variant="outline"
						onclick={handleRequestPersist}
						disabled={isRequesting}
						class="shrink-0 gap-2"
					>
						<DatabaseIcon class="size-4" />
						{isRequesting ? 'Requesting…' : 'Enable'}
					</Button>
				{:else}
					<span class="text-muted-foreground shrink-0 text-xs">Not available</span>
				{/if}
			</div>

			{#if persistGranted === false && isPersistApiAvailable}
				<p class="text-muted-foreground text-xs">
					The browser did not grant persistent storage. You can try again or add the app to your
					Home Screen to increase the chance of approval.
				</p>
			{/if}
		</div>

		<!-- iOS Add to Home Screen hint -->
		{#if showIosHint}
			<Alert.Root>
				<SmartphoneIcon class="size-4" />
				<Alert.Title>Add to Home Screen for best experience</Alert.Title>
				<Alert.Description>
					On iOS, Safari may clear cached articles after a few weeks. To keep your library available
					offline and improve storage stability, tap the
					<strong>Share</strong> button in Safari and choose
					<strong>Add to Home Screen</strong>.
				</Alert.Description>
			</Alert.Root>
		{/if}
	</Card.Content>
</Card.Root>
