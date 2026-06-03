<!-- Server settings card.
     Shows non-secret deployment configuration received from GET /api/config.
     Secrets (LLM_API_KEY, password hashes) are never sent by the server and are not shown.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { liveQuery } from 'dexie';
	import * as Card from '$lib/components/ui/card';
	import { db, SYNC_STATE_ID } from '$lib/db';
	import { syncStatus } from '$lib/sync/store.svelte';
	import type { ServerInfo } from '$lib/types';

	// ---------------------------------------------------------------------------
	// State
	// ---------------------------------------------------------------------------

	let info = $state<ServerInfo | undefined>(undefined);

	// ---------------------------------------------------------------------------
	// Lifecycle
	// ---------------------------------------------------------------------------

	onMount(() => {
		const sub = liveQuery(() => db.sync_state.get(SYNC_STATE_ID)).subscribe({
			next(state) {
				if (state?.serverInfo) info = state.serverInfo;
			},
			error(err) {
				console.error('[settings] server info liveQuery error', err);
			}
		});

		return () => sub.unsubscribe();
	});

	// ---------------------------------------------------------------------------
	// Helpers
	// ---------------------------------------------------------------------------

	interface Row {
		label: string;
		value: string | number | boolean;
		hint?: string;
	}

	interface Section {
		title: string;
		rows: Row[];
	}

	function buildSections(s: ServerInfo): Section[] {
		return [
			{
				title: 'HTTP',
				rows: [
					{ label: 'HTTP_PORT', value: s.http_port },
					{ label: 'DATABASE_PATH', value: s.database_path }
				]
			},
			{
				title: 'LLM',
				rows: [
					{ label: 'LLM_API_BASE_URL', value: s.llm_api_base_url },
					{ label: 'LLM_MODEL', value: s.llm_model },
					{ label: 'LLM_MAX_CONCURRENT', value: s.llm_max_concurrent },
					{ label: 'LLM_REQUEST_TIMEOUT', value: s.llm_request_timeout },
					{ label: 'LLM_MAX_RETRIES', value: s.llm_max_retries }
				]
			},
			{
				title: 'Ingestion',
				rows: [
					{ label: 'READABILITY_TIMEOUT', value: s.readability_timeout },
					{ label: 'ENRICHMENT_VERSION', value: s.enrichment_version }
				]
			},
			{
				title: 'markdown.new',
				rows: [
					{ label: 'MARKDOWN_ENABLED', value: s.markdown_enabled },
					{ label: 'MARKDOWN_BASE_URL', value: s.markdown_base_url },
					{ label: 'MARKDOWN_TIMEOUT', value: s.markdown_timeout },
					{ label: 'MARKDOWN_DAILY_LIMIT', value: s.markdown_daily_limit },
					{ label: 'MARKDOWN_COST_PER_ARTICLE', value: s.markdown_cost_per_article }
				]
			},
			{
				title: 'Logging',
				rows: [
					{ label: 'LOG_LEVEL', value: s.log_level },
					{ label: 'LOG_FORMAT', value: s.log_format }
				]
			}
		];
	}

	function displayValue(v: string | number | boolean): string {
		if (typeof v === 'boolean') return v ? 'true' : 'false';
		return String(v);
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Server</Card.Title>
		<Card.Description>
			Deployment configuration from the server. Secrets are not shown.
		</Card.Description>
	</Card.Header>

	<Card.Content>
		{#if !info && syncStatus.error}
			<p class="text-destructive text-sm">
				Couldn't load server info: {syncStatus.error}.
			</p>
			<p class="text-muted-foreground text-sm">
				Check your server URL and auth token on the <strong>Device</strong> tab, then sync.
			</p>
		{:else if !info}
			<p class="text-muted-foreground text-sm">Loading…</p>
		{:else}
			<div class="space-y-5">
				{#each buildSections(info) as section (section.title)}
					<div>
						<p class="text-muted-foreground mb-2 text-xs font-medium uppercase tracking-wider">
							{section.title}
						</p>
						<div class="divide-border divide-y rounded-md border">
							{#each section.rows as row (row.label)}
								<div class="flex items-center justify-between gap-4 px-3 py-2">
									<code class="text-muted-foreground shrink-0 text-xs">{row.label}</code>
									<span class="min-w-0 truncate text-right text-xs font-medium">
										{displayValue(row.value)}
									</span>
								</div>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		{/if}
	</Card.Content>
</Card.Root>
