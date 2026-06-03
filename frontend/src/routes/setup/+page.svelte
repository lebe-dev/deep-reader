<!-- First-run setup: create the single built-in account.
     Reachable only while the service is uninitialized (the layout guard routes
     here). On success the user is logged in and sent to the library. -->
<script lang="ts">
	import { goto } from '$app/navigation';
	import * as Card from '$lib/components/ui/card';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Button } from '$lib/components/ui/button';
	import { ApiError } from '$lib/api';
	import { setupAccount } from '$lib/auth/store.svelte';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';

	let username = $state('');
	let password = $state('');
	let confirm = $state('');
	let error = $state('');
	let submitting = $state(false);

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';

		if (!username.trim()) {
			error = 'Enter a username.';
			return;
		}
		if (password.length < 8) {
			error = 'Password must be at least 8 characters.';
			return;
		}
		if (password !== confirm) {
			error = 'Passwords do not match.';
			return;
		}

		submitting = true;
		try {
			await setupAccount(username.trim(), password);
			await goto('/');
		} catch (err) {
			if (err instanceof ApiError && err.status === 409) {
				error = 'This server is already set up. Please sign in instead.';
			} else if (err instanceof ApiError) {
				error = err.body || 'Setup failed. Check your input and try again.';
			} else {
				error = 'Could not reach the server. Try again.';
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
			<Card.Title>Welcome to Deep Reader</Card.Title>
			<Card.Description>Create your account to finish setup.</Card.Description>
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
						autocomplete="new-password"
						required
					/>
				</div>

				<div class="grid gap-1.5">
					<Label for="confirm">Confirm password</Label>
					<Input
						id="confirm"
						type="password"
						bind:value={confirm}
						autocomplete="new-password"
						required
					/>
				</div>

				{#if error}
					<p class="text-destructive text-sm">{error}</p>
				{/if}

				<Button type="submit" class="w-full" disabled={submitting}>
					{submitting ? 'Creating account…' : 'Create account'}
				</Button>
			</form>
		</Card.Content>
	</Card.Root>
</div>
