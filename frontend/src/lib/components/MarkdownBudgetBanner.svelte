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
	// Reactive "current UTC day". `isDismissed` reads this so a dismissal stops
	// sticking once the budget resets at UTC midnight even on a long-lived tab.
	// We avoid a per-second ticker: the day can only change at midnight, so we
	// schedule a single timer to the next UTC midnight and also recompute when
	// the tab regains visibility/focus (covers a slept/backgrounded device).
	let today = $state(utcDay());

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

		const refreshDay = () => {
			today = utcDay();
		};

		// Re-derive the day whenever the user returns to the tab — a backgrounded
		// timer may have been throttled/coalesced across midnight.
		document.addEventListener('visibilitychange', refreshDay);
		window.addEventListener('focus', refreshDay);

		// Tick once at the next UTC midnight (and re-arm for each subsequent day).
		let timer: ReturnType<typeof setTimeout>;
		const armMidnight = () => {
			timer = setTimeout(() => {
				refreshDay();
				armMidnight();
			}, msUntilNextUtcMidnight());
		};
		armMidnight();

		return () => {
			sub.unsubscribe();
			document.removeEventListener('visibilitychange', refreshDay);
			window.removeEventListener('focus', refreshDay);
			clearTimeout(timer);
		};
	});

	function utcDay(): string {
		return new Date().toISOString().slice(0, 10);
	}

	// Milliseconds from now until the next UTC midnight (00:00:00.000 UTC).
	function msUntilNextUtcMidnight(): number {
		const now = new Date();
		const next = Date.UTC(
			now.getUTCFullYear(),
			now.getUTCMonth(),
			now.getUTCDate() + 1,
			0,
			0,
			0,
			0
		);
		// Guard against a 0/negative interval (rounding at the boundary) so the
		// timer never tight-loops; fall back to ~1 minute.
		return Math.max(next - now.getTime(), 60_000);
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
	// hasn't dropped below the value at dismiss time. Reads the reactive `today`
	// (not utcDay() directly) so it re-evaluates when the UTC day rolls over.
	const isDismissed = $derived(
		dismiss !== null && dismiss.day === today && remaining >= dismiss.atRemaining
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
