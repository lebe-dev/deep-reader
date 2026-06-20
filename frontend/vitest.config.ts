import { defineConfig } from 'vitest/config';
import { fileURLToPath } from 'node:url';

// Standalone config for unit tests of pure logic. We deliberately do NOT load
// the SvelteKit plugin here — the tested modules are framework-agnostic, and
// keeping the plugin out makes the test run fast and free of SvelteKit's
// dev-server machinery.
export default defineConfig({
	resolve: {
		alias: {
			$lib: fileURLToPath(new URL('./src/lib', import.meta.url)),
			// mode-watcher only exports under the `svelte` condition, so it can't be
			// resolved in the node test env — point it at a stub for tests.
			'mode-watcher': fileURLToPath(
				new URL('./src/lib/test-stubs/mode-watcher.ts', import.meta.url)
			)
		}
	},
	test: {
		environment: 'node',
		include: ['src/**/*.{test,spec}.ts']
	}
});
