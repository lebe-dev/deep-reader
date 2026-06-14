<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import favicon from '$lib/assets/favicon.svg';
	import { ModeWatcher, toggleMode, mode } from 'mode-watcher';
	import { page } from '$app/state';
	import { navigating } from '$app/stores';
	import { goto, afterNavigate } from '$app/navigation';
	import { Toaster } from '$lib/components/ui/sonner';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import LibraryIcon from '@lucide/svelte/icons/library';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import { cn } from '$lib/utils';
	import { readerFullscreen } from '$lib/reader-fullscreen.svelte';
	import { initSync } from '$lib/sync/store.svelte';
	import { bootstrapPWA } from '$lib/pwa/bootstrap';
	import { authState, refreshAuth } from '$lib/auth/store.svelte';
	import { captureError } from '$lib/sentry';
	import UpdateBanner from '$lib/components/UpdateBanner.svelte';
	import MarkdownBudgetBanner from '$lib/components/MarkdownBudgetBanner.svelte';

	let { children } = $props();

	// App-wide render error boundary. A synchronous throw during a component's
	// render or in a `$derived`/`$effect` (e.g. mapping a backend array that came
	// back null) is NOT caught by SvelteKit's `handleError` hook — it surfaces as
	// an uncaught error and never reaches Sentry, leaving a blank screen. Catching
	// it here gives the user a recoverable fallback and reports it deliberately.
	function reportRenderError(error: unknown) {
		console.error('[ui] render error', error);
		captureError(error, { area: 'ui', extra: { path: page.url.pathname } });
	}

	// Routes that render without the app chrome and are reachable before login.
	const AUTH_ROUTES = ['/setup', '/login'];

	let syncStarted = false;

	onMount(() => {
		bootstrapPWA();
		refreshAuth();
	});

	// Always land at the top when navigating between pages. The reader window
	// scroll (set via scrollIntoView for position restore) otherwise leaks into
	// Library / Settings, since SvelteKit's automatic scroll reset is unreliable
	// in SPA mode. The reader restores its own position later, in TokenRenderer's
	// onMount, so this never fights the reading-position restore. Anchor links
	// (URLs with a hash) keep their native scroll-into-view behaviour.
	afterNavigate((nav) => {
		if (nav.to?.url.hash) return;
		window.scrollTo(0, 0);
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
	<link rel="icon" href={favicon} />
</svelte:head>

<ModeWatcher />
<Toaster richColors closeButton position="bottom-right" />

{#if $navigating}
	<div
		class="fixed inset-x-0 top-0 z-50 h-[2px] overflow-hidden"
		role="progressbar"
		aria-label="Loading"
	>
		<div class="nav-bar bg-primary absolute h-full w-1/2"></div>
	</div>
{/if}

<svelte:boundary onerror={reportRenderError}>
	{#if !authState.checked}
		<div class="bg-background min-h-svh"></div>
	{:else if showChrome}
		<div class="bg-background text-foreground flex min-h-svh flex-col">
			{#if !readerFullscreen.active}
				<UpdateBanner />
				<MarkdownBudgetBanner />
				<header
					class="bg-background/95 supports-[backdrop-filter]:bg-background/60 sticky top-0 z-40 w-full border-b pt-[env(safe-area-inset-top)] backdrop-blur"
				>
					<div class="mx-auto flex h-14 w-full max-w-3xl items-center gap-2 px-4">
						<a href="/" class="mr-2 flex items-center gap-2 font-semibold">
							<BookOpenIcon class="size-5" />
							<span class="hidden sm:inline">Deep Reader</span>
							<span class="sm:hidden">DR</span>
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
			{/if}

			<main
				class={cn(
					'mx-auto w-full max-w-3xl flex-1 px-4',
					readerFullscreen.active ? 'pt-[calc(env(safe-area-inset-top)+1rem)] pb-6' : 'py-6'
				)}
			>
				{@render children?.()}
			</main>
		</div>
	{:else}
		<!-- Auth pages render bare, centered. -->
		<div class="bg-background text-foreground flex min-h-svh flex-col">
			{@render children?.()}
		</div>
	{/if}

	{#snippet failed(error, reset)}
		<div
			class="bg-background text-foreground flex min-h-svh flex-col items-center justify-center gap-4 px-4 text-center"
		>
			<h1 class="text-lg font-semibold">Something went wrong</h1>
			<p class="text-muted-foreground max-w-xs text-sm">
				The page hit an unexpected error. Try again, or reload if it persists.
			</p>
			{#if error instanceof Error && error.message}
				<p class="text-muted-foreground max-w-xs font-mono text-xs break-words">{error.message}</p>
			{/if}
			<div class="flex items-center gap-2">
				<Button variant="outline" onclick={reset}>Try again</Button>
				<Button variant="ghost" onclick={() => location.reload()}>Reload</Button>
			</div>
		</div>
	{/snippet}
</svelte:boundary>

<style>
	.nav-bar {
		animation: nav-slide 1.2s ease-in-out infinite;
	}

	@keyframes nav-slide {
		0% {
			left: -55%;
		}
		100% {
			left: 110%;
		}
	}
</style>
