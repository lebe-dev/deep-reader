// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces
declare global {
	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		// interface PageState {}
		// interface Platform {}
	}

	// Client build version, injected by Vite from package.json (vite.config.ts).
	// On mobile sideload builds this carries VERSION + short commit hash so the
	// Settings screen can report exactly which build is installed (MOBILE-ARCH.md §10.4).
	const __APP_VERSION__: string;
}

export {};
