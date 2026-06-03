<!-- Settings page — four-tab layout: Reading / Device / Storage / Server.
     Spec §8 (settings model), §9 (PATCH /api/settings, device setup), §10 (persist).
-->
<script lang="ts">
	import * as Tabs from '$lib/components/ui/tabs';
	import ReadingSettings from '$lib/components/settings/ReadingSettings.svelte';
	import DeviceSettings from '$lib/components/settings/DeviceSettings.svelte';
	import StorageSettings from '$lib/components/settings/StorageSettings.svelte';
	import ServerSettings from '$lib/components/settings/ServerSettings.svelte';
	import pkg from '../../../package.json';
	import { checkForUpdate } from '$lib/pwa/state.svelte';
</script>

<svelte:head>
	<title>Settings — Deep Reader</title>
</svelte:head>

<div class="space-y-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
		<p class="text-muted-foreground text-sm">
			Manage your reading preferences and device configuration.
		</p>
	</div>

	<Tabs.Root value="reading">
		<Tabs.List class="w-full">
			<Tabs.Trigger value="reading" class="flex-1">Reading</Tabs.Trigger>
			<Tabs.Trigger value="device" class="flex-1">Device</Tabs.Trigger>
			<Tabs.Trigger value="storage" class="flex-1">Storage</Tabs.Trigger>
			<Tabs.Trigger value="server" class="flex-1">Server</Tabs.Trigger>
		</Tabs.List>

		<Tabs.Content value="reading" class="mt-4">
			<ReadingSettings />
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

	<p class="text-muted-foreground mt-6 text-center text-xs">
		Client v{pkg.version}
		·
		<button
			onclick={checkForUpdate}
			class="text-muted-foreground cursor-pointer underline"
		>
			Check for updates
		</button>
	</p>
</div>
