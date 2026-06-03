<!-- Prominent low-budget banner for the markdown.new daily conversion quota.
     Shows when today's remaining conversions drop to / below the user-configured
     `markdown_warn_threshold` (synced setting, 0 = off). Two states:
       - warning  (remaining > 0): amber, dismissible
       - exhausted (remaining == 0): destructive
     Dismiss is remembered in localStorage and only sticks until the budget
     resets (a new UTC day) or the remaining count drops further. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import { liveQuery } from 'dexie';
	import { browser } from '$app/environment';
	import { syncStatus } from '$lib/sync/store.svelte';
	import { db, SYNC_STATE_ID } from '$lib/db';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import XIcon from '@lucide/svelte/icons/x';

	const DISMISS_KEY = 'dr.mdBudgetBannerDismiss';
	const DEFAULT_THRESHOLD = 5;

	type Dismiss = { day: string; atRemaining: number };

	// Synced threshold from the settings singleton (appears once boot sync runs).
	let threshold = $state(DEFAULT_THRESHOLD);
	let dismiss = $state<Dismiss | null>(null);

	onMount(() => {
		dismiss = loadDismiss();
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				threshold = state?.settings?.markdown_warn_threshold ?? DEFAULT_THRESHOLD;
			},
			error(err) {
				console.error('[md-budget-banner] sync_state liveQuery error', err);
			}
		});
		return () => sub.unsubscribe();
	});

	function utcDay(): string {
		return new Date().toISOString().slice(0, 10);
	}

	function loadDismiss(): Dismiss | null {
		if (!browser) return null;
		try {
			const raw = localStorage.getItem(DISMISS_KEY);
			if (!raw) return null;
			const parsed = JSON.parse(raw) as Dismiss;
			if (typeof parsed?.day === 'string' && typeof parsed?.atRemaining === 'number') return parsed;
		} catch {
			// Corrupt entry — treat as no dismissal.
		}
		return null;
	}

	const budget = $derived(syncStatus.markdownBudget);
	const remaining = $derived(budget?.articles_remaining ?? 0);
	const active = $derived(!!budget?.enabled && budget.daily_limit > 0 && threshold > 0);
	const exhausted = $derived(active && remaining <= 0);

	// A dismissal sticks only for the same UTC day and while the remaining count
	// hasn't dropped below the value at dismiss time.
	const isDismissed = $derived(
		dismiss !== null && dismiss.day === utcDay() && remaining >= dismiss.atRemaining
	);

	const visible = $derived(active && remaining <= threshold && !isDismissed);

	function handleDismiss() {
		const entry: Dismiss = { day: utcDay(), atRemaining: remaining };
		dismiss = entry;
		if (browser) {
			try {
				localStorage.setItem(DISMISS_KEY, JSON.stringify(entry));
			} catch {
				// Storage unavailable (private mode) — dismissal lasts this session only.
			}
		}
	}

	function plural(n: number, one: string, few: string, many: string): string {
		const mod10 = n % 10;
		const mod100 = n % 100;
		if (mod10 === 1 && mod100 !== 11) return one;
		if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return few;
		return many;
	}
</script>

{#if visible}
	<div
		class={[
			'flex items-center justify-between gap-3 px-4 py-2 text-sm',
			exhausted
				? 'bg-destructive/15 text-destructive'
				: 'bg-amber-500/15 text-amber-700 dark:text-amber-400'
		]}
		role="status"
		aria-live="polite"
	>
		<span class="flex items-center gap-2">
			{#if exhausted}
				<TriangleAlertIcon class="size-4 shrink-0" />
				Лимит markdown.new исчерпан — новые статьи извлекаются встроенным экстрактором до сброса (UTC
				midnight).
			{:else}
				<SparklesIcon class="size-4 shrink-0" />
				Осталось ~{remaining}
				{plural(remaining, 'конвертация', 'конвертации', 'конвертаций')} markdown.new сегодня ({budget!
					.units_remaining}/{budget!.daily_limit} единиц).
			{/if}
		</span>
		<button
			onclick={handleDismiss}
			aria-label="Скрыть"
			class="rounded-md p-1 transition-colors hover:bg-black/10 dark:hover:bg-white/10"
		>
			<XIcon class="size-4" />
		</button>
	</div>
{/if}
