<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import favicon from '$lib/assets/favicon.svg';
	import { ModeWatcher, toggleMode, mode } from 'mode-watcher';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { Toaster } from '$lib/components/ui/sonner';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import LibraryIcon from '@lucide/svelte/icons/library';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import { cn } from '$lib/utils';
	import { initSync } from '$lib/sync/store.svelte';
	import { bootstrapPWA } from '$lib/pwa/bootstrap';
	import { authState, refreshAuth } from '$lib/auth/store.svelte';
	import UpdateBanner from '$lib/components/UpdateBanner.svelte';

	let { children } = $props();

	// Routes that render without the app chrome and are reachable before login.
	const AUTH_ROUTES = ['/setup', '/login'];

	let syncStarted = false;

	onMount(() => {
		bootstrapPWA();
		refreshAuth();
	});

	// Auth guard: route between /setup, /login and the app based on auth state,
	// and start background sync once authenticated. Re-runs on auth/path changes.
	$effect(() => {
		if (!authState.checked) return;
		const path = page.url.pathname;

		if (authState.initialized === false) {
			if (path !== '/setup') goto('/setup');
			return;
		}
		if (!authState.authenticated) {
			if (path !== '/login') goto('/login');
			return;
		}
		// Authenticated: keep the user out of the auth pages and start syncing.
		if (AUTH_ROUTES.includes(path)) {
			goto('/');
			return;
		}
		if (!syncStarted) {
			syncStarted = true;
			initSync();
		}
	});

	const navItems = [
		{ href: '/', label: 'Library', icon: LibraryIcon },
		{ href: '/settings', label: 'Settings', icon: SettingsIcon }
	];

	function isActive(href: string): boolean {
		if (href === '/') return page.url.pathname === '/';
		return page.url.pathname.startsWith(href);
	}

	// Show the app chrome only for an authenticated user on a non-auth route.
	const showChrome = $derived(authState.authenticated && !AUTH_ROUTES.includes(page.url.pathname));
</script>

<svelte:head>
	<title>Deep Reader</title>
	<link rel="icon" href={favicon} />
</svelte:head>

<ModeWatcher />
<Toaster richColors closeButton position="bottom-right" />

{#if showChrome}
	<div class="bg-background text-foreground flex min-h-svh flex-col">
		<UpdateBanner />
		<header
			class="bg-background/95 supports-[backdrop-filter]:bg-background/60 sticky top-0 z-40 w-full border-b backdrop-blur"
		>
			<div class="mx-auto flex h-14 w-full max-w-3xl items-center gap-2 px-4">
				<a href="/" class="mr-2 flex items-center gap-2 font-semibold">
					<BookOpenIcon class="size-5" />
					<span>Deep Reader</span>
				</a>

				<Separator orientation="vertical" class="mx-1 h-6" />

				<nav class="flex items-center gap-1">
					{#each navItems as item (item.href)}
						{@const Icon = item.icon}
						<Button
							href={item.href}
							variant="ghost"
							size="sm"
							class={cn(
								'gap-2',
								isActive(item.href) ? 'text-foreground bg-accent' : 'text-muted-foreground'
							)}
						>
							<Icon class="size-4" />
							<span>{item.label}</span>
						</Button>
					{/each}
				</nav>

				<div class="ml-auto flex items-center gap-1">
					<Button
						variant="ghost"
						size="icon"
						onclick={toggleMode}
						aria-label="Toggle light / dark mode"
						title="Toggle theme"
					>
						{#if mode.current === 'dark'}
							<MoonIcon class="size-4" />
						{:else}
							<SunIcon class="size-4" />
						{/if}
					</Button>
				</div>
			</div>
		</header>

		<main class="mx-auto w-full max-w-3xl flex-1 px-4 py-6">
			{@render children?.()}
		</main>
	</div>
{:else}
	<!-- Auth pages (and the pre-auth loading flash) render bare, centered. -->
	<div class="bg-background text-foreground flex min-h-svh flex-col">
		{@render children?.()}
	</div>
{/if}
