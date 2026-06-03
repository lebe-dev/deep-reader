import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	compilerOptions: {
		// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
		runes: ({ filename }) => (filename.split(/[/\\]/).includes('node_modules') ? undefined : true)
	},
	kit: {
		// SPA / PWA mode: emit a self-contained single-page app. The build is copied
		// into the Go module's web/dist and embedded, served from origin root.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: 'index.html',
			precompress: false,
			strict: false
		}),
		// Served from origin root.
		paths: { base: '' },
		// Do NOT let SvelteKit auto-register the service worker. In production it
		// injects `navigator.serviceWorker.register('/service-worker.js')` with NO
		// options — a *classic* registration — while bootstrap.ts registers the
		// same URL as a *module*. Two registrations of one scope with conflicting
		// `type` make the browser reinstall the worker on every navigation, so it
		// is permanently stuck "waiting" and the update banner never clears.
		// bootstrap.ts is our single registrar (it also owns the update-banner UX).
		serviceWorker: { register: false }
	}
};

export default config;
