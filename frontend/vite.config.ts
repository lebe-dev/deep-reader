import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import pkg from './package.json' with { type: 'json' };

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	// Expose the package.json version as a compile-time constant. Mobile builds
	// stamp VERSION+githash here via `just cap-sync-versioned` (MOBILE-ARCH.md §10.4).
	define: { __APP_VERSION__: JSON.stringify(pkg.version) },
	server: { port: 4200, allowedHosts: ['test.home'] }
});
