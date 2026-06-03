// Reactive PWA update state — shared between bootstrap.ts and UpdateBanner.svelte.

let _updateAvailable = $state(false);
let _waitingWorker: ServiceWorker | null = null;

export const pwaState = {
	get updateAvailable() {
		return _updateAvailable;
	}
};

export function signalUpdateAvailable(worker: ServiceWorker): void {
	_waitingWorker = worker;
	_updateAvailable = true;
}

export function dismissUpdate(): void {
	_updateAvailable = false;
}

export async function checkForUpdate(): Promise<void> {
	if (!('serviceWorker' in navigator)) return;
	const registration = await navigator.serviceWorker.getRegistration();
	await registration?.update();
}

export function applyUpdate(): void {
	if (!_waitingWorker) return;

	// Register the reload BEFORE asking the worker to skip waiting, otherwise the
	// controllerchange could fire before we are listening for it. `once` guards
	// against a double reload if the event somehow fires twice.
	navigator.serviceWorker.addEventListener('controllerchange', () => location.reload(), {
		once: true
	});

	_waitingWorker.postMessage({ type: 'SKIP_WAITING' });
}
