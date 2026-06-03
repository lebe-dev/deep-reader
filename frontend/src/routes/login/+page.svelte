<!-- Sign in with the single built-in account. The layout guard routes here when
     the service is initialized but the request is unauthenticated. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { ApiError, OfflineError } from '$lib/api';
	import { login } from '$lib/auth/store.svelte';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let submitting = $state(false);

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';

		if (!username.trim() || !password) {
			error = 'Enter your username and password.';
			return;
		}

		submitting = true;
		try {
			await login(username.trim(), password);
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
	<Card.Root class="w-full max-w-sm">
		<Card.Header class="text-center">
			<div class="mb-2 flex justify-center">
				<div class="bg-muted rounded-full p-3">
					<BookOpenIcon class="size-6" />
				</div>
			</div>
			<Card.Title>Sign in to Deep Reader</Card.Title>
			<Card.Description>Enter your account credentials.</Card.Description>
		</Card.Header>

		<Card.Content>
			<form class="space-y-4" onsubmit={handleSubmit}>
				<div class="grid gap-1.5">
					<Label for="username">Username</Label>
					<Input id="username" bind:value={username} autocomplete="username" required />
				</div>

				<div class="grid gap-1.5">
					<Label for="password">Password</Label>
					<Input
						id="password"
						type="password"
						bind:value={password}
						autocomplete="current-password"
						required
					/>
				</div>

				{#if error}
					<p class="text-destructive text-sm">{error}</p>
				{/if}

				<Button type="submit" class="w-full" disabled={submitting}>
					{submitting ? 'Signing in…' : 'Sign in'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
