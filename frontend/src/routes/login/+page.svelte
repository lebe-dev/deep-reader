<!-- Sign in with the single built-in account. The layout guard routes here when
     the service is initialized but the request is unauthenticated. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { ApiError, OfflineError } from '$lib/api';
	import { login } from '$lib/auth/store.svelte';
	import { toggleMode, mode } from 'mode-watcher';
	import SunIcon from '@lucide/svelte/icons/sun';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';

	const STORAGE_KEY = 'login:username';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);

	let usernameEl = $state<HTMLInputElement | null>(null);
	let passwordEl = $state<HTMLInputElement | null>(null);

	const canSubmit = $derived(username.trim().length > 0 && password.length > 0);

	onMount(() => {
		const saved = localStorage.getItem(STORAGE_KEY);
		if (saved) username = saved;

		if (username) {
			passwordEl?.focus();
		} else {
			usernameEl?.focus();
		}
	});

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';

		submitting = true;
		try {
			await login(username.trim(), password);
			localStorage.setItem(STORAGE_KEY, username.trim());
			await goto('/');
		} catch (err) {
			if (err instanceof ApiError && err.status === 401) {
				error = 'Invalid username or password.';
			} else if (err instanceof OfflineError) {
				error = 'Could not reach the server. Check your connection.';
			} else {
				error = 'Sign-in failed. Try again.';
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
			<p class="title-shimmer mb-1 text-4xl font-bold tracking-tight" style="font-family: 'Merriweather', serif;">
				Deep Reader
			</p>
			<Card.Description>Enter your account credentials.</Card.Description>
		</Card.Header>

		<Card.Content>
			<form class="space-y-4" onsubmit={handleSubmit}>
				<div class="grid gap-1.5">
					<Label for="username">Username</Label>
					<Input id="username" bind:value={username} bind:ref={usernameEl} autocomplete="username" disabled={submitting} required />
				</div>

				<div class="grid gap-1.5">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						bind:value={password}
						bind:ref={passwordEl}
						autocomplete="current-password"
						disabled={submitting}
						required
					/>
				</div>

				{#if error}
					<p class="text-destructive text-sm">{error}</p>
				{/if}

				<Button type="submit" class="w-full" disabled={!canSubmit || submitting}>
					{#if submitting}
						<LoaderCircleIcon class="mr-2 size-4 animate-spin" />
						Signing in…
					{:else}
						Sign in
					{/if}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>

<style>
	.title-shimmer {
		background: linear-gradient(
			90deg,
			oklch(0.145 0 0) 0%,
			oklch(50% 0.134 242.749) 50%,
			oklch(0.145 0 0) 100%
		);
		background-size: 200% auto;
		background-clip: text;
		-webkit-background-clip: text;
		color: transparent;
		animation: shimmer 10s linear infinite;
	}

	@keyframes shimmer {
		0% { background-position: 200% center; }
		100% { background-position: -200% center; }
	}
</style>
