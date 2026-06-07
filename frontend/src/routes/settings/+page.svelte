<!-- Settings page — tab layout: Reading / Appearance / LLM / Device / Storage / Server.
     Spec §8 (settings model), §9 (PATCH /api/settings, device setup), §10 (persist).
-->
<script lang="ts">
	import * as Tabs from '$lib/components/ui/tabs';
	import ReadingSettings from '$lib/components/settings/ReadingSettings.svelte';
	import AppearanceSettings from '$lib/components/settings/AppearanceSettings.svelte';
	import LlmSettings from '$lib/components/settings/LlmSettings.svelte';
	import LlmProvidersSettings from '$lib/components/settings/LlmProvidersSettings.svelte';
	import DeviceSettings from '$lib/components/settings/DeviceSettings.svelte';
	import StorageSettings from '$lib/components/settings/StorageSettings.svelte';
	import ServerSettings from '$lib/components/settings/ServerSettings.svelte';
	import pkg from '../../../package.json';
	import { checkForUpdate } from '$lib/pwa/bootstrap';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import { toast } from 'svelte-sonner';
	import { onMount } from 'svelte';

	// Horizontal-scroll affordance for the tab strip: on narrow screens the tabs
	// overflow and there's no scrollbar (no-scrollbar), so without a hint the user
	// can't tell more tabs sit off the right edge. We fade the overflowing edges
	// and show a chevron while there's room to scroll further.
	let tabsListEl: HTMLElement | null = $state(null);
	let canScrollLeft = $state(false);
	let canScrollRight = $state(false);

	function updateTabScroll() {
		const el = tabsListEl;
		if (!el) return;
		canScrollLeft = el.scrollLeft > 1;
		canScrollRight = el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
	}

	onMount(() => {
		updateTabScroll();
		window.addEventListener('resize', updateTabScroll);
		return () => window.removeEventListener('resize', updateTabScroll);
	});

	async function handleCheckForUpdate() {
		const toastId = toast.loading('Checking for updates…');
		const found = await checkForUpdate();
		toast.dismiss(toastId);
		if (found) {
			toast.success('Update available — see the banner above.');
		} else {
			toast('Already up to date.');
		}
	}
</script>

<svelte:head>
	<title>Settings — Deep Reader</title>
</svelte:head>

<div class="select-none space-y-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
		<p class="text-muted-foreground text-sm">
			Manage your reading preferences and device configuration.
		</p>
	</div>

	<Tabs.Root value="reading">
		<div class="relative">
			<Tabs.List
				bind:ref={tabsListEl}
				onscroll={updateTabScroll}
				class="no-scrollbar w-full max-w-full justify-start overflow-x-auto sm:justify-center"
			>
				<Tabs.Trigger value="reading" class="shrink-0 sm:flex-1">Reading</Tabs.Trigger>
				<Tabs.Trigger value="appearance" class="shrink-0 sm:flex-1">Appearance</Tabs.Trigger>
				<Tabs.Trigger value="llm" class="shrink-0 sm:flex-1">LLM</Tabs.Trigger>
				<Tabs.Trigger value="device" class="shrink-0 sm:flex-1">Device</Tabs.Trigger>
				<Tabs.Trigger value="storage" class="shrink-0 sm:flex-1">Storage</Tabs.Trigger>
				<Tabs.Trigger value="server" class="shrink-0 sm:flex-1">Server</Tabs.Trigger>
			</Tabs.List>

			<!-- Edge fades + chevron hinting that the strip scrolls horizontally. -->
			<div
				class="from-muted pointer-events-none absolute inset-y-0 left-0 w-6 rounded-l-lg bg-gradient-to-r to-transparent transition-opacity duration-150"
				class:opacity-0={!canScrollLeft}
				aria-hidden="true"
			></div>
			<div
				class="from-muted pointer-events-none absolute inset-y-0 right-0 flex w-8 items-center justify-end rounded-r-lg bg-gradient-to-l to-transparent pr-1 transition-opacity duration-150"
				class:opacity-0={!canScrollRight}
				aria-hidden="true"
			>
				<ChevronRightIcon class="text-muted-foreground size-4" />
			</div>
		</div>

		<Tabs.Content value="reading" class="mt-4">
			<ReadingSettings />
		</Tabs.Content>

		<Tabs.Content value="appearance" class="mt-4">
			<AppearanceSettings />
		</Tabs.Content>

		<Tabs.Content value="llm" class="mt-4 space-y-4">
			<LlmProvidersSettings />
			<LlmSettings />
		</Tabs.Content>

		<Tabs.Content value="device" class="mt-4">
			<DeviceSettings />
		</Tabs.Content>

		<Tabs.Content value="storage" class="mt-4">
			<StorageSettings />
		</Tabs.Content>

		<Tabs.Content value="server" class="mt-4">
			<ServerSettings />
		</Tabs.Content>
	</Tabs.Root>

	<div class="text-muted-foreground mt-6 flex items-center justify-center gap-3 text-xs">
		<span>Client v{pkg.version}</span>
		<span class="bg-border h-3 w-px"></span>
		<a
			href="https://github.com/lebe-dev/deep-reader"
			target="_blank"
			rel="noopener noreferrer"
			class="hover:text-foreground flex items-center gap-1 underline transition-colors"
		>
			GitHub
			<ExternalLinkIcon class="size-3" />
		</a>
		<span class="bg-border h-3 w-px"></span>
		<button
			onclick={handleCheckForUpdate}
			class="cursor-pointer underline hover:text-foreground transition-colors"
		>
			Check for updates
		</button>
	</div>
</div>
